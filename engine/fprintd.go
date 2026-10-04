package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	fprintBusName       = "net.reactivated.Fprint"
	fprintManagerPath   = dbus.ObjectPath("/net/reactivated/Fprint/Manager")
	fprintManagerIface  = "net.reactivated.Fprint.Manager"
	fprintDeviceIface   = "net.reactivated.Fprint.Device"
	fprintVerifyTimeout = 15 * time.Second
	fprintProbeTimeout  = 5 * time.Second
)

// fprintdAvailable returns nil if the fprintd service is active and has at
// least one usable device. It is the single probe behind both `omaseal
// doctor`'s hardware report and the presence gate's fprintdUsable check.
func fprintdAvailable(ctx context.Context) error {
	if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "fprintd.service").Run(); err != nil {
		return errors.New("fprintd.service is not active")
	}

	conn, err := dbus.SystemBus()
	if err != nil {
		return errors.New("cannot connect to the D-Bus system bus")
	}
	defer conn.Close()

	var names []string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return fmt.Errorf("cannot list D-Bus names: %w", err)
	}
	found := false
	for _, n := range names {
		if n == fprintBusName {
			found = true
			break
		}
	}
	if !found {
		return errors.New("fprintd is not on the system bus")
	}

	mgr := conn.Object(fprintBusName, fprintManagerPath)
	var devicePath dbus.ObjectPath
	if err := mgr.CallWithContext(ctx, fprintManagerIface+".GetDefaultDevice", 0).Store(&devicePath); err != nil {
		return fmt.Errorf("fprintd has no default device: %w", err)
	}
	if devicePath == "" || devicePath == "/" {
		return errors.New("fprintd has no enrolled device")
	}
	return nil
}

// fprintdUsable is the bounded boolean form of fprintdAvailable for the
// presence chain: false means "no biometric mechanism", not "denied" — the
// caller runs its next mechanism instead of failing.
func fprintdUsable(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, fprintProbeTimeout)
	defer cancel()
	return fprintdAvailable(probeCtx) == nil
}

// FprintdVerify performs a real fingerprint verification and fails closed:
// it returns an error when no reader is usable and on every verification
// failure. Callers wanting a fallback should probe with fprintdUsable first.
//
// A device present but unenrolled lands here and fails closed at verify time.
// The secret is only released when the user explicitly matches.
func FprintdVerify(ctx context.Context, reason string) error {
	WriteLog("fprintd: verify for %s", reason)

	if !fprintdUsable(ctx) {
		return errors.New("fingerprint reader not available")
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, fprintVerifyTimeout)
	defer verifyCancel()

	conn, err := dbus.SystemBus()
	if err != nil {
		return errors.New("cannot connect to the D-Bus system bus")
	}
	defer conn.Close()

	mgr := conn.Object(fprintBusName, fprintManagerPath)
	var devicePath dbus.ObjectPath
	if err := mgr.CallWithContext(verifyCtx, fprintManagerIface+".GetDefaultDevice", 0).Store(&devicePath); err != nil {
		return fmt.Errorf("fprintd has no default device: %w", err)
	}

	dev := conn.Object(fprintBusName, devicePath)
	if err := dev.CallWithContext(verifyCtx, fprintDeviceIface+".Claim", 0, "").Err; err != nil {
		return fmt.Errorf("fprintd Claim failed: %w", err)
	}
	defer dev.CallWithContext(context.Background(), fprintDeviceIface+".Release", 0)

	// Only accept VerifyStatus signals actually emitted by fprintd. Without a
	// sender constraint, any local user can broadcast a forged verify-match on
	// the system bus and bypass the biometric gate.
	var fprintdOwner string
	if err := conn.BusObject().CallWithContext(verifyCtx, "org.freedesktop.DBus.GetNameOwner", 0, fprintBusName).Store(&fprintdOwner); err != nil {
		return fmt.Errorf("fprintd owner lookup failed: %w", err)
	}

	matchRule := fmt.Sprintf("type='signal',sender='%s',interface='%s',member='VerifyStatus',path='%s'", fprintBusName, fprintDeviceIface, devicePath)
	if err := conn.BusObject().CallWithContext(verifyCtx, "org.freedesktop.DBus.AddMatch", 0, matchRule).Err; err != nil {
		return fmt.Errorf("fprintd AddMatch failed: %w", err)
	}
	defer conn.BusObject().CallWithContext(context.Background(), "org.freedesktop.DBus.RemoveMatch", 0, matchRule)

	ch := make(chan *dbus.Signal, 4)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)

	if err := dev.CallWithContext(verifyCtx, fprintDeviceIface+".VerifyStart", 0, "any").Err; err != nil {
		return fmt.Errorf("fprintd VerifyStart failed: %w", err)
	}
	defer dev.CallWithContext(context.Background(), fprintDeviceIface+".VerifyStop", 0)

	for {
		select {
		case <-verifyCtx.Done():
			return errors.New("fingerprint verification timeout")
		case sig, ok := <-ch:
			if !ok {
				return errors.New("fingerprint signal channel closed")
			}
			if sig.Sender != fprintdOwner || sig.Path != devicePath || sig.Name != fprintDeviceIface+".VerifyStatus" {
				continue
			}
			if len(sig.Body) < 2 {
				continue
			}
			result, _ := sig.Body[0].(string)
			done, _ := sig.Body[1].(bool)
			switch result {
			case "verify-match":
				WriteLog("fprintd: matched for %s", reason)
				return nil
			case "verify-no-match", "verify-disconnected", "verify-unknown-error":
				if done {
					WriteLog("fprintd: %s for %s", result, reason)
					return fmt.Errorf("fingerprint verification failed: %s", result)
				}
			case "verify-retry-scan", "verify-swipe-too-short", "verify-finger-not-centered", "verify-remove-and-retry":
				// transient; wait for another signal
			default:
				if done {
					WriteLog("fprintd: %s for %s", result, reason)
					return fmt.Errorf("fingerprint verification ended: %s", result)
				}
			}
		}
	}
}
