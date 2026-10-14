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

func svcActivationPath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "share")
		}
	}
	return filepath.Join(base, "dbus-1", "services", "org.freedesktop.secrets.service")
}

func svcUnitPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "systemd", "user", "omaseal-provider.service")
}

func svcDaemonReload() error {
	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func svcStopUnit() error {
	cmd := exec.Command("systemctl", "--user", "stop", "omaseal-provider.service")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
func providerEnable(_ []string) error {
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

	conn, err := svcConnect("")
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
	conn, err := svcConnect("")
	if err == nil {
		defer func() { _ = conn.Close() }()
		if _, owner := svcOwnerIsOurs(conn); owner != "" {
			fmt.Printf("note: %s still owned by %s until that process exits\n", svcBusName, owner)
		}
	}
	return nil
}
