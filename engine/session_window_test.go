package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One-window / one-lock tests: the native session and the agent session share
// one expiry, keep-alive slides both under a hard cap, and one lock wipes
// everything. See docs/design/native-store.md "Unlock window".

type winClock struct{ t time.Time }

func (c *winClock) now() time.Time { return c.t }

// windowEnv builds a locked native store whose runtime dir IS the agent
// runtime dir (production layout), with one injectable clock driving the
// store and the agent code, and installs it as currentStore.
func windowEnv(t *testing.T, keepAlive bool) (*winClock, *nativeStore) {
	t.Helper()
	setupAgentEnv(t)
	askPolicy(t, keepAlive)
	oldTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = oldTTY })

	clk := &winClock{t: time.Now().UTC().Truncate(time.Second)}
	oldNow := agentNow
	agentNow = clk.now
	t.Cleanup(func() { agentNow = oldNow })

	s := newNativeStoreAt(t.TempDir(), agentRuntimeDir())
	s.now = clk.now
	initNative(t, s, "win-pass")
	if err := s.clearNativeSession(); err != nil {
		t.Fatal(err)
	}
	s.prompt = func(string) (string, error) { return "", errors.New("no prompt") }

	oldStore := currentStore
	currentStore = s
	t.Cleanup(func() { currentStore = oldStore })

	stubPresence(t, true, nil, nil)
	oldStdin := readPassphraseStdin
	readPassphraseStdin = func() (string, error) { return "win-pass", nil }
	t.Cleanup(func() { readPassphraseStdin = oldStdin })
	return clk, s
}

func nativeSessionExpiry(t *testing.T, dir string) int64 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, nativeSessJSONFile))
	if err != nil {
		t.Fatalf("native session missing: %v", err)
	}
	var sess nativeSession
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatal(err)
	}
	return sess.Expires
}

func assertOneWindow(t *testing.T, s *nativeStore, want time.Time) {
	t.Helper()
	nat := nativeSessionExpiry(t, s.runtimeDir)
	ag, ok := readSessionExpiry()
	if !ok {
		t.Fatal("agent session missing")
	}
	if nat != ag.Unix() {
		t.Fatalf("native expiry %d != agent expiry %d", nat, ag.Unix())
	}
	if !want.IsZero() && nat != want.Unix() {
		t.Fatalf("expiry %d, want %d", nat, want.Unix())
	}
}

func TestUnlockPathsShareOneExpiry(t *testing.T) {
	t.Run("prompt in ensureIdentity", func(t *testing.T) {
		clk, s := windowEnv(t, false)
		s.prompt = func(string) (string, error) { return "win-pass", nil }
		if _, err := s.Get("svc", "acct"); err != nil {
			t.Fatal(err)
		}
		assertOneWindow(t, s, clk.t.Add(15*time.Minute))
	})
	t.Run("agent unlock passphrase-stdin", func(t *testing.T) {
		clk, s := windowEnv(t, false)
		if err := UnlockAgent(true); err != nil {
			t.Fatal(err)
		}
		assertOneWindow(t, s, clk.t.Add(15*time.Minute))
	})
	t.Run("agent unlock presence plus prompt", func(t *testing.T) {
		clk, s := windowEnv(t, false)
		oldTTY, oldGUI := stdinIsTTY, nativePromptGUI
		stdinIsTTY = func() bool { return false }
		nativePromptGUI = func(context.Context, string) (string, error) { return "win-pass", nil }
		t.Cleanup(func() { stdinIsTTY, nativePromptGUI = oldTTY, oldGUI })
		t.Setenv("WAYLAND_DISPLAY", "wayland-test")
		if err := UnlockAgent(false); err != nil {
			t.Fatal(err)
		}
		assertOneWindow(t, s, clk.t.Add(15*time.Minute))
	})
	t.Run("policy minutes honoured", func(t *testing.T) {
		clk, s := windowEnv(t, false)
		if err := saveAgentPolicy(AgentPolicy{Mode: "ask", SessionMinutes: 45}); err != nil {
			t.Fatal(err)
		}
		if err := UnlockAgent(true); err != nil {
			t.Fatal(err)
		}
		assertOneWindow(t, s, clk.t.Add(45*time.Minute))
	})
	t.Run("session minutes above the cap are clamped", func(t *testing.T) {
		clk, s := windowEnv(t, false)
		if err := saveAgentPolicy(AgentPolicy{Mode: "ask", SessionMinutes: 60 * 30, MaxUnlockHours: 2}); err != nil {
			t.Fatal(err)
		}
		if err := UnlockAgent(true); err != nil {
			t.Fatal(err)
		}
		assertOneWindow(t, s, clk.t.Add(2*time.Hour))
	})
}

