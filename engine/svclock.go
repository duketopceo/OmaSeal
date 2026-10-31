package main

import (
	"context"
	"os"
	"time"

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

// svcSystemBus returns godbus's shared system-bus connection — already
// authed and Hello'd (a private dial needs auth+Hello the caller must
// drive itself, and an unauthed conn's calls hang with no reply). Shared
// means the watch must never Close it; teardown is RemoveSignal only.
var svcSystemBus = func() (*dbus.Conn, error) {
	return dbus.SystemBus()
}

// watchLogind arms the suspend/session-lock subscription. Called once from
// providerServe after the name claim; failures are logged, never fatal.
func (d *svcDaemon) watchLogind() {
	sys, err := svcSystemBus()
	if err != nil {
		WriteLog("provider: no system bus — suspend lock watch disabled (%v)", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	obj := sys.Object(svcLogindName, svcLogindPath)

	// Every setup call is bounded: a system-bus Call blocks until reply or
	// conn death with no timeout of its own, and a hung logind must degrade
	// to a logged skip — never a stuck goroutine.
	if err := sys.AddMatchSignalContext(ctx,
		dbus.WithMatchSender(svcLogindName),
		dbus.WithMatchInterface(svcLogindMgr),
		dbus.WithMatchMember("PrepareForSleep")); err != nil {
		WriteLog("provider: suspend match failed (%v) — vault will not auto-lock on sleep", err)
		return
	}

	// Session.Lock needs our own session object path; a daemon started
	// outside a logind session (dbus-run-session, some autostart paths)
	// can't resolve one — sleep coverage still applies.
	var sessPath dbus.ObjectPath
	if err := obj.CallWithContext(ctx, svcLogindMgr+".GetSessionByPID", 0, uint32(os.Getpid())).Store(&sessPath); err == nil {
		if err := sys.AddMatchSignalContext(ctx,
			dbus.WithMatchSender(svcLogindName),
			dbus.WithMatchInterface(svcLogindSess),
			dbus.WithMatchMember("Lock"),
			dbus.WithMatchObjectPath(sessPath)); err != nil {
			WriteLog("provider: session-lock match failed (%v)", err)
		}
	} else {
		WriteLog("provider: no logind session for pid %d — session Lock not watched (%v)", os.Getpid(), err)
	}

	inhibit := svcSleepInhibit(ctx, sys)
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
// Returns -1 when logind refuses or the bounded call expires.
func svcSleepInhibit(ctx context.Context, sys *dbus.Conn) int {
	var fd dbus.UnixFD
	err := sys.Object(svcLogindName, svcLogindPath).
		CallWithContext(ctx, svcLogindMgr+".Inhibit", 0,
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
				// Re-arm for the next sleep cycle — bounded like setup.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				inhibit = svcSleepInhibit(ctx, sys)
				cancel()
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
	// Shared conn: signal removal only, never Close — other in-process
	// consumers keep the handle.
	sys.RemoveSignal(ch)
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
