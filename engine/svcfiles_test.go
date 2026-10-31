package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// stubProviderSideEffects keeps enable/disable off the real systemctl and
// ambient bus — the tests assert file artifacts, not the live session.
func stubProviderSideEffects(t *testing.T) {
	t.Helper()
	oldSys, oldDial := svcSystemctl, svcConnectAmbient
	svcSystemctl = func(args ...string) ([]byte, error) { return nil, nil }
	svcConnectAmbient = func() (*dbus.Conn, error) {
		return nil, fmt.Errorf("no ambient bus in test")
	}
	t.Cleanup(func() { svcSystemctl, svcConnectAmbient = oldSys, oldDial })
}

func TestProviderEnableWritesActivationAndUnit(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProviderSideEffects(t)

	if err := providerEnable(nil); err != nil {
		t.Fatalf("enable: %v", err)
	}
	act, err := os.ReadFile(svcActivationPath())
	if err != nil {
		t.Fatalf("activation file: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	for _, want := range []string{"Name=org.freedesktop.secrets", "SystemdService=omaseal-provider.service", "Exec=" + exe + " provider serve"} {
		if !strings.Contains(string(act), want) {
			t.Errorf("activation missing %q", want)
		}
	}
	unit, err := os.ReadFile(svcUnitPath())
	if err != nil {
		t.Fatalf("unit file: %v", err)
	}
	for _, want := range []string{"Type=dbus", "BusName=org.freedesktop.secrets", "LimitCORE=0", "ExecStart=" + exe + " provider serve"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit missing %q", want)
		}
	}
}

func TestProviderDisableRemovesFiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProviderSideEffects(t)

	if err := providerEnable(nil); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := providerDisable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	for _, p := range []string{svcActivationPath(), svcUnitPath()} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present after disable", p)
		}
	}
}

func TestProviderDisableIdempotent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stubProviderSideEffects(t)
	if err := providerDisable(); err != nil {
		t.Fatalf("disable with nothing installed: %v", err)
	}
}

func TestSvcOwnerIsOursSelfClaim(t *testing.T) {
	bus := newSvcTestBus(t)
	conn := bus.connect(t)
	// The test process claiming the name makes us the owner — pid/exe
	// resolution should identify this binary as ours.
	if err := svcClaimName(conn); err != nil {
		t.Fatalf("claim: %v", err)
	}
	ours, owner, _ := svcOwnerIsOurs(conn)
	if !ours {
		t.Fatalf("owner %s should resolve as ours", owner)
	}
	if !strings.Contains(owner, "pid=") {
		t.Errorf("owner desc missing pid: %s", owner)
	}
}

func TestSvcOwnerIsOursForeignAndUnowned(t *testing.T) {
	bus := newSvcTestBus(t)
	conn := bus.connect(t)
	ours, owner, _ := svcOwnerIsOurs(conn)
	if ours {
		t.Fatal("unowned name resolved as ours")
	}
	if owner != "" {
		t.Fatalf("unowned name gave owner %q", owner)
	}
}

// TestSvcOwnerIsOursNonDumpable exercises the comm fallback: the provider
// sets PR_SET_DUMPABLE=0 at exec, which makes /proc/<pid> root-owned so the
// exe readlink fails even for the owner. A helper process (same binary)
// reproduces exactly that state, claims the name, and svcOwnerIsOurs must
// still resolve it as ours.
func TestSvcOwnerIsOursNonDumpable(t *testing.T) {
	if os.Getenv("SVC_TEST_HELPER") != "" {
		// Helper mode: mark self non-dumpable (root-owned /proc, exactly
		// like the running provider), claim the name, hold until killed.
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			os.Exit(2)
		}
		conn, err := svcConnect(os.Getenv("SVC_TEST_BUS"))
		if err != nil {
			os.Exit(3)
		}
		if err := svcClaimName(conn); err != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}

	bus := newSvcTestBus(t)
	conn := bus.connect(t)
	cmd := exec.Command(os.Args[0], "-test.run", "TestSvcOwnerIsOursNonDumpable")
	cmd.Env = append(os.Environ(),
		"SVC_TEST_HELPER=1", "SVC_TEST_BUS="+bus.addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("helper spawn: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	// Wait for the helper to own the name.
	var ownerPid uint32
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var unique string
		if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, svcBusName).Store(&unique); err == nil && unique != conn.Names()[0] {
			p, err := svcPeerPID(conn, unique)
			if err == nil {
				ownerPid = p
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ownerPid == 0 {
		t.Fatal("helper never claimed the name")
	}

	// The helper is non-dumpable — its exe link must be unreadable here
	// (root-run environments may not honor this; skip rather than fail).
	if _, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", ownerPid)); err == nil {
		t.Skip("environment permits exe readlink on non-dumpable processes")
	}

	ours, owner, _ := svcOwnerIsOurs(conn)
	if !ours {
		t.Fatalf("non-dumpable owner must resolve as ours via comm fallback: %s", owner)
	}
}

func TestSvcActivationUnderXDGDataHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	want := filepath.Join(dir, "dbus-1", "services", "org.freedesktop.secrets.service")
	if got := svcActivationPath(); got != want {
		t.Fatalf("activation path %q, want %q", got, want)
	}
}

// TestContribUnitMatchesTemplate pins contrib/omaseal-provider.service (the
// file the package installs) to the unit body `provider enable` writes —
// single-sourcing the unit facts so the two install paths can't drift.
func TestContribUnitMatchesTemplate(t *testing.T) {
	raw, err := os.ReadFile("../contrib/omaseal-provider.service")
	if err != nil {
		t.Skipf("contrib unit not present: %v", err)
	}
	// The contrib file may carry comment lines the embedded template omits;
	// the unit facts must be byte-identical once those are stripped.
	var body []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		body = append(body, line)
	}
	got := strings.TrimSpace(strings.Join(body, "\n")) + "\n"
	if got != svcUnitContents {
		t.Fatalf("contrib unit drifted from svcUnitContents:\n--- contrib ---\n%s\n--- embedded ---\n%s", got, svcUnitContents)
	}
}