// Lock mode refuses agents, so a store unlock must not inherit the long
// agent window: the native session stays at the 15-minute TTL.
func TestLockModeUnlockKeepsShortNativeWindow(t *testing.T) {
	clk, s := windowEnv(t, false)
	if err := saveAgentPolicy(AgentPolicy{Mode: "lock", SessionMinutes: 720}); err != nil {
		t.Fatal(err)
	}
	s.prompt = func(string) (string, error) { return "win-pass", nil }
	if _, err := s.Get("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if got, want := nativeSessionExpiry(t, s.runtimeDir), clk.t.Add(defaultNativeTTL).Unix(); got != want {
		t.Fatalf("native expiry = %d, want %d (now+15m)", got, want)
	}
	if _, ok := readSessionExpiry(); ok {
		t.Fatal("lock mode must not mint an agent session")
	}
}

func TestKeepAliveRenewsBothWindows(t *testing.T) {
	clk, s := windowEnv(t, true)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(10 * time.Minute)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	assertOneWindow(t, s, clk.t.Add(15*time.Minute))
	// the renewed native session still reads (same key, rewritten in place)
	if _, err := s.Get("svc", "acct"); err != nil {
		t.Fatalf("read after renewal: %v", err)
	}
	fi, err := os.Stat(filepath.Join(s.runtimeDir, nativeSessJSONFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("native session mode after renewal = %v (%v)", fi.Mode().Perm(), err)
	}
}

func TestKeepAliveNeverPassesHardCap(t *testing.T) {
	clk, s := windowEnv(t, true)
	if err := saveAgentPolicy(AgentPolicy{Mode: "ask", SessionMinutes: 15, KeepAlive: true, MaxUnlockHours: 1}); err != nil {
		t.Fatal(err)
	}
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	start := clk.t
	for i := 0; i < 5; i++ { // 10-minute steps up to minute 50
		clk.t = clk.t.Add(10 * time.Minute)
		if err := CheckAgentOperation("get"); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	clk.t = start.Add(55 * time.Minute)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	assertOneWindow(t, s, start.Add(time.Hour)) // clamped, not +15m
	clk.t = start.Add(59 * time.Minute)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	assertOneWindow(t, s, start.Add(time.Hour)) // still the cap
	clk.t = start.Add(time.Hour)
	if err := CheckAgentOperation("get"); err == nil {
		t.Fatal("session must end at the hard cap")
	}
}

func TestKeepAliveDefaultCapIs16h(t *testing.T) {
	clk, s := windowEnv(t, true)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	start := clk.t
	for clk.t.Before(start.Add(17 * time.Hour)) {
		clk.t = clk.t.Add(10 * time.Minute)
		if clk.t.Before(start.Add(16 * time.Hour)) {
			if err := CheckAgentOperation("get"); err != nil {
				t.Fatalf("at %v: %v", clk.t.Sub(start), err)
			}
			if got := nativeSessionExpiry(t, s.runtimeDir); got > start.Add(16*time.Hour).Unix() {
				t.Fatalf("native expiry passed the 16h cap")
			}
		}
	}
	if err := CheckAgentOperation("get"); err == nil {
		t.Fatal("session survived past the 16h cap")
	}
}

func TestKeepAliveDoesNotCreateNativeSession(t *testing.T) {
	clk, s := windowEnv(t, true)
	// Agent session only (e.g. mode ask, store never unlocked).
	if err := writeSessionExpiry(clk.t.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(30 * time.Second)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	if exp, _ := readSessionExpiry(); exp.Unix() != clk.t.Add(15*time.Minute).Unix() {
		t.Fatalf("agent session not renewed: %v", exp)
	}
	for _, f := range []string{nativeSessJSONFile, nativeSessAgeFile} {
		if _, err := os.Stat(filepath.Join(s.runtimeDir, f)); err == nil {
			t.Fatalf("keep-alive created %s", f)
		}
	}
}

func TestKeepAliveDoesNotReviveExpiredNativeSession(t *testing.T) {
	clk, s := windowEnv(t, true)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	nat := nativeSessionExpiry(t, s.runtimeDir)
	// Agent session outlives the native one (hand-edited): renewal must not
	// resurrect the expired native session.
	if err := renewSessionExpiry(clk.t.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	clk.t = time.Unix(nat, 0).Add(time.Minute)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	if got := nativeSessionExpiry(t, s.runtimeDir); got != nat {
		t.Fatalf("expired native session was extended: %d -> %d", nat, got)
	}
}

func TestKeepAliveWithoutStartRecordDoesNotRenew(t *testing.T) {
	clk, s := windowEnv(t, true)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sessionStartPath(agentRuntimeDir())); err != nil {
		t.Fatal(err)
	}
	before, _ := readSessionExpiry()
	clk.t = clk.t.Add(10 * time.Minute)
	if err := CheckAgentOperation("get"); err != nil {
		t.Fatal(err)
	}
	after, _ := readSessionExpiry()
	if !after.Equal(before) || nativeSessionExpiry(t, s.runtimeDir) != before.Unix() {
		t.Fatalf("renewed a session with no start record: %v -> %v", before, after)
	}
}

func TestLockAgentWipesNativeSessionAndStart(t *testing.T) {
	_, s := windowEnv(t, true)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if err := LockAgent(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(s.runtimeDir, nativeSessJSONFile),
		filepath.Join(s.runtimeDir, nativeSessAgeFile),
		sessionStartPath(s.runtimeDir),
		agentSessionPath(),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s survived agent lock", filepath.Base(p))
		}
	}
	_, err := s.Get("svc", "acct")
	if err == nil || codeFromError(err) != "locked" {
		t.Fatalf("read after lock = %v, want locked", err)
	}
	// Lock succeeds when everything is already gone.
	if err := LockAgent(); err != nil {
		t.Fatalf("second lock: %v", err)
	}
}

// A long-lived instance (MCP server, daemon) holds the identity in memory;
// a lock done by another process must still take effect on its next read.
func TestLongLivedStoreDropsIdentityAfterLock(t *testing.T) {
	_, s := windowEnv(t, false)
	if err := UnlockAgent(true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if s.identity.Load() == nil {
		t.Fatal("identity should be held in memory")
	}
	// A different process locks: it only sees the files.
	other := newNativeStoreAt(s.dir, s.runtimeDir)
	if err := other.wipeSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("svc", "acct"); err == nil || codeFromError(err) != "locked" {
		t.Fatalf("long-lived store still reads after lock: %v", err)
	}
	if s.identity.Load() != nil {
		t.Fatal("identity not dropped from memory")
	}
}

// A prompt-derived identity (provider daemon) has no session files by design;
// the per-access session check must leave it alone.
func TestPromptDerivedIdentityIgnoresSessionFiles(t *testing.T) {
	_, s := windowEnv(t, false)
	id, err := s.unwrapIdentityPass("win-pass")
	if err != nil {
		t.Fatal(err)
	}
	s.identity.Store(id)
	if _, err := s.Get("svc", "acct"); err != nil {
		t.Fatalf("prompt-derived identity dropped: %v", err)
	}
}

func TestSvcRelockWipesNativeAndAgentSessions(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "idle daemon"
		if held {
			name = "daemon adopted session"
		}
		t.Run(name, func(t *testing.T) {
			_, s := windowEnv(t, true)
			bus := newSvcTestBus(t)
			// newSvcTestDaemon resets XDG_RUNTIME_DIR; re-point it so the
			// agent session path matches the store's runtime dir.
			d := newSvcTestDaemon(t, bus)
			t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(s.runtimeDir))
			d.store = s
			if err := UnlockAgent(true); err != nil {
				t.Fatal(err)
			}
			if held {
				if err := d.ensureStoreIdentity(); err != nil {
					t.Fatal(err)
				}
			} else {
				s.identity.Store(nil)
				s.sessionIdent.Store(nil)
			}
			d.relockForSuspend("suspend")
			for _, p := range []string{
				filepath.Join(s.runtimeDir, nativeSessJSONFile),
				filepath.Join(s.runtimeDir, nativeSessAgeFile),
				sessionStartPath(s.runtimeDir),
				agentSessionPath(),
			} {
				if _, err := os.Stat(p); err == nil {
					t.Fatalf("%s survived relock", filepath.Base(p))
				}
			}
			if s.identity.Load() != nil {
				t.Fatal("identity survived relock")
			}
		})
	}
}

func TestMaxUnlockHoursValidation(t *testing.T) {
	setupAgentEnv(t)
	for _, tc := range []struct {
		hours int
		want  time.Duration
	}{
		{0, 16 * time.Hour}, {-3, 16 * time.Hour}, {25, 16 * time.Hour}, {1000, 16 * time.Hour},
		{1, time.Hour}, {8, 8 * time.Hour}, {24, 24 * time.Hour},
	} {
		raw := `{"mode":"ask","session_minutes":15,"max_unlock_hours":` + itoa(tc.hours) + `}`
		if err := os.WriteFile(agentPolicyPath(), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := loadAgentPolicy()
		if err != nil {
			t.Fatal(err)
		}
		if got := p.maxUnlockWindow(); got != tc.want {
			t.Errorf("max_unlock_hours=%d: window %v, want %v", tc.hours, got, tc.want)
		}
	}
	// Absent field: default.
	if err := os.WriteFile(agentPolicyPath(), []byte(`{"mode":"ask"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, _ := loadAgentPolicy(); p.maxUnlockWindow() != 16*time.Hour {
		t.Fatalf("absent field window = %v", p.maxUnlockWindow())
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// --- audit caller ------------------------------------------------------

func readLogText(t *testing.T) string {
	t.Helper()
	lines, err := ReadLog(200)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func TestReadLogsCarryCaller(t *testing.T) {
	cfg, ms := mcpFixture(t)
	writeAgentPolicy(t, cfg, "open", false)
	_ = ms.set("svc", "acct", "sekrit")
	SetLogOutput() // route log.Printf into the chained log file, as main does
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// mcp
	mcpCall("omaseal_get", map[string]any{"service": "svc", "account": "acct"})
	// ipc
	resp, code := ipcDispatch("get", `{"service":"svc","account":"acct"}`, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("ipc get: %+v", resp)
	}
	// cli
	oldStore, oldArgs := currentStore, os.Args
	currentStore = &cliFakeStore{items: map[string]string{"svc/acct": "sekrit"}}
	os.Args = []string{"omaseal", "get", "svc", "acct"}
	t.Cleanup(func() { currentStore, os.Args = oldStore, oldArgs })
	captureStdout(t, handleGet)

	log := readLogText(t)
	for _, want := range []string{
		"access svc/acct caller=mcp",
		"access svc/acct caller=ipc",
		"get svc/acct caller=cli",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n%s", want, log)
		}
	}
	// Stats must still group by service/account, not by the caller suffix.
	m := accessLogRe.FindStringSubmatch("2026/11/25 10:00:00 access svc/acct caller=mcp chain=" + strings.Repeat("a", 64))
	if m == nil || m[2] != "svc/acct" {
		t.Fatalf("analytics regex captured %q", m)
	}
}
