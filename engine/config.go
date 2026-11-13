package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// omasealConfig is ~/.config/omaseal/config.json — store/backend selection
// and other local (non-agent-policy) settings. agent.json stays scoped to
// agent policy; this file owns everything else.
type omasealConfig struct {
	// Backend selects the secret store: "secretservice" (default, gnome-
	// keyring via D-Bus) or "native" (age-encrypted file store — see
	// docs/design/native-store.md).
	Backend string `json:"backend"`
	// Replica marks this instance as a read-only synced copy. Set, Delete,
	// import, migrate, and recipient rotation refuse with errReplicaReadOnly.
	// Get/List/unlock and first-run identity init still work. Also set by
	// the OMASEAL_REPLICA env (either is sufficient).
	Replica bool `json:"replica,omitempty"`
	// Recipients is the user-facing additional-recipient list (name + age
	// X25519 public key). The encryption set Set actually uses is the
	// committed snapshot in the native store dir — see nativerecipients.go.
	// saveConfig writes this after a gated add/remove so the two agree.
	Recipients []namedRecipient `json:"recipients,omitempty"`
}

// namedRecipient is an additional machine that may decrypt every secret.
// The local identity.pub is always a recipient and is not stored here.
type namedRecipient struct {
	Name   string `json:"name"`
	Pubkey string `json:"pubkey"`
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

// saveConfig writes config.json atomically at 0600, creating the directory
// at 0700. Known fields only — this file has no unknown-field preservation.
func saveConfig(cfg omasealConfig) error {
	path := omasealConfigPath()
	if path == "" {
		return errors.New("no config directory")
	}
	if cfg.Backend == "" {
		cfg.Backend = "secretservice"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileMode(path, data, 0600)
}

// replicaFromEnv reports whether OMASEAL_REPLICA is a truthy value.
func replicaFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OMASEAL_REPLICA"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
