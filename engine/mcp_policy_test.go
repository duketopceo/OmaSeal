package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mcpFixture redirects every persistence surface into temp dirs:
// XDG_CONFIG_HOME → agent.json + ai-manifest.txt, XDG_RUNTIME_DIR → the
// session file, XDG_STATE_HOME → logs/usage. Combined with mockKeyring the
// whole agent trust stack is exercisable without touching real state.
func mcpFixture(t *testing.T) (cfgDir string) {
	t.Helper()
	cfgDir = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	mockKeyring(t)
	return cfgDir
}

func writeAgentPolicy(t *testing.T, cfgDir, mode string, keepAlive bool) {
	t.Helper()
	dir := filepath.Join(cfgDir, "omaseal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := AgentPolicy{Mode: mode, SessionMinutes: 15, KeepAlive: keepAlive}
	data, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(dir, "agent.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, cfgDir, body string) {
	t.Helper()
	dir := filepath.Join(cfgDir, "omaseal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai-manifest.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeSessionExpiryAt writes the fixed-width expiry record at the same
// path agentRuntimeDir resolves, for an arbitrary expiry.
func writeSessionExpiryAt(t *testing.T, expiry time.Time) {
	t.Helper()
	if err := os.MkdirAll(agentRuntimeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentSessionPath(), []byte(sessionExpiryText(expiry)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mcpCall(name string, args map[string]any) *mcpResponse {
	raw, _ := json.Marshal(args)
	return callMCPTool(mcpToolCall{Name: name, Arguments: raw})
}

// mcpIsError reports whether the tool response is an isError content frame,
// and returns the text for assertion.
func mcpIsError(t *testing.T, resp *mcpResponse) (bool, string) {
	t.Helper()
	if resp.Error != nil {
		return true, resp.Error.Message
	}
	r, ok := resp.Result.(map[string]any)
	if !ok {
		if cr, ok2 := resp.Result.(mcpToolCallResponse); ok2 {
			if len(cr.Content) > 0 {
				if txt, ok3 := cr.Content[0]["text"].(string); ok3 {
					return false, txt
				}
			}
		}
		return false, ""
	}
	txt := ""
	if c, ok := r["content"].([]map[string]any); ok && len(c) > 0 {
		txt, _ = c[0]["text"].(string)
	}
	isErr, _ := r["isError"].(bool)
	return isErr, txt
}

func TestMCPGetLockedModeRefuses(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "lock", false)
	if err := Set("svc", "acct", "sekrit"); err != nil {
		t.Fatal(err)
	}
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr || !strings.Contains(txt, "agent_locked") {
		t.Fatalf("lock mode should refuse with agent_locked: %q", txt)
	}
}

func TestMCPGetAskModeNoSessionRefuses(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", false)
	_ = Set("svc", "acct", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr || !strings.Contains(txt, "agent_unauthorized") {
		t.Fatalf("ask without session should refuse: %q", txt)
	}
}

func TestMCPGetAskModeValidSessionAllows(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", false)
	writeSessionExpiryAt(t, time.Now().UTC().Add(10*time.Minute))
	_ = Set("svc", "acct", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if isErr {
		t.Fatalf("valid session should allow: %q", txt)
	}
	if txt != "sekrit" {
		t.Fatalf("expected secret payload, got %q", txt)
	}
}

func TestMCPGetExpiredSessionRefuses(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", false)
	writeSessionExpiryAt(t, time.Now().UTC().Add(-time.Minute))
	_ = Set("svc", "acct", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr {
		t.Fatalf("expired session should refuse: %q", txt)
	}
}

func TestMCPGetOpenModeAllows(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	_ = Set("svc", "acct", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if isErr {
		t.Fatalf("open mode should allow: %q", txt)
	}
}

func TestMCPManifestDenyRefusesEvenInOpenMode(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	writeManifest(t, cfg, "DENY bank/*\n")
	_ = Set("bank", "root", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "bank", "account": "root"}))
	if !isErr || !strings.Contains(txt, "manifest_denied") {
		t.Fatalf("DENY should refuse with manifest_denied: %q", txt)
	}
}

func TestMCPManifestAskNeedsSessionEvenInOpenMode(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	writeManifest(t, cfg, "ASK api/*\n")
	_ = Set("api", "key", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "api", "account": "key"}))
	if !isErr || !strings.Contains(txt, "agent_unauthorized") {
		t.Fatalf("manifest ASK should require a session: %q", txt)
	}
	writeSessionExpiryAt(t, time.Now().UTC().Add(10*time.Minute))
	isErr, _ = mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "api", "account": "key"}))
	if isErr {
		t.Fatal("manifest ASK with live session should allow")
	}
}

func TestMCPManifestUnreadableFailsClosed(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	// A directory at the manifest path makes ReadFile fail — the gate must
	// refuse rather than skip checks it cannot evaluate.
	dir := filepath.Join(cfg, "omaseal")
	if err := os.MkdirAll(filepath.Join(dir, "ai-manifest.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = Set("svc", "acct", "sekrit")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr || !strings.Contains(txt, "manifest_error") {
		t.Fatalf("unreadable manifest should refuse with manifest_error: %q", txt)
	}
}

func TestMCPStatusUngatedUnderLock(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "lock", false)
	isErr, txt := mcpIsError(t, mcpCall("omaseal_status", nil))
	if isErr {
		t.Fatalf("status must stay readable under lock: %q", txt)
	}
}

func TestMCPSessionKeepaliveRenews(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", true) // keepalive on
	near := time.Now().UTC().Add(30 * time.Second)
	writeSessionExpiryAt(t, near)
	_ = Set("svc", "acct", "sekrit")
	if isErr, _ := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"})); isErr {
		t.Fatal("live session should allow")
	}
	expiry, ok := readSessionExpiry()
	if !ok {
		t.Fatal("session file missing after keepalive access")
	}
	if !expiry.After(near.Add(10 * time.Minute)) {
		t.Fatalf("keepalive should slide expiry forward, got %v (was %v)", expiry, near)
	}
}

func TestMCPSessionRevokedMidWindow(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", false)
	writeSessionExpiryAt(t, time.Now().UTC().Add(10*time.Minute))
	_ = Set("svc", "acct", "sekrit")
	if err := clearAgentSession(); err != nil {
		t.Fatal(err)
	}
	isErr, txt := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr {
		t.Fatalf("revoked session should refuse: %q", txt)
	}
}

func TestMCPUnknownTool(t *testing.T) {
	mcpFixture(t)
	resp := mcpCall("omaseal_bogus", nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Fatalf("unknown tool should error: %+v", resp)
	}
}

func TestSessionGarbageContentDenied(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "ask", false)
	if err := os.MkdirAll(agentRuntimeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentSessionPath(), []byte("not-a-timestamp"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = Set("svc", "acct", "sekrit")
	isErr, _ := mcpIsError(t, mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"}))
	if !isErr {
		t.Fatal("garbage session file must be treated as no session")
	}
}

func TestRenewCannotResurrectRevokedSession(t *testing.T) {
	// renewSessionExpiry opens O_WRONLY without O_CREATE — a session revoked
	// mid-operation must not come back via a keepalive write.
	mcpFixture(t)
	if err := renewSessionExpiry(time.Now().UTC().Add(time.Hour)); err == nil {
		t.Fatal("renew on a missing session file must fail")
	}
	if _, ok := readSessionExpiry(); ok {
		t.Fatal("renew resurrected a revoked session")
	}
}

func TestMCPManifestDeniesPayloadAmbiguousSlash(t *testing.T) {
	// A payload "a/b/c" is ambiguous (a/b,c) or (a,b/c) — a DENY on either
	// decomposition hides it.
	cfg := mcpFixture(t)
	writeManifest(t, cfg, "DENY a/*\n")
	m, err := LoadManifest()
	if err != nil || m == nil {
		t.Fatalf("manifest load: %v", err)
	}
	if !manifestDeniesPayload(m, "a/b/c") {
		t.Fatal("DENY a/* should hide payload a/b/c")
	}
	if manifestDeniesPayload(m, "x/y/z") {
		t.Fatal("unrelated payload should not be hidden")
	}
}

func TestMCPSetPayloadCannotWriteDeniedName(t *testing.T) {
	cfg := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	writeManifest(t, cfg, "DENY bank/*\n")
	isErr, txt := mcpIsError(t, mcpCall("omaseal_set", map[string]any{"service": "bank", "account": "x", "secret": "v"}))
	if !isErr || !strings.Contains(txt, "manifest_denied") {
		t.Fatalf("set into DENY should refuse: %q", txt)
	}
	if _, err := Get("bank", "x"); err == nil {
		t.Fatal("denied set must not write")
	}
}
