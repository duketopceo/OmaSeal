package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderEnableWritesActivationAndUnit(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := providerEnable(nil); err != nil {
		t.Fatalf("enable: %v", err)
	}
	act, err := os.ReadFile(svcActivationPath())
	if err != nil {
		t.Fatalf("activation file: %v", err)
	}
	for _, want := range []string{"Name=org.freedesktop.secrets", "SystemdService=omaseal-provider.service", "Exec=/usr/bin/omaseal provider serve"} {
		if !strings.Contains(string(act), want) {
			t.Errorf("activation missing %q", want)
		}
	}
	unit, err := os.ReadFile(svcUnitPath())
	if err != nil {
		t.Fatalf("unit file: %v", err)
	}
	for _, want := range []string{"Type=dbus", "BusName=org.freedesktop.secrets", "LimitCORE=0", "ExecStart=/usr/bin/omaseal provider serve"} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("unit missing %q", want)
		}
	}
}

func TestProviderDisableRemovesFiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

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
	ours, owner := svcOwnerIsOurs(conn)
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
	ours, owner := svcOwnerIsOurs(conn)
	if ours {
		t.Fatal("unowned name resolved as ours")
	}
	if owner != "" {
		t.Fatalf("unowned name gave owner %q", owner)
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
