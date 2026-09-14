package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCodexTOMLWritesServersTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := mergeCodexTOML(path, "/usr/bin/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	// Golden: codex reads [mcp_servers.<name>], never mcpServers.
	if !strings.Contains(got, "[mcp_servers.omaseal]") {
		t.Fatalf("missing [mcp_servers.omaseal] table:\n%s", got)
	}
	if strings.Contains(got, "mcpServers") {
		t.Fatalf("wrote mcpServers (wrong table family):\n%s", got)
	}
	if !strings.Contains(got, `command = "/usr/bin/omaseal"`) || !strings.Contains(got, `args = ["mcp"]`) {
		t.Fatalf("bad table body:\n%s", got)
	}
}

func TestMergeCodexTOMLPreservesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	existing := "# codex config\nmodel = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"/bin/other\"\n\n[projects.\"/repo\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeCodexTOML(path, "/b/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	for _, want := range []string{"model = \"gpt-5\"", "[mcp_servers.other]", "[projects.\"/repo\"]", "trust_level = \"trusted\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "[mcp_servers.omaseal]\ncommand = \"/b/omaseal\"\nargs = [\"mcp\"]\n") {
		t.Fatalf("omaseal table not appended cleanly:\n%s", got)
	}
	// Mode preserved
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", st.Mode().Perm())
	}
}

func TestMergeCodexTOMLRepairsLegacyTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// The pre-fix spelling must be replaced, not duplicated.
	existing := "[mcpServers.omaseal]\ncommand = \"/old/omaseal\"\nargs = [\"mcp\"]\n\n[mcp_servers.other]\ncommand = \"/bin/x\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeCodexTOML(path, "/new/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Contains(got, "mcpServers") || strings.Contains(got, "/old/omaseal") {
		t.Fatalf("legacy table not repaired:\n%s", got)
	}
	if strings.Count(got, ".omaseal]") != 1 || !strings.Contains(got, `command = "/new/omaseal"`) {
		t.Fatalf("expected exactly one fresh omaseal table:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.other]") {
		t.Fatalf("lost sibling table:\n%s", got)
	}
}

func TestMergeCodexTOMLReplacesCurrentTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	existing := "[mcp_servers.omaseal]\ncommand = \"/old/omaseal\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeCodexTOML(path, "/new/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if strings.Count(got, "[mcp_servers.omaseal]") != 1 || !strings.Contains(got, `command = "/new/omaseal"`) {
		t.Fatalf("existing table not replaced:\n%s", got)
	}
}

func TestMergeJSONConfig(t *testing.T) {
	spec, ok := findAgentSpec("claude")
	if !ok {
		t.Fatal("claude spec missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	// Fresh file
	if err := mergeJSONConfig(path, spec, "/b/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	entry, _ := servers["omaseal"].(map[string]any)
	if entry["command"] != "/b/omaseal" {
		t.Fatalf("bad entry: %v", servers)
	}

	// Merge preserves unknown keys + sibling servers, replaces omaseal only
	prior := `{"theme":"dark","mcpServers":{"other":{"command":"/x"},"omaseal":{"command":"/old"}}}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSONConfig(path, spec, "/new/omaseal"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["theme"] != "dark" {
		t.Fatalf("lost unrelated key: %v", cfg)
	}
	servers, _ = cfg["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatalf("lost sibling server: %v", servers)
	}
	if servers["omaseal"].(map[string]any)["command"] != "/new/omaseal" {
		t.Fatalf("omaseal entry not replaced: %v", servers["omaseal"])
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", st.Mode().Perm())
	}

	// Malformed JSON errors instead of truncating
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mergeJSONConfig(path, spec, "/b/omaseal"); err == nil {
		t.Fatal("want parse error on malformed JSON")
	}
}

func TestWriteFileModeAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	if err := writeFileMode(path, []byte("v1"), 0o640); err != nil {
		t.Fatal(err)
	}
	// No temp residue
	if names, _ := filepath.Glob(filepath.Join(dir, ".omaseal-*.tmp")); len(names) != 0 {
		t.Fatalf("temp files left: %v", names)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", st.Mode().Perm())
	}
}

func TestFindAgentSpecAliases(t *testing.T) {
	for _, name := range []string{"agy", "antigravity"} {
		s, ok := findAgentSpec(name)
		if !ok || s.name != "agy" {
			t.Fatalf("findAgentSpec(%q) = %v,%v", name, s.name, ok)
		}
	}
	if s, ok := findAgentSpec("claude-code"); !ok || s.name != "claude" {
		t.Fatalf("claude-code alias = %v,%v", s.name, ok)
	}
	if _, ok := findAgentSpec("nosuchagent"); ok {
		t.Fatal("unknown agent resolved")
	}
}

func TestMCPInstalledAt(t *testing.T) {
	dir := t.TempDir()
	claude, _ := findAgentSpec("claude")
	codex, _ := findAgentSpec("codex")

	jsonPath := filepath.Join(dir, "mcp.json")
	if mcpInstalledAt(claude, jsonPath) {
		t.Fatal("false positive on missing file")
	}
	os.WriteFile(jsonPath, []byte(`{"mcpServers":{"omaseal":{}}}`), 0o644)
	if !mcpInstalledAt(claude, jsonPath) {
		t.Fatal("missed JSON entry")
	}

	tomlPath := filepath.Join(dir, "config.toml")
	os.WriteFile(tomlPath, []byte("[mcp_servers.omaseal]\ncommand = \"/b\"\n"), 0o644)
	if !mcpInstalledAt(codex, tomlPath) {
		t.Fatal("missed mcp_servers table")
	}
	// Legacy spelling still detected so installs repair it
	os.WriteFile(tomlPath, []byte("[mcpServers.omaseal]\ncommand = \"/b\"\n"), 0o644)
	if !mcpInstalledAt(codex, tomlPath) {
		t.Fatal("missed legacy mcpServers table")
	}
	os.WriteFile(tomlPath, []byte("[mcp_servers.other]\n"), 0o644)
	if mcpInstalledAt(codex, tomlPath) {
		t.Fatal("false positive on unrelated table")
	}
}
