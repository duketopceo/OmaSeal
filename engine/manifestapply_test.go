package main

import (
	"strings"
	"testing"
)

func TestGovernsJevCredential(t *testing.T) {
	for _, p := range []string{"openrouter/default", "openrouter/*", "OPENROUTER/jev", "*", "*/*", " openrouter/default "} {
		if !governsJevCredential(p) {
			t.Errorf("expected %q to govern jev credential path", p)
		}
	}
	for _, p := range []string{"github/*", "openai/key", "kurultai/api"} {
		if governsJevCredential(p) {
			t.Errorf("unexpected jev-credential match: %q", p)
		}
	}
}

func TestIsExpansionChange(t *testing.T) {
	m := &Manifest{Rules: []ManifestRule{
		{Policy: PolicyDeny, Pattern: "bank/*"},
		{Policy: PolicyAsk, Pattern: "github/*"},
		{Policy: PolicyAllow, Pattern: "openrouter/default"},
		{Policy: PolicyAsk, Pattern: "*"},
	}}
	cases := []struct {
		name string
		c    ProposalChange
		want bool
	}{
		{"deny removal", ProposalChange{Action: "remove", Pattern: "bank/*"}, true},
		{"allow removal", ProposalChange{Action: "remove", Pattern: "openrouter/default"}, false},
		{"ask removal", ProposalChange{Action: "remove", Pattern: "github/*"}, false},
		{"ask to allow", ProposalChange{Action: "set", Policy: "ALLOW", Pattern: "github/*"}, true},
		{"allow to ask", ProposalChange{Action: "set", Policy: "ASK", Pattern: "openrouter/default"}, false},
		{"ask to deny", ProposalChange{Action: "set", Policy: "DENY", Pattern: "github/*"}, false},
		{"new allow", ProposalChange{Action: "add", Policy: "ALLOW", Pattern: "new/svc"}, true},
		{"new ask", ProposalChange{Action: "add", Policy: "ASK", Pattern: "new/svc"}, false},
		{"new deny", ProposalChange{Action: "add", Policy: "DENY", Pattern: "new/svc"}, false},
		{"new allow wildcard", ProposalChange{Action: "add", Policy: "ALLOW", Pattern: "new/*"}, true},
	}
	for _, tc := range cases {
		if got := isExpansionChange(m, tc.c); got != tc.want {
			t.Errorf("%s: isExpansionChange = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestApplyChanges(t *testing.T) {
	content := `# header comment
ALLOW  openrouter/default               - key
ASK    github/*                         - repo access
DENY   bank/*                           - money
ASK    *                                - fallback
`
	out := applyChanges(content, []ProposalChange{
		{Action: "remove", Pattern: "bank/*"},
		{Action: "set", Policy: "ALLOW", Pattern: "github/*", Description: "trusted"},
		{Action: "add", Policy: "DENY", Pattern: "schwab/*", Description: "broker"},
	})
	if !strings.Contains(out, "# header comment") {
		t.Error("comment scaffolding lost")
	}
	if strings.Contains(out, "bank/*") {
		t.Error("remove did not drop bank/*")
	}
	if !strings.Contains(out, "ALLOW  github/*") || !strings.Contains(out, "trusted") {
		t.Error("set did not rewrite github/*")
	}
	if !strings.Contains(out, "DENY   schwab/*") {
		t.Error("add did not append schwab/*")
	}
	if !strings.Contains(out, "ASK    *") {
		t.Error("fallback rule lost")
	}
}

func TestApplyChangesReparse(t *testing.T) {
	content := "ALLOW  a/one - x\nASK    * - fallback\n"
	out := applyChanges(content, []ProposalChange{
		{Action: "set", Policy: "DENY", Pattern: "a/one", Description: "lockdown"},
	})
	// Reparse the result through the real loader path.
	m := &Manifest{Rules: nil}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 && !strings.HasPrefix(ln, "#") {
			m.Rules = append(m.Rules, ManifestRule{Policy: RulePolicy(strings.ToUpper(f[0])), Pattern: f[1]})
		}
	}
	p, _ := m.CheckPolicy("a", "one")
	if p != PolicyDeny {
		t.Fatalf("applied manifest does not check DENY for a/one, got %s", p)
	}
}
