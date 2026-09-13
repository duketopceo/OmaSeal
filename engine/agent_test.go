package main

import (
	"strings"
	"testing"
	"time"
)

// setupAgentEnv points the policy (XDG_CONFIG_HOME) and session
// (XDG_RUNTIME_DIR) stores at per-test temp dirs.
func setupAgentEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
}

func askPolicy(t *testing.T, keepAlive bool) {
	t.Helper()
	if err := saveAgentPolicy(AgentPolicy{Mode: "ask", SessionMinutes: 15, KeepAlive: keepAlive}); err != nil {
		t.Fatalf("save policy: %v", err)
	}
}

func TestKeepAliveSlidesExpiry(t *testing.T) {
	setupAgentEnv(t)
	askPolicy(t, true)

	soon := time.Now().UTC().Add(time.Minute)
	if err := writeSessionExpiry(soon); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := CheckAgentOperation("list"); err != nil {
		t.Fatalf("check op: %v", err)
	}
	got, ok := readSessionExpiry()
	if !ok {
		t.Fatal("session file missing after op")
	}
	if got.Before(time.Now().UTC().Add(14 * time.Minute)) {
		t.Fatalf("keep-alive did not slide expiry: still %s", got)
	}
}

func TestFixedExpiryWithoutKeepAlive(t *testing.T) {
	setupAgentEnv(t)
	askPolicy(t, false)

	soon := time.Now().UTC().Add(time.Minute)
	if err := writeSessionExpiry(soon); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := CheckAgentOperation("list"); err != nil {
		t.Fatalf("check op: %v", err)
	}
	got, ok := readSessionExpiry()
	if !ok {
		t.Fatal("session file missing after op")
	}
	if !got.Equal(soon.Truncate(time.Second)) {
		t.Fatalf("expiry moved without keep-alive: %s -> %s", soon, got)
	}
}

func TestKeepAliveDoesNotReviveExpiredSession(t *testing.T) {
	setupAgentEnv(t)
	askPolicy(t, true)

	past := time.Now().UTC().Add(-time.Minute)
	if err := writeSessionExpiry(past); err != nil {
		t.Fatalf("write session: %v", err)
	}
	err := CheckAgentOperation("list")
	if err == nil || !strings.Contains(err.Error(), "unlock") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestSetAgentModePreservesPolicy(t *testing.T) {
	setupAgentEnv(t)
	if err := saveAgentPolicy(AgentPolicy{
		Mode: "ask", SessionMinutes: 15, KeepAlive: true,
		PrimaryAgent: "claude", Agents: []string{"claude", "devin"},
	}); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	if err := SetAgentMode("lock", 0); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	p, err := loadAgentPolicy()
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if p.Mode != "lock" || !p.KeepAlive || p.PrimaryAgent != "claude" || len(p.Agents) != 2 {
		t.Fatalf("mode change clobbered policy: %+v", p)
	}
}
