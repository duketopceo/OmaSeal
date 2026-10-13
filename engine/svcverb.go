package main

import (
	"errors"
	"fmt"
	"os"
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
		if err := providerStatus(); err != nil {
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

	conn, err := svcConnect("")
	if err != nil {
		return err
	}
	if err := svcClaimName(conn); err != nil {
		_ = conn.Close()
		return err
	}

	d := &svcDaemon{
		conn:     conn,
		store:    store,
		sessions: map[dbus.ObjectPath]*svcSession{},
		lostName: make(chan struct{}),
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := d.installObjects(); err != nil {
		_ = conn.Close()
		return fmt.Errorf("export object surface: %w", err)
	}
	d.watchNames()

	WriteLog("provider: serving %s on the session bus", svcBusName)
	<-d.lostName
	return fmt.Errorf("lost %s", svcBusName)
}

// storeIdentityPath reports the daemon's native identity file — the
// existence check that gates serve.
func storeIdentityPath(s *nativeStore) string {
	return s.dir + "/" + nativeIdentityFile
}

// installObjects is wired by the object-surface unit (U3): exports the
// subtree router + static paths. Present here so serve compiles standalone.
func (d *svcDaemon) installObjects() error { return nil }

// providerStatus reports who owns org.freedesktop.secrets. The enabled-state
// and activation-path reporting lands with the packaging unit (U6).
func providerStatus() error {
	conn, err := svcConnect("")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var unique string
	err = conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, svcBusName).Store(&unique)
	if err != nil {
		fmt.Println(svcBusName, "is unowned — first activation wins")
		return nil
	}
	fmt.Printf("%s owned by %s\n", svcBusName, svcOwnerDesc(conn))
	return nil
}

// providerEnable/providerDisable are the packaging unit's bodies (U6).
func providerEnable(_ []string) error { return errors.New("not yet implemented") }
func providerDisable() error          { return errors.New("not yet implemented") }
