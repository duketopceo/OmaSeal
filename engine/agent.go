package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	agentConfigDirName = "omaseal"
	agentPolicyFile    = "agent.json"
	agentSessionFile   = "session"
)

// AgentPolicy is the user-controlled gate for agent/MCP access.
// mode:
//   - "open"  : agents can read and write secrets freely.
//   - "ask"   : agents must run `omaseal agent unlock` (biometric if available)
//               before any MCP tool that touches secrets.
//   - "lock"  : agents cannot access secrets at all.
type AgentPolicy struct {
	Mode           string `json:"mode"`
	SessionMinutes int    `json:"session_minutes"`
}

func defaultAgentPolicy() AgentPolicy {
	return AgentPolicy{Mode: "open", SessionMinutes: 15}
}

func agentConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, agentConfigDirName)
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func agentPolicyPath() string {
	return filepath.Join(agentConfigDir(), agentPolicyFile)
}

func loadAgentPolicy() (AgentPolicy, error) {
	path := agentPolicyPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultAgentPolicy(), nil
		}
		return defaultAgentPolicy(), err
	}
	var p AgentPolicy
	if err := json.Unmarshal(data, &p); err != nil {
		return defaultAgentPolicy(), err
	}
	if !isValidMode(p.Mode) {
		p.Mode = defaultAgentPolicy().Mode
	}
	if p.SessionMinutes <= 0 {
		p.SessionMinutes = defaultAgentPolicy().SessionMinutes
	}
	return p, nil
}

func saveAgentPolicy(p AgentPolicy) error {
	path := agentPolicyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func isValidMode(mode string) bool {
	switch mode {
	case "open", "ask", "lock":
		return true
	}
	return false
}

// agentRuntimeDir returns a per-user, non-persistent directory for the session
// token. Prefer XDG_RUNTIME_DIR (tmpfs) so the session is cleared on logout.
func agentRuntimeDir() string {
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		dir := filepath.Join(base, agentConfigDirName)
		_ = os.MkdirAll(dir, 0700)
		return dir
	}
	dir := fmt.Sprintf("/tmp/%s-%d", agentConfigDirName, os.Getuid())
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func agentSessionPath() string {
	return filepath.Join(agentRuntimeDir(), agentSessionFile)
}

func writeSessionExpiry(expiry time.Time) error {
	path := agentSessionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", expiry.Unix())), 0600)
}

func readSessionExpiry() (time.Time, bool) {
	data, err := os.ReadFile(agentSessionPath())
	if err != nil {
		return time.Time{}, false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(ts, 0).UTC(), true
}

func clearAgentSession() error {
	return os.Remove(agentSessionPath())
}

// AgentMode returns the current mode for use in status/CLI output.
func AgentMode() string {
	p, _ := loadAgentPolicy()
	return p.Mode
}

// CheckAgentOperation returns an error if the current agent policy forbids op.
// op is one of: list, get, resolve, set, delete.
func CheckAgentOperation(op string) error {
	p, err := loadAgentPolicy()
	if err != nil {
		return newError("agent_policy_read", "omaseal doctor", fmt.Errorf("cannot load agent policy: %w", err))
	}

	switch p.Mode {
	case "open":
		return nil
	case "lock":
		return newError("agent_locked", "omaseal agent mode open", fmt.Errorf("agent access is locked"))
	case "ask":
		expiry, ok := readSessionExpiry()
		if ok && time.Now().UTC().Before(expiry) {
			return nil
		}
		return newError("agent_unauthorized", "omaseal agent unlock", fmt.Errorf("agent must unlock before %s", op))
	}
	return nil
}

// SetAgentMode changes the persistent agent policy.
func SetAgentMode(mode string, sessionMinutes int) error {
	if !isValidMode(mode) {
		return fmt.Errorf("invalid mode %q; use open, ask, or lock", mode)
	}
	if sessionMinutes <= 0 {
		sessionMinutes = defaultAgentPolicy().SessionMinutes
	}
	return saveAgentPolicy(AgentPolicy{Mode: mode, SessionMinutes: sessionMinutes})
}

// UnlockAgent creates a time-bounded session after a best-effort biometric gate.
func UnlockAgent() error {
	p, err := loadAgentPolicy()
	if err != nil {
		return err
	}
	if p.Mode == "open" {
		return fmt.Errorf("agent mode is already open; locking is not needed")
	}

	// FprintdVerify returns nil when no biometric hardware is present, but
	// returns an error on a failed scan. That mirrors the macOS best-effort
	// posture: protect where you can, do not hard-fail on older hardware.
	if err := FprintdVerify(context.Background(), "agent unlock"); err != nil {
		return fmt.Errorf("biometric gate failed: %w", err)
	}

	expiry := time.Now().UTC().Add(time.Duration(p.SessionMinutes) * time.Minute)
	if err := writeSessionExpiry(expiry); err != nil {
		return err
	}
	fmt.Printf("Agent access unlocked until %s (%d minutes).\n", expiry.Format(time.RFC3339), p.SessionMinutes)
	return nil
}

// LockAgent revokes any active agent session and, if mode is not locked, sets
// the persistent policy to ask so the next access requires re-authorization.
func LockAgent() error {
	_ = clearAgentSession()
	p, err := loadAgentPolicy()
	if err != nil {
		return err
	}
	if p.Mode == "open" {
		if err := SetAgentMode("ask", p.SessionMinutes); err != nil {
			return err
		}
	}
	fmt.Println("Agent session cleared. Agent mode is now " + AgentMode() + ".")
	return nil
}

// PrintAgentStatus writes the current policy and session state to stderr.
func PrintAgentStatus() {
	p, err := loadAgentPolicy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot load agent policy: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "agent mode:       %s\n", p.Mode)
	fmt.Fprintf(os.Stderr, "session minutes:  %d\n", p.SessionMinutes)
	expiry, ok := readSessionExpiry()
	if ok && time.Now().UTC().Before(expiry) {
		fmt.Fprintf(os.Stderr, "session:          active until %s\n", expiry.Format(time.RFC3339))
	} else {
		fmt.Fprintln(os.Stderr, "session:          none")
	}
}

func handleAgent() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: omaseal agent mode <open|ask|lock> [session-minutes]")
		fmt.Fprintln(os.Stderr, "       omaseal agent unlock")
		fmt.Fprintln(os.Stderr, "       omaseal agent lock")
		fmt.Fprintln(os.Stderr, "       omaseal agent status")
		os.Exit(1)
	}
	sub := os.Args[2]
	switch sub {
	case "mode":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "error: missing mode")
			os.Exit(1)
		}
		mode := os.Args[3]
		mins := 0
		if len(os.Args) >= 5 {
			m, err := strconv.Atoi(os.Args[4])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: invalid session minutes: %v\n", err)
				os.Exit(1)
			}
			mins = m
		}
		if err := SetAgentMode(mode, mins); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent mode set to %s.\n", mode)
	case "unlock":
		if err := UnlockAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "lock":
		if err := LockAgent(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "status":
		PrintAgentStatus()
	default:
		fmt.Fprintf(os.Stderr, "error: unknown agent subcommand %q\n", sub)
		os.Exit(1)
	}
}
