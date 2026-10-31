package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// `omaseal provider` — the Secret Service provider daemon verbs.
//
//	serve     claim org.freedesktop.secrets on the session bus and serve it
//	          (D-Bus/systemd activation target — normally not run by hand)
//	status    show who owns the name and whether activation is installed
//	enable    write the D-Bus activation file + systemd user unit
//	disable   stop the daemon and remove the activation shadow
//
// The verb dispatches BEFORE selectStore() in main — the daemon serves the
// native store directly and must not die on config it never reads, nor
// recurse into a `secretservice` backend that would call itself.

func handleProvider() {
	if len(os.Args) < 3 {
		providerUsage()
		os.Exit(1)
	}
	switch os.Args[2] {
	case "serve":
		if err := providerServe(); err != nil {
			fmt.Fprintln(os.Stderr, "provider serve:", err)
			os.Exit(1)
		}
	case "status":
		if err := providerStatus(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, "provider status:", err)
			os.Exit(1)
		}
	case "enable":
		if err := providerEnable(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, "provider enable:", err)
			os.Exit(1)
		}
	case "disable":
		if err := providerDisable(); err != nil {
			fmt.Fprintln(os.Stderr, "provider disable:", err)
			os.Exit(1)
		}
	default:
		providerUsage()
		os.Exit(1)
	}
}

func providerUsage() {
	fmt.Fprintf(os.Stderr, `omaseal provider — Secret Service provider daemon

  omaseal provider serve     own org.freedesktop.secrets on this session bus
  omaseal provider status    show the current name owner + enabled state
  omaseal provider enable    install the activation file + systemd user unit
  omaseal provider disable   stop the daemon, remove activation files
`)
}

// providerServe is the daemon entry. It claims the name politely and serves
// until the name is lost. Startup failures are typed errors — under systemd
// the unit simply fails rather than racing a foreign owner.
func providerServe() error {
	dropDumpable()

	store := newNativeStore()
	// An uninitialized store can never satisfy a bus Prompt — init needs a
	// TTY passphrase. Fail fast so clients see ServiceUnknown instead of a
	// permanently erroring provider.
	if _, err := os.Stat(storeIdentityPath(store)); err != nil {
		return fmt.Errorf("native store not initialized (no %s) — run `omaseal set` once in a terminal first", storeIdentityPath(store))
	}

	conn, err := svcConnectAmbient()
	if err != nil {
		return err
	}

	d := &svcDaemon{
		conn:     conn,
		store:    store,
		sessions: map[dbus.ObjectPath]*svcSession{},
		prompts:  map[dbus.ObjectPath]*svcPrompt{},
		index:    loadSvcIndex(svcIndexPath()),
		lostName: make(chan struct{}),
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := d.installObjects(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("export object surface: %w", err)
	}
	if err := d.watchNames(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("name watch: %w", err)
	}
	// Claim last: no client may reach the name before the object surface
	// and the loss watch are armed.
	if err := svcClaimName(conn); err != nil {
		_ = conn.Close()
		return err
	}

	// Arm the suspend/session-lock watch after the claim — async: its
	// setup makes synchronous system-bus round-trips (dial, AddMatch,
	// GetSessionByPID, Inhibit) and a hung logind must never wedge the
	// serve loop or the lostName wait. Failures self-log inside.
	go d.watchLogind()

	WriteLog("provider: serving %s on the session bus", svcBusName)
	<-d.lostName
	return fmt.Errorf("lost %s", svcBusName)
}

// storeIdentityPath reports the daemon's native identity file — the
// existence check that gates serve.
func storeIdentityPath(s *nativeStore) string {
	return filepath.Join(s.dir, nativeIdentityFile)
}

// providerStatus reports the name owner, enabled state, activation paths,
// and whether a running provider's environment can reach a GUI prompter.
// --json emits the same facts machine-readably for panels and scripts.
func providerStatus(args []string) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown flag %q — usage: omaseal provider status [--json]", a)
		}
	}
	conn, err := svcConnectAmbient()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var unique string
	owned := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, svcBusName).Store(&unique) == nil
	ours, ownerDesc, _ := svcOwnerIsOurs(conn)
	_, actErr := os.Stat(svcActivationPath())
	_, unitErr := os.Stat(svcUnitPath())
	enabled := actErr == nil || unitErr == nil

	unit := svcUnitState()

	if asJSON {
		out := map[string]any{
			"name":    svcBusName,
			"owned":   owned,
			"ours":    ours,
			"enabled": enabled,
		}
		if owned {
			out["owner"] = ownerDesc
		}
		if enabled {
			out["activation"] = svcActivationPath()
			out["unit"] = svcUnitPath()
		}
		if unit != nil {
			out["unit_state"] = unit
		}
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(out)
	}

	if !owned {
		fmt.Println(svcBusName, "is unowned — first activation wins")
	} else {
		fmt.Printf("%s owned by %s\n", svcBusName, ownerDesc)
		if ours {
			svcWarnIfNoDisplay(conn, unique)
		}
	}
	if enabled {
		fmt.Println("enabled: activation/unit installed")
		fmt.Println("  " + svcActivationPath())
		fmt.Println("  " + svcUnitPath())
	} else {
		fmt.Println("not enabled — `omaseal provider enable` installs activation")
	}
	svcPrintUnitState(unit, owned)
	return nil
}

