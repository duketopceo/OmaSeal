package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var mcpAgentConfig = map[string]struct {
	dir  string
	file string
}{
	"claude": {dir: ".claude", file: "mcp.json"},
	"codex":  {dir: ".codex", file: "mcp.json"},
}

type mcpConfig struct {
	MCPServers map[string]interface{} `json:"mcpServers"`
}

func handleMCPInstall() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: omaseal mcp install <claude|codex>")
		os.Exit(1)
	}

	agent := os.Args[3]
	if err := installMCP(agent); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("OmaSeal MCP installed for %s.\n", agent)
}

func installMCP(agent string) error {
	meta, ok := mcpAgentConfig[agent]
	if !ok {
		return fmt.Errorf("unknown agent %q; try claude or codex", agent)
	}

	self, err := os.Executable()
	if err != nil {
		self = "omaseal"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("cannot determine user home directory")
	}

	agentDir := filepath.Join(home, meta.dir)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", agentDir, err)
	}

	configPath := filepath.Join(agentDir, meta.file)
	var cfg mcpConfig
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]interface{}{}
	}
	cfg.MCPServers["omaseal"] = map[string]interface{}{
		"command": self,
		"args":    []string{"mcp"},
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(configPath, b, 0644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}
