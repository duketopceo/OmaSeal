package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Provider activation files (U6). Two artifacts make the daemon the bus's
// Secret Service for this user:
//
//	dbus-1 services file — bus-activation entry; `SystemdService=` hands
//	  startups to the user unit under systemd, `Exec=` is the fallback for
//	  non-systemd sessions.
//	systemd user unit — Type=dbus pins readiness to the name claim;
//	  LimitCORE=0 keeps vault material out of crash dumps.
//
// The activation file lives under ~/.local/share/dbus-1/services so it
// shadows — not replaces — the system-wide gnome-keyring entry.

const svcActivationContents = `[D-BUS Service]
Name=org.freedesktop.secrets
Exec=/usr/bin/omaseal provider serve
SystemdService=omaseal-provider.service
`

const svcUnitContents = `[Unit]
Description=OmaSeal Secret Service provider
Documentation=https://github.com/duketopceo/OmaSeal

[Service]
Type=dbus
BusName=org.freedesktop.secrets
ExecStart=/usr/bin/omaseal provider serve
LimitCORE=0
Restart=on-failure

[Install]
WantedBy=default.target
`

// svcXDGDir resolves an XDG base dir with the spec fallback — the single
// home for the env-or-~ pattern the provider paths all share.
func svcXDGDir(envKey string, fallbackParts ...string) string {
	if base := os.Getenv(envKey); base != "" {
		return base
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append([]string{home}, fallbackParts...)...)
}

func svcActivationPath() string {
	return filepath.Join(svcXDGDir("XDG_DATA_HOME", ".local", "share"), "dbus-1", "services", "org.freedesktop.secrets.service")
}

func svcUnitPath() string {
	return filepath.Join(svcXDGDir("XDG_CONFIG_HOME", ".config"), "systemd", "user", "omaseal-provider.service")
}

// svcSystemctl is the systemctl seam — tests stub it so enable/disable
// never execs the real user's service manager.
var svcSystemctl = func(args ...string) ([]byte, error) {
	cmd := exec.Command("systemctl", args...)
	return cmd.CombinedOutput()
}

// svcConnectAmbient dials the session bus — a seam so tests never touch
// the real user's Secret Service owner.
var svcConnectAmbient = func() (*dbus.Conn, error) {
	return svcConnect("")
}

func svcDaemonReload() error {
	_, err := svcSystemctl("--user", "daemon-reload")
	return err
}

func svcStopUnit() error {
	out, err := svcSystemctl("--user", "stop", "omaseal-provider.service")
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// svcActivationIsOurs reports whether the installed activation file Execs an
// omaseal binary — compared by basename so an omaseal at any prefix counts.
// An installed-but-inactive omaseal activation can take the name mid-migrate
// (the moment the foreign owner exits or a call activates us), so guards
// that check only the live owner miss it.
func svcActivationIsOurs() bool {
	data, err := os.ReadFile(svcActivationPath())
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Exec=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Exec="))
		if len(fields) > 0 && filepath.Base(fields[0]) == "omaseal" {
			return true
		}
	}
	return false
}

// svcOwnerIsOurs reports whether the name's current owner is an omaseal
// binary — resolved via pid → /proc/<pid>/exe against os.Executable, so a
// foreign process claiming the name never counts.
func svcOwnerIsOurs(conn *dbus.Conn) (ours bool, owner string) {
	var unique string
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, svcBusName).Store(&unique); err != nil {
		return false, ""
	}
	pid, err := svcPeerPID(conn, unique)
	if err != nil {
		return false, unique
	}
	self, _ := os.Executable()
	target, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	ours = target != "" && self != "" && target == self
	return ours, fmt.Sprintf("%s pid=%d exe=%s", unique, pid, filepath.Base(target))
}

// providerEnable installs the activation shadow + user unit.
func providerEnable(args []string) error {
	for _, a := range args {
		return fmt.Errorf("unknown flag %q — `omaseal provider enable` takes none", a)
	}
	exe, err := os.Executable()
	if err == nil && !strings.HasPrefix(exe, "/usr/") {
		fmt.Fprintf(os.Stderr, "warning: templates pin ExecStart=/usr/bin/omaseal but this binary is %s — install the package or the unit will not start\n", exe)
	}
	if err := os.MkdirAll(filepath.Dir(svcActivationPath()), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(svcUnitPath()), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(svcActivationPath(), []byte(svcActivationContents), 0644); err != nil {
		return fmt.Errorf("activation file: %w", err)
	}
	if err := os.WriteFile(svcUnitPath(), []byte(svcUnitContents), 0644); err != nil {
		return fmt.Errorf("unit file: %w", err)
	}
	if err := svcDaemonReload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: daemon-reload failed (%v) — enable manually with `systemctl --user daemon-reload`\n", err)
	}

	// Store must exist before serve can start — init is TTY-only.
	store := newNativeStore()
	if _, err := os.Stat(storeIdentityPath(store)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: native store uninitialized — run `omaseal set` once in a terminal or the daemon will fail on activation\n")
	}

	fmt.Println("provider enabled:")
	fmt.Println("  " + svcActivationPath())
	fmt.Println("  " + svcUnitPath())

	conn, err := svcConnectAmbient()
	if err == nil {
		defer func() { _ = conn.Close() }()
		ours, owner := svcOwnerIsOurs(conn)
		switch {
		case ours:
			fmt.Println("omaseal already owns " + svcBusName)
		case owner != "":
			fmt.Printf("%s is owned by %s — omaseal takes over after that process exits or `omaseal provider disable`-style stop, then next activation\n", svcBusName, owner)
		default:
			fmt.Println("name is unowned — first activation claims it")
		}
	}
	return nil
}

// providerDisable stops the unit, then removes the activation shadow so the
// next activation returns to the system provider (gnome-keyring).
func providerDisable() error {
	if err := svcStopUnit(); err != nil {
		fmt.Fprintf(os.Stderr, "note: unit stop: %v (non-systemd session or unit inactive)\n", err)
	}
	var removed []string
	for _, p := range []string{svcActivationPath(), svcUnitPath()} {
		if err := os.Remove(p); err == nil {
			removed = append(removed, p)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	if err := svcDaemonReload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: daemon-reload failed (%v)\n", err)
	}
	if len(removed) == 0 {
		fmt.Println("provider was not enabled")
	} else {
		fmt.Println("provider disabled:")
		for _, p := range removed {
			fmt.Println("  removed", p)
		}
	}
	conn, err := svcConnectAmbient()
	if err == nil {
		defer func() { _ = conn.Close() }()
		if _, owner := svcOwnerIsOurs(conn); owner != "" {
			fmt.Printf("note: %s still owned by %s until that process exits\n", svcBusName, owner)
		}
	}
	return nil
}