// svcUnitState asks systemd how the provider unit is doing — the supervisor
// is the provenance recorder for deaths the daemon cannot report itself
// (kill -9, panic). Fields come from `systemctl --user show`; nil when no
// systemd is reachable or the unit is not loaded.
func svcUnitState() map[string]string {
	out, err := svcSystemctl("--user", "show", "omaseal-provider.service",
		"-p", "ActiveState", "-p", "SubState", "-p", "Result",
		"-p", "NRestarts", "-p", "ExecMainStatus", "-p", "LoadState")
	if err != nil {
		return nil
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			props[k] = v
		}
	}
	if len(props) == 0 || props["LoadState"] == "not-found" {
		return nil
	}
	return props
}

// svcPrintUnitState renders the provenance block: a dead or failed unit is
// reported as such, never silently absent. Without a supervisor record, an
// enabled-but-unowned name still recovers lazily — the activation file
// respawns the daemon on the next client call.
func svcPrintUnitState(unit map[string]string, owned bool) {
	if unit == nil {
		if !owned {
			fmt.Println("no supervisor record — if the daemon dies, the activation file respawns it on the next client call")
		}
		return
	}
	state := unit["ActiveState"]
	sub := unit["SubState"]
	result := unit["Result"]
	restarts := unit["NRestarts"]
	switch {
	case state == "active":
		fmt.Printf("unit: %s (%s)", state, sub)
	case state == "failed" || result != "" && result != "success":
		fmt.Printf("unit: %s — last exit %s", state, result)
	default:
		fmt.Printf("unit: %s (%s) — activates on next client call", state, sub)
	}
	if restarts != "" && restarts != "0" {
		fmt.Printf(", restarts: %s", restarts)
	}
	fmt.Println()
}

// svcWarnIfNoDisplay checks a running provider's environment for display
// access — without WAYLAND_DISPLAY/DISPLAY the Prompt path fails closed and
// unlock prompts silently can't render.
func svcWarnIfNoDisplay(conn *dbus.Conn, unique string) {
	pid, err := svcPeerPID(conn, unique)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return
	}
	// environ is NUL-separated; match exact variable names — a substring
	// scan would false-positive on any *_DISPLAY= entry.
	for entry := range strings.SplitSeq(string(raw), "\x00") {
		if strings.HasPrefix(entry, "WAYLAND_DISPLAY=") || strings.HasPrefix(entry, "DISPLAY=") {
			return
		}
	}
	fmt.Println("warning: provider environment has no WAYLAND_DISPLAY/DISPLAY — GUI unlock prompts will fail")
}
