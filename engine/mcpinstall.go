package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var mcpAgentConfig = map[string]struct {
	dir  string
	file string
}{
	"claude":      {dir: ".claude", file: "mcp.json"},
	"codex":       {dir: ".codex", file: "mcp.json"},
	"cursor":      {dir: ".cursor", file: "mcp.json"},
	"devin":       {dir: ".devin", file: "mcp.json"},
	"agy":         {dir: ".agy", file: "mcp.json"},
	"antigravity": {dir: ".agy", file: "mcp.json"},
	"hermes":      {dir: ".hermes", file: "mcp.json"},
}

// canonical agents only; aliases share the same directory and should not
// be double-written by install-all.
var mcpCanonicalAgents = []string{"claude", "codex", "cursor", "devin", "agy", "hermes"}

func handleMCPInstall() {
	if len(os.Args) < 4 {
		mcpInstallUsage()
		os.Exit(1)
	}

	agent := os.Args[3]
	fs := flag.NewFlagSet("mcp-install", flag.ContinueOnError)
	dir := fs.String("dir", "", "Install .<agent>/mcp.json in this directory instead of the home directory")
	if err := fs.Parse(os.Args[4:]); err != nil {
		printError("install: ", err)
		mcpInstallUsage()
		os.Exit(1)
	}

	installedPath, err := installMCP(agent, *dir)
	if err != nil {
		printError("install: ", err)
		os.Exit(1)
	}
	fmt.Printf("OmaSeal MCP installed for %s at %s.\n", agent, installedPath)
}

func handleMCPInstallAll() {
	fs := flag.NewFlagSet("mcp-install-all", flag.ContinueOnError)
	dir := fs.String("dir", "", "Install .<agent>/mcp.json directories in this path instead of the home directory")
	// accept positional path too: `mcp install-all [path]`
	if err := fs.Parse(os.Args[3:]); err != nil {
		printError("install-all: ", err)
		mcpInstallAllUsage()
		os.Exit(1)
	}

	targetDir := *dir
	if fs.NArg() > 0 && targetDir == "" {
		targetDir = fs.Arg(0)
	}

	errs := []string{}
	installed := []string{}
	for _, agent := range mcpCanonicalAgents {
		path, err := installMCP(agent, targetDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", agent, err))
		} else {
			installed = append(installed, path)
		}
	}
	for _, p := range installed {
		fmt.Println("installed:", p)
	}
	if len(errs) > 0 {
		printError("install-all: ", errors.New(strings.Join(errs, "; ")))
		os.Exit(1)
	}
}

func mcpInstallUsage() {
	fmt.Fprintln(os.Stderr, "usage: omaseal mcp install <claude|codex|cursor|devin|agy|antigravity|hermes> [--dir <path>]")
	fmt.Fprintln(os.Stderr, "       --dir .   writes .<agent>/mcp.json in the current repo")
}

func mcpInstallAllUsage() {
	fmt.Fprintln(os.Stderr, "usage: omaseal mcp install-all [path]")
	fmt.Fprintln(os.Stderr, "       omaseal mcp install-all --dir <path>")
	fmt.Fprintln(os.Stderr, "  Writes .claude/mcp.json, .codex/mcp.json, .cursor/mcp.json,")
	fmt.Fprintln(os.Stderr, "  .devin/mcp.json, .agy/mcp.json, and .hermes/mcp.json in one pass.")
}

func installMCP(agent, dir string) (string, error) {
	meta, ok := mcpAgentConfig[agent]
	if !ok {
		return "", fmt.Errorf("unknown agent %q; try claude, codex, cursor, devin, agy, or hermes", agent)
	}

	self, err := os.Executable()
	if err != nil {
		self = "omaseal"
	}

	base := ""
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolve --dir: %w", err)
		}
		base = abs
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot determine user home directory")
		}
		base = home
	}

	agentDir := filepath.Join(base, meta.dir)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return "", fmt.Errorf("create %s: %w", agentDir, err)
	}

	configPath := filepath.Join(agentDir, meta.file)

	// Preserve every top-level key we do not recognize (e.g. agent-specific
	// settings). Surface JSON parse errors instead of silently overwriting.
	rawConfig := map[string]json.RawMessage{}
	if data, err := os.ReadFile(configPath); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &rawConfig); err != nil {
				return "", fmt.Errorf("parse %s: %w", configPath, err)
			}
		}
	}

	servers := map[string]interface{}{}
	if raw, ok := rawConfig["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return "", fmt.Errorf("parse %s mcpServers: %w", configPath, err)
		}
	}
	servers["omaseal"] = map[string]interface{}{
		"type":    "stdio",
		"command": self,
		"args":    []string{"mcp"},
	}
	rawConfig["mcpServers"] = mustRawJSON(servers)

	out := map[string]interface{}{}
	for k, v := range rawConfig {
		if k == "mcpServers" {
			out[k] = servers
		} else {
			out[k] = v
		}
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(configPath, b, 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", configPath, err)
	}
	return configPath, nil
}

func mustRawJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}
