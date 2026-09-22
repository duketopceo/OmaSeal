package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func auditItems() []Item {
	return []Item{
		{Service: "openrouter", Account: "default", AccessCount: 12},
		{Service: "github", Account: "work", AccessCount: 3},
		{Service: "bank", Account: "checking"},
		{Service: "lonely", Account: "svc", AccessCount: 1},
	}
}

func auditManifestFixture() *Manifest {
	return &Manifest{
		Path: "test-manifest.txt",
		Rules: []ManifestRule{
			{Policy: PolicyAllow, Pattern: "openrouter/default"},
			{Policy: PolicyAsk, Pattern: "github/*"},
			{Policy: PolicyDeny, Pattern: "gone/dead"},
			{Policy: PolicyAsk, Pattern: "*"},
		},
	}
}

func findingKinds(findings []AuditFinding) map[string][]string {
	out := map[string][]string{}
	for _, f := range findings {
		out[f.Kind] = append(out[f.Kind], f.Target)
	}
	return out
}

func TestAuditDeadRule(t *testing.T) {
	kinds := findingKinds(auditManifest(auditManifestFixture(), auditItems(), time.Now()))
	found := false
	for _, target := range kinds["dead_rule"] {
		if target == "gone/dead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dead_rule finding for gone/dead, got %+v", kinds)
	}
}

func TestAuditUncovered(t *testing.T) {
	kinds := findingKinds(auditManifest(auditManifestFixture(), auditItems(), time.Now()))
	found := false
	for _, target := range kinds["uncovered"] {
		if target == "lonely/svc" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected uncovered finding for lonely/svc, got %+v", kinds)
	}
	// Items with explicit or wildcard rules must NOT be flagged uncovered.
	for _, target := range kinds["uncovered"] {
		if target == "github/work" || target == "openrouter/default" {
			t.Fatalf("covered item flagged uncovered: %s", target)
		}
	}
}

func TestAuditStale(t *testing.T) {
	old := time.Now().Add(-120 * 24 * time.Hour)
	items := append(auditItems(),
		Item{Service: "old", Account: "thing", AccessCount: 5, LastAccessed: &old},
		Item{Service: "never", Account: "used"},
	)
	kinds := findingKinds(auditManifest(auditManifestFixture(), items, time.Now()))
	var gotOld, gotNever bool
	for _, target := range kinds["stale"] {
		gotOld = gotOld || target == "old/thing"
		gotNever = gotNever || target == "never/used"
		if target == "github/work" {
			t.Fatalf("recently-used item flagged stale")
		}
	}
	if !gotOld || !gotNever {
		t.Fatalf("expected stale findings for old/thing and never/used, got %+v", kinds["stale"])
	}
}

func TestAuditClean(t *testing.T) {
	m := &Manifest{Rules: []ManifestRule{
		{Policy: PolicyAllow, Pattern: "a/one"},
		{Policy: PolicyAsk, Pattern: "b/*"},
		{Policy: PolicyAsk, Pattern: "*"},
	}}
	recent := time.Now()
	items := []Item{
		{Service: "a", Account: "one", AccessCount: 2, LastAccessed: &recent},
		{Service: "b", Account: "two", AccessCount: 1, LastAccessed: &recent},
	}
	findings := auditManifest(m, items, time.Now())
	if len(findings) != 0 {
		t.Fatalf("expected no findings on clean manifest, got %+v", findings)
	}
}

func TestAuditAdvisories(t *testing.T) {
	items := []Item{
		{Service: "has space", Account: "name"},
		{Service: "test", Account: "service"},
		{Service: "openrouter", Account: "default", AccessCount: 1},
	}
	kinds := findingKinds(auditManifest(&Manifest{Path: "x", Rules: []ManifestRule{
		{Policy: PolicyAllow, Pattern: "openrouter/default"},
		{Policy: PolicyAsk, Pattern: "*"},
	}}, items, time.Now()))
	var ws, ts bool
	for _, target := range kinds["advisory"] {
		ws = ws || target == "has space/name"
		ts = ts || target == "test/service"
	}
	if !ws || !ts {
		t.Fatalf("expected advisories for whitespace name and test service, got %+v", kinds["advisory"])
	}
}

func TestAuditCatchAllNeverDead(t *testing.T) {
	m := &Manifest{Rules: []ManifestRule{{Policy: PolicyAsk, Pattern: "*"}}}
	findings := auditManifest(m, nil, time.Now())
	for _, f := range findings {
		if f.Kind == "dead_rule" {
			t.Fatalf("catch-all reported dead: %+v", f)
		}
	}
}

func TestProposalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "omaseal"), 0o700); err != nil {
		t.Fatal(err)
	}
	mpath := filepath.Join(dir, "omaseal", "ai-manifest.txt")
	content := "ALLOW openrouter/default - key\nASK    * - fallback\n"
	if err := os.WriteFile(mpath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := manifestFileSHA256(mpath)
	if err != nil {
		t.Fatal(err)
	}
	ppath := filepath.Join(dir, "proposal.json")
	p := &Proposal{Version: 1, GeneratedAt: time.Now().UTC(), Generator: "omaseal audit",
		ManifestSHA256: sum, Changes: []ProposalChange{{Action: "remove", Pattern: "openrouter/default"}}}
	if err := writeProposal(ppath, p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(ppath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("proposal perms = %o, want 600", st.Mode().Perm())
	}
	data, _ := os.ReadFile(ppath)
	var back Proposal
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ManifestSHA256 != sum || len(back.Changes) != 1 || back.Changes[0].Pattern != "openrouter/default" {
		t.Fatalf("proposal did not round-trip: %+v", back)
	}
}

func TestSuggestChangesOnlyDeadRules(t *testing.T) {
	findings := []AuditFinding{
		{Kind: "dead_rule", Target: "gone/x"},
		{Kind: "uncovered", Target: "a/b"},
		{Kind: "stale", Target: "c/d"},
	}
	changes := suggestChanges(findings)
	if len(changes) != 1 || changes[0].Action != "remove" || changes[0].Pattern != "gone/x" {
		t.Fatalf("expected one remove change for the dead rule, got %+v", changes)
	}
}
