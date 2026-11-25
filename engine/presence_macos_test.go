//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubMacHelper points the helper candidates at dir and fakes the runner.
func stubMacHelper(t *testing.T, dir string, run func(ctx context.Context, path, reason string) (int, error)) {
	t.Helper()
	oldExe, oldHome, oldRun := presenceExecutableFunc, presenceHomeDirFunc, presenceRunHelperFunc
	presenceExecutableFunc = func() (string, error) { return filepath.Join(dir, "omaseal"), nil }
	presenceHomeDirFunc = func() (string, error) { return filepath.Join(dir, "nohome"), nil }
	presenceRunHelperFunc = run
	t.Cleanup(func() {
		presenceExecutableFunc, presenceHomeDirFunc, presenceRunHelperFunc = oldExe, oldHome, oldRun
	})
}

func writeHelper(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "omaseal-presence")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMacHelperExitCodeMapping(t *testing.T) {
	dir := t.TempDir()
	writeHelper(t, dir, 0o755)
	cases := []struct {
		code    int
		runErr  error
		wantErr error // nil = confirmed
	}{
		{0, nil, nil},
		{1, nil, errPresenceDenied},
		{2, nil, errMacPresenceUnavailable},
		// Unknown codes (crash, kill) fail closed.
		{77, nil, errPresenceDenied},
		{-1, nil, errPresenceDenied},
		// An unanswered prompt is a denial, never a fall-through to allow_ungated.
		{0, fmt.Errorf("%w: %w", errPresenceHelperLapsed, context.DeadlineExceeded), errPresenceDenied},
		{0, fmt.Errorf("%w: %w", errPresenceHelperLapsed, context.Canceled), errPresenceDenied},
		// The helper could not start at all: no mechanism.
		{0, errors.New("exec format error"), errMacPresenceUnavailable},
	}
	for _, c := range cases {
		stubMacHelper(t, dir, func(context.Context, string, string) (int, error) { return c.code, c.runErr })
		err := macosHelperPresence(context.Background(), "agent unlock")
		if !errors.Is(err, c.wantErr) && !(err == nil && c.wantErr == nil) {
			t.Errorf("code=%d runErr=%v: got %v, want %v", c.code, c.runErr, err, c.wantErr)
		}
	}
}

// The real runner reports a cancelled/expired prompt as lapsed, which the
// chain step turns into a denial.
func TestRealHelperRunnerLapsedIsDenied(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "slow")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 5\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := runPresenceHelper(ctx, p, "x"); !errors.Is(err, errPresenceHelperLapsed) {
		t.Fatalf("runner err = %v, want errPresenceHelperLapsed", err)
	}
	writeHelper(t, dir, 0o755)
	stubMacHelper(t, dir, func(context.Context, string, string) (int, error) {
		return runPresenceHelper(ctx, p, "x")
	})
	if err := macosHelperPresence(context.Background(), "agent unlock"); !errors.Is(err, errPresenceDenied) {
		t.Fatalf("lapsed prompt = %v, want errPresenceDenied", err)
	}
}

func TestMacHelperUnsafePermissionsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o775, 0o757, 0o777} {
		dir := t.TempDir()
		writeHelper(t, dir, mode)
		ran := false
		stubMacHelper(t, dir, func(context.Context, string, string) (int, error) { ran = true; return 0, nil })
		err := macosHelperPresence(context.Background(), "x")
		if !errors.Is(err, errMacPresenceUnavailable) || ran {
			t.Errorf("mode %v: err=%v ran=%v, want unavailable and not run", mode, err, ran)
		}
		if presenceHelperUsable() {
			t.Errorf("mode %v: helper reported usable", mode)
		}
	}
}

func TestMacHelperWrongOwnerRefused(t *testing.T) {
	dir := t.TempDir()
	writeHelper(t, dir, 0o755)
	stubMacHelper(t, dir, func(context.Context, string, string) (int, error) { return 0, nil })
	oldUID := presenceUIDFunc
	presenceUIDFunc = func() int { return os.Getuid() + 4242 } // file owner now looks foreign
	defer func() { presenceUIDFunc = oldUID }()
	if os.Getuid() == 0 {
		t.Skip("root-owned files are always accepted")
	}
	if err := macosHelperPresence(context.Background(), "x"); !errors.Is(err, errMacPresenceUnavailable) {
		t.Fatalf("foreign-owned helper must be refused, got %v", err)
	}
}

func TestMacHelperNonRegularRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkPresenceHelper(dir); err == nil {
		t.Fatal("directory must be refused")
	}
}

func TestMacHelperMissingFallsThrough(t *testing.T) {
	dir := t.TempDir() // no helper written
	stubMacHelper(t, dir, func(context.Context, string, string) (int, error) {
		t.Fatal("must not run a missing helper")
		return 0, nil
	})
	if err := macosHelperPresence(context.Background(), "x"); !errors.Is(err, errMacPresenceUnavailable) {
		t.Fatalf("missing helper = %v, want unavailable", err)
	}
	if presenceHelperUsable() {
		t.Fatal("missing helper reported usable")
	}
}

func TestMacHelperEnvLimited(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "hunter2")
	t.Setenv("LANG", "C")
	for _, kv := range presenceHelperEnv() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "PATH", "HOME", "USER", "LANG":
		default:
			t.Errorf("unexpected env var passed to helper: %s", k)
		}
	}
}

func TestRealHelperRunnerExitCodes(t *testing.T) {
	dir := t.TempDir()
	for code, want := range map[string]int{"0": 0, "1": 1, "2": 2} {
		p := filepath.Join(dir, "h"+code)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit "+code+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := runPresenceHelper(context.Background(), p, "r")
		if err != nil || got != want {
			t.Errorf("exit %s: got (%d, %v)", code, got, err)
		}
	}
}

func TestChainMacConfirmedSkipsOthers(t *testing.T) {
	stubPresence(t, true, errors.New("fprintd must not run"), errors.New("gui must not run"))
	stubMacPresence(t, nil)
	if err := runPresenceChain(context.Background(), "agent unlock"); err != nil {
		t.Fatalf("mac confirm should allow: %v", err)
	}
}

func TestChainMacDenialStopsChain(t *testing.T) {
	ran := false
	stubPresence(t, true, nil, nil)
	oldV := fprintdVerifyFunc
	fprintdVerifyFunc = func(context.Context, string) error { ran = true; return nil }
	defer func() { fprintdVerifyFunc = oldV }()
	stubMacPresence(t, errPresenceDenied)
	err := requireUserPresence(context.Background(), "agent unlock", AgentPolicy{AllowUngated: true})
	if !errors.Is(err, errPresenceDenied) {
		t.Fatalf("mac denial must deny even with allow_ungated, got %v", err)
	}
	if ran {
		t.Fatal("a mac denial must not fall through to fprintd")
	}
}

func TestChainMacUnavailableFallsThrough(t *testing.T) {
	stubPresence(t, false, nil, nil) // GUI confirm allows
	stubMacPresence(t, errMacPresenceUnavailable)
	if err := runPresenceChain(context.Background(), "x"); err != nil {
		t.Fatalf("unavailable mac step must fall through to gui: %v", err)
	}
	stubPresence(t, false, nil, errNoGUIPrompter)
	if err := runPresenceChain(context.Background(), "x"); !errors.Is(err, errNoPresenceMechanism) {
		t.Fatalf("everything unavailable = %v, want errNoPresenceMechanism", err)
	}
	// ungated semantics unchanged
	if err := requireUserPresence(context.Background(), "x", AgentPolicy{AllowUngated: true}); err != nil {
		t.Fatalf("allow_ungated should still permit: %v", err)
	}
	if err := requirePresenceStrict(context.Background(), "x", "hint"); !errors.Is(err, errNoPresenceMechanism) {
		t.Fatalf("strict must refuse: %v", err)
	}
}

func TestPresenceMechanismMacLabel(t *testing.T) {
	old := macosHelperUsableFunc
	macosHelperUsableFunc = func() bool { return true }
	defer func() { macosHelperUsableFunc = old }()
	if got := presenceMechanism(context.Background(), AgentPolicy{AllowUngated: true}, false); got != "macos-local-auth" {
		t.Fatalf("mechanism = %q, want macos-local-auth", got)
	}
}

func stubMacPresence(t *testing.T, err error) {
	t.Helper()
	old := macosPresenceFunc
	macosPresenceFunc = func(context.Context, string) error { return err }
	t.Cleanup(func() { macosPresenceFunc = old })
}
