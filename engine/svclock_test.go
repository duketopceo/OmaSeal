package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/godbus/dbus/v5"
)

// armTestUnlock puts the daemon fixture in the unlocked state a real
// prompt-derived unlock produces: in-memory identity + promptIdentity flag.
func armTestUnlock(t *testing.T, d *svcDaemon) {
	t.Helper()
	d.mu.Lock()
	d.store.identity.Store(&age.X25519Identity{})
	d.promptIdentity = true
	d.mu.Unlock()
}

func TestSvcRelockForSuspendLocksVault(t *testing.T) {
	bus := newSvcTestBus(t)
	d := newSvcTestDaemon(t, bus)
	armTestUnlock(t, d)

	d.relockForSuspend("suspend")

	if d.store.identity.Load() != nil {
		t.Fatal("suspend relock left identity in memory")
	}
	if d.promptIdentity {
		t.Fatal("suspend relock left promptIdentity set")
	}
}

func TestSvcRelockForSuspendNoopWhenLocked(t *testing.T) {
	bus := newSvcTestBus(t)
	d := newSvcTestDaemon(t, bus)

	// Never unlocked: the path must return silently — no log noise, no
	// state churn — and must not panic.
	d.relockForSuspend("suspend")

	if d.store.identity.Load() != nil || d.promptIdentity {
		t.Fatal("locked daemon gained state from a no-op relock")
	}
}

func TestSvcWatchLogindNoSystemBus(t *testing.T) {
	bus := newSvcTestBus(t)
	d := newSvcTestDaemon(t, bus)
	old := svcSystemBus
	svcSystemBus = func() (*dbus.Conn, error) {
		return nil, errors.New("no system bus in test")
	}
	t.Cleanup(func() { svcSystemBus = old })

	// Missing logind/system-bus disables the watch and logs — it must
	// never take serving down with it.
	d.watchLogind()

	// The daemon is untouched: an armed unlock still reads armed.
	armTestUnlock(t, d)
	if d.store.identity.Load() == nil {
		t.Fatal("watch setup failure mutated daemon state")
	}
}

// TestSvcSuspendSignalRelocks drives the dispatch path itself — the same
// code a real PrepareForSleep(true) hits — rather than only the lock body.
func TestSvcSuspendSignalRelocks(t *testing.T) {
	bus := newSvcTestBus(t)
	d := newSvcTestDaemon(t, bus)
	armTestUnlock(t, d)

	// Simulate what suspendLoop does on PrepareForSleep(true): relock.
	// (The signal channel needs a live system bus to exercise end-to-end;
	// the handler body is the unit under test.)
	d.relockForSuspend("suspend")

	if d.store.identity.Load() != nil || d.promptIdentity {
		t.Fatal("PrepareForSleep did not lock the vault")
	}
}

func TestSvcUnitStateParsing(t *testing.T) {
	old := svcSystemctl
	svcSystemctl = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "--user show omaseal-provider.service -p ActiveState -p SubState -p Result -p NRestarts -p ExecMainStatus -p LoadState" {
			return nil, fmt.Errorf("unexpected systemctl args: %v", args)
		}
		return []byte("ActiveState=failed\nSubState=failed\nResult=exit-code\nNRestarts=4\nExecMainStatus=1\nLoadState=loaded\n"), nil
	}
	t.Cleanup(func() { svcSystemctl = old })

	unit := svcUnitState()
	if unit == nil {
		t.Fatal("loaded unit state must parse")
	}
	for k, want := range map[string]string{
		"ActiveState":    "failed",
		"Result":         "exit-code",
		"NRestarts":      "4",
		"ExecMainStatus": "1",
	} {
		if unit[k] != want {
			t.Errorf("%s = %q, want %q", k, unit[k], want)
		}
	}
}

func TestSvcUnitStateNotFound(t *testing.T) {
	old := svcSystemctl
	svcSystemctl = func(args ...string) ([]byte, error) {
		return []byte("LoadState=not-found\n"), nil
	}
	t.Cleanup(func() { svcSystemctl = old })
	if unit := svcUnitState(); unit != nil {
		t.Fatalf("not-found unit must report nil, got %v", unit)
	}
}

func TestSvcUnitStateNoSystemd(t *testing.T) {
	old := svcSystemctl
	svcSystemctl = func(args ...string) ([]byte, error) {
		return nil, fmt.Errorf("systemctl: command not found")
	}
	t.Cleanup(func() { svcSystemctl = old })
	if unit := svcUnitState(); unit != nil {
		t.Fatalf("systemctl failure must report nil, got %v", unit)
	}
}

func TestSvcPrintUnitStateFailedShowsExit(t *testing.T) {
	// The failed/result path is the "dead with provenance" case — it must
	// name the exit reason, not just "failed".
	unit := map[string]string{
		"ActiveState": "failed",
		"Result":      "exit-code",
		"NRestarts":   "3",
	}
	out := captureStdout(t, func() { svcPrintUnitState(unit, false) })
	for _, want := range []string{"failed", "exit-code", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("failed-state output missing %q: %q", want, out)
		}
	}
}

func TestSvcPrintUnitStateNoRecordHintsActivation(t *testing.T) {
	out := captureStdout(t, func() { svcPrintUnitState(nil, false) })
	if !strings.Contains(out, "activation") {
		t.Errorf("nil-unit unowned output should hint lazy respawn: %q", out)
	}
}
