package main

import (
	"os"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// Suspend/lock watch (U1): an unlocked vault must not ride across a sleep
// boundary. The daemon subscribes to logind on the SYSTEM bus — a second
// connection alongside the session-bus serve conn — and relocks through the
// same lockDaemonLocked path that Service.Lock, TTL expiry, and `agent lock`
// use. No new lock machinery lives here.
//
// Three observed events:
//
//	Manager.PrepareForSleep(true)  — suspend is imminent; relock, then
//	  release the delay inhibitor so logind may proceed.
//	Manager.PrepareForSleep(false) — resumed; re-arm the inhibitor for the
//	  next cycle.
//	Session.Lock                   — `loginctl lock-session`; relock where
//	  the session object path is resolvable.
//
// The sleep delay inhibitor asks logind to hold the suspend until our fd is
// released (bounded by logind's inhibitor timeout), so the relock completes
// before the kernel actually sleeps. If the inhibit call fails — a headless
// or non-logind environment — the watch degrades to best-effort: the signal
// still arrives before sleep, the relock just races the suspend ramp.
// Without any system bus (dbus-run-session tests, containers) the watch
// logs and skips itself; serving does not depend on it.

const (
	svcLogindName = "org.freedesktop.login1"
	svcLogindPath = dbus.ObjectPath("/org/freedesktop/login1")
	svcLogindMgr  = "org.freedesktop.login1.Manager"
	svcLogindSess = "org.freedesktop.login1.Session"
)

// svcSystemBus dials a private system-bus connection — a seam like
// svcConnectAmbient so tests never touch the real logind. Private like
// svcConnect: the watch closes its conn on teardown, which must not poison
// godbus's shared handle for other in-process consumers.
var svcSystemBus = func() (*dbus.Conn, error) {
	return dbus.SystemBusPrivate()
}

// watchLogind arms the suspend/session-lock subscription. Called once from
// providerServe after the name claim; failures are logged, never fatal.
func (d *svcDaemon) watchLogind() {
	sys, err := svcSystemBus()
	if err != nil {
		WriteLog("provider: no system bus — suspend lock watch disabled (%v)", err)
		return
	}
	obj := sys.Object(svcLogindName, svcLogindPath)

	if err := sys.AddMatchSignal(
		dbus.WithMatchSender(svcLogindName),
		dbus.WithMatchInterface(svcLogindMgr),
		dbus.WithMatchMember("PrepareForSleep")); err != nil {
		WriteLog("provider: suspend match failed (%v) — vault will not auto-lock on sleep", err)
		_ = sys.Close()
		return
	}

	// Session.Lock needs our own session object path; a daemon started
	// outside a logind session (dbus-run-session, some autostart paths)
	// can't resolve one — sleep coverage still applies.
	var sessPath dbus.ObjectPath
	if err := obj.Call(svcLogindMgr+".GetSessionByPID", 0, uint32(os.Getpid())).Store(&sessPath); err == nil {
		if err := sys.AddMatchSignal(
			dbus.WithMatchSender(svcLogindName),
			dbus.WithMatchInterface(svcLogindSess),
			dbus.WithMatchMember("Lock"),
			dbus.WithMatchObjectPath(sessPath)); err != nil {
			WriteLog("provider: session-lock match failed (%v)", err)
		}
	} else {
		WriteLog("provider: no logind session for pid %d — session Lock not watched (%v)", os.Getpid(), err)
	}

	inhibit := svcSleepInhibit(sys)
	if inhibit < 0 {
		WriteLog("provider: sleep inhibitor refused — relock races suspend")
	}

	ch := make(chan *dbus.Signal, 8)
	sys.Signal(ch)
	go d.suspendLoop(sys, ch, inhibit)
	WriteLog("provider: suspend lock watch armed")
}

// svcSleepInhibit takes a delay inhibitor on "sleep" so logind holds the
// suspend transition until the fd is released (or its bound expires).
// Returns -1 when logind refuses.
func svcSleepInhibit(sys *dbus.Conn) int {
	var fd dbus.UnixFD
	err := sys.Object(svcLogindName, svcLogindPath).
		Call(svcLogindMgr+".Inhibit", 0,
			"sleep", "omaseal", "relock the vault before suspend", "delay").
		Store(&fd)
	if err != nil {
		return -1
	}
	return int(fd)
}

// suspendLoop consumes logind signals for the life of the daemon. The
// signal channel dying means the system-bus connection is gone — nothing
// to do but log; the session-bus serve continues.
func (d *svcDaemon) suspendLoop(sys *dbus.Conn, ch chan *dbus.Signal, inhibit int) {
	for sig := range ch {
		switch sig.Name {
		case svcLogindMgr + ".PrepareForSleep":
			if len(sig.Body) != 1 {
				continue
			}
			sleeping, _ := sig.Body[0].(bool)
			if sleeping {
				d.relockForSuspend("suspend")
				if inhibit >= 0 {
					_ = unix.Close(inhibit)
					inhibit = -1
				}
			} else if inhibit < 0 {
				// Re-arm for the next sleep cycle.
				inhibit = svcSleepInhibit(sys)
			}
		case svcLogindSess + ".Lock":
			d.relockForSuspend("session lock")
		}
	}
	// A still-armed inhibitor fd would delay every future suspend for the
	// daemon's remaining life — release it on teardown.
	if inhibit >= 0 {
		_ = unix.Close(inhibit)
	}
	WriteLog("provider: system bus signal channel closed — suspend lock watch ended")
	_ = sys.Close()
}

// relockForSuspend drops the vault through the shared lock path. logind
// holds the suspend while the inhibitor fd stays open, so this runs to
// completion before the machine sleeps.
func (d *svcDaemon) relockForSuspend(reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.store.identity.Load() == nil && !d.promptIdentity {
		return // already locked — no work, no log noise
	}
	WriteLog("provider: %s — relocking vault", reason)
	d.lockDaemonLocked()
}
