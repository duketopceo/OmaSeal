package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{Service: "My Imported App", Account: "My Bank Login"}, // spaces: emitted as a quoted rule
	}
	path, err := manifestDir()
	if err != nil {
		t.Fatal(err)
	}
	content, skipped := GenerateDefaultManifest(items)
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none — spaced names are quoted now", skipped)
	}
	if !strings.Contains(content, `"My Imported App/My Bank Login"`) {
		t.Fatalf("spaced item must appear as a quoted rule:\n%s", content)
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
	// The space-named item's own rule must govern it (the old space-split
	// truncated DENY rules into dead/mis-scoped patterns — a real bypass).
	wantP, _ := defaultPolicyFor("My Imported App")
	if p, _ := m.CheckPolicy("My Imported App", "My Bank Login"); p != wantP {
		t.Errorf("spaced item policy: want %s, got %s", wantP, p)
	}
	// And it must not produce a truncated rule matching an unintended target.
	if p, _ := m.CheckPolicy("My", "anything"); p != PolicyAsk {
		t.Errorf("space-name leak: 'My' should hit catch-all ASK, got %s", p)
	}
}

func TestManifestQuotedSpacedDeny(t *testing.T) {
	// A DENY on a spaced name must actually deny — the pre-fix parser
	// truncated the pattern at the first space, silently downgrading it.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "omaseal"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := manifestDir()
	if err != nil {
		t.Fatal(err)
	}
	content := `DENY "My Imported App/My Bank Login" - finance data
ASK * - fallback
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := m.CheckPolicy("My Imported App", "My Bank Login"); p != PolicyDeny {
		t.Fatalf("quoted DENY not enforced: got %s", p)
	}
	if p, _ := m.CheckPolicy("other", "thing"); p != PolicyAsk {
		t.Fatalf("catch-all broken: got %s", p)
	}
	// Truncated-prefix must not match — the quoted rule names the full item.
	if p, _ := m.CheckPolicy("My Imported App", "other"); p != PolicyAsk {
		t.Fatalf("partial name leaked into DENY: got %s", p)
	}
}

func TestParseRuleLine(t *testing.T) {
	cases := []struct {
		line                  string
		wantPolicy            RulePolicy
		wantPattern, wantDesc string
		wantOK                bool
	}{
		{`DENY github/* - source control`, PolicyDeny, "github/*", "source control", true},
		{`ALLOW a/b`, PolicyAllow, "a/b", "", true},
		{`ASK "Svc With Space/acct name" - desc`, PolicyAsk, "Svc With Space/acct name", "desc", true},
		{`DENY "Svc/acct"`, PolicyDeny, "Svc/acct", "", true},
		{`DENY "unterminated`, "", "", "", false},
		{`# DENY x/y - commented`, "", "", "", false},
		{`   # indented comment`, "", "", "", false},
		{`DЕNY x/y`, "", "", "", false}, // Cyrillic Е — not a policy token
		{`DENNY x/y`, "", "", "", false},
		{``, "", "", "", false},
		{`DENY`, "", "", "", false},
	}
	for _, c := range cases {
		p, pat, d, ok := parseRuleLine(c.line)
		if ok != c.wantOK || p != c.wantPolicy || pat != c.wantPattern || d != c.wantDesc {
			t.Errorf("%q: got (%s,%q,%q,%v), want (%s,%q,%q,%v)",
				c.line, p, pat, d, ok, c.wantPolicy, c.wantPattern, c.wantDesc, c.wantOK)
		}
	}
}

func TestAuditBadTokenAndShadowedRules(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "omaseal"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := manifestDir()
	if err != nil {
		t.Fatal(err)
	}
	content := `ALLOW x/y - ok
DENY x/y - shadowed by the earlier ALLOW
DENYY github/* - typo token, silently dropped before
ALLOW a/b - fine
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{Service: "x", Account: "y", AccessCount: 1},
		{Service: "a", Account: "b", AccessCount: 1},
	}
	findings := auditManifest(m, items, time.Now(), true)
	var badToken, shadowed bool
	for _, f := range findings {
		if strings.Contains(f.Detail, "does not start a valid") {
			badToken = true
		}
		if strings.Contains(f.Detail, "duplicates line") {
			shadowed = true
		}
	}
	if !badToken {
		t.Fatalf("unrecognized-token line not flagged: %+v", findings)
	}
	if !shadowed {
		t.Fatalf("shadowed duplicate rule not flagged: %+v", findings)
	}
}
