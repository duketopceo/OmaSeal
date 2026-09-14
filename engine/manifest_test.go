package main

import (
	"testing"
)

func TestCheckPolicy(t *testing.T) {
	manifest := &Manifest{
		Path: "test-manifest.txt",
		Rules: []ManifestRule{
			{Policy: PolicyAllow, Pattern: "openrouter/default", Description: "Primary LLM"},
			{Policy: PolicyAllow, Pattern: "kurultai/*", Description: "Agent bus"},
			{Policy: PolicyDeny, Pattern: "schwab/*", Description: "Financial"},
			{Policy: PolicyAsk, Pattern: "*", Description: "Catch-all"},
		},
	}

	// Exact match
	p, d := manifest.CheckPolicy("openrouter", "default")
	if p != PolicyAllow || d != "Primary LLM" {
		t.Errorf("expected ALLOW for openrouter/default, got %s, %s", p, d)
	}

	// Wildcard match
	p, d = manifest.CheckPolicy("kurultai", "antigravity")
	if p != PolicyAllow || d != "Agent bus" {
		t.Errorf("expected ALLOW for kurultai/antigravity, got %s, %s", p, d)
	}

	// Deny match
	p, d = manifest.CheckPolicy("schwab", "lukedaduke2789")
	if p != PolicyDeny || d != "Financial" {
		t.Errorf("expected DENY for schwab/lukedaduke2789, got %s, %s", p, d)
	}

	// Fallback catch-all
	p, _ = manifest.CheckPolicy("unknown", "account")
	if p != PolicyAsk {
		t.Errorf("expected ASK for unknown, got %s", p)
	}
}
