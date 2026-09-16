package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPolicy(t *testing.T) {
	// Rules are deliberately ordered loosest-first so a naive first-match
	// matcher fails: the exact rule MUST beat the earlier catch-all.
	manifest := &Manifest{
		Path: "test-manifest.txt",
		Rules: []ManifestRule{
			{Policy: PolicyAsk, Pattern: "*", Description: "Catch-all"},
			{Policy: PolicyDeny, Pattern: "schwab/*", Description: "Financial"},
			{Policy: PolicyAllow, Pattern: "kurultai/*", Description: "Agent bus"},
			{Policy: PolicyAllow, Pattern: "openrouter/default", Description: "Primary LLM"},
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
	p, d = manifest.CheckPolicy("schwab", "testuser1")
	if p != PolicyDeny || d != "Financial" {
		t.Errorf("expected DENY for schwab/testuser1, got %s, %s", p, d)
	}

	// Fallback catch-all
	p, _ = manifest.CheckPolicy("unknown", "account")
	if p != PolicyAsk {
		t.Errorf("expected ASK for unknown, got %s", p)
	}
}

// TestManifestRoundTrip writes a generated manifest and reloads it: every
// emitted rule must parse back to the same policy for the same item, and
// names that would corrupt the space-separated format must be skipped.
func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "omaseal"), 0o700); err != nil {
		t.Fatal(err)
	}

	items := []Item{
		{Service: "openrouter", Account: "default"},
		{Service: "github", Account: "work"},
		{Service: "bank", Account: "checking"},
		{Service: "My Imported App", Account: "My Bank Login"}, // spaces: must be skipped
	}
	path, err := manifestDir()
	if err != nil {
		t.Fatal(err)
	}
	content, skipped := GenerateDefaultManifest(items)
	if len(skipped) != 1 || skipped[0] != "My Imported App/My Bank Login" {
		t.Fatalf("skipped = %v, want [My Imported App/My Bank Login]", skipped)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items[:3] {
		wantP, _ := defaultPolicyFor(it.Service)
		gotP, _ := m.CheckPolicy(it.Service, it.Account)
		if gotP != wantP {
			t.Errorf("round-trip policy for %s/%s: want %s, got %s", it.Service, it.Account, wantP, gotP)
		}
	}
	// The space-named item must not have produced a truncated rule that
	// would match an unintended target.
	if p, _ := m.CheckPolicy("My", "anything"); p != PolicyAsk {
		t.Errorf("space-name leak: 'My' should hit catch-all ASK, got %s", p)
	}
}
