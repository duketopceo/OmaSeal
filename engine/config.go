package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// omasealConfig is ~/.config/omaseal/config.json — store/backend selection
// and other local (non-agent-policy) settings. agent.json stays scoped to
// agent policy; this file owns everything else.
type omasealConfig struct {
	// Backend selects the secret store: "secretservice" (Linux default,
	// gnome-keyring via D-Bus), "native" (age-encrypted file store —
	// see docs/design/native-store.md; the default on macOS), or
	// "keychain" (opt-in, macOS and Windows: native store.json for items,
	// login Keychain for the passphrase-wrapped age identity). Never
	// the default. Ignored in replica mode — see selectStore.
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
	cfg := omasealConfig{Backend: defaultBackend()}
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
		cfg.Backend = defaultBackend()
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
		cfg.Backend = defaultBackend()
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

// replicaEnabled is true when either the config flag or the env is set.
func replicaEnabled(cfg omasealConfig) bool {
	return cfg.Replica || replicaFromEnv()
}

// defaultBackend is the backend used when config omits or leaves
// "backend" empty. Linux stays secretservice (existing installs,
// gnome-keyring consumers). macOS has no Secret Service bus, so the
// default is the native file store — never Keychain, which is opt-in.
func defaultBackend() string {
	if runtime.GOOS == "darwin" {
		return "native"
	}
	return "secretservice"
}

// selectStore installs the configured backend as currentStore. Called once
// at startup before command dispatch — every op lane (CLI, IPC, MCP,
// providers) routes through the package dispatchers after this.
//
// Replica mode always gets the native file store, even if config says
// keychain (or secretservice): the replica syncs store.json
// and keeps its identity in identity.age. A Keychain config is ignored,
// not a startup error, so a copied config cannot strand the replica.
func selectStore() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if replicaEnabled(cfg) {
		currentStore = newNativeStore()
		return nil
	}
	backend := cfg.Backend
	if backend == "" {
		backend = defaultBackend()
	}
	switch backend {
	case "secretservice":
		currentStore = ssStore{}
	case "native":
		currentStore = newNativeStore()
	case "keychain":
		if !keychainBackendSupported() {
			return fmt.Errorf("backend %q is not available on this platform; use backend %q or omit the key", backend, "native")
		}
		currentStore = newNativeStore()
	default:
		return fmt.Errorf("unknown backend %q in %s (want secretservice, native, or keychain)", backend, omasealConfigPath())
	}
	return nil
}
