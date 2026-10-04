package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// omasealConfig is ~/.config/omaseal/config.json — store/backend selection
// and other local (non-agent-policy) settings. agent.json stays scoped to
// agent policy; this file owns everything else.
type omasealConfig struct {
	// Backend selects the secret store: "secretservice" (default, gnome-
	// keyring via D-Bus) or "native" (age-encrypted file store — see
	// docs/design/native-store.md).
	Backend string `json:"backend"`
}

func omasealConfigPath() string {
	dir := omasealConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.json")
}

func loadConfig() (omasealConfig, error) {
	cfg := omasealConfig{Backend: "secretservice"}
	path := omasealConfigPath()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if cfg.Backend == "" {
		cfg.Backend = "secretservice"
	}
	return cfg, nil
}

// selectStore installs the configured backend as currentStore. Called once
// at startup before command dispatch — every op lane (CLI, IPC, MCP,
// providers) routes through the package dispatchers after this.
func selectStore() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	switch cfg.Backend {
	case "", "secretservice":
		currentStore = ssStore{}
	case "native":
		currentStore = newNativeStore()
	default:
		return fmt.Errorf("unknown backend %q in %s (want secretservice or native)", cfg.Backend, omasealConfigPath())
	}
	return nil
}
