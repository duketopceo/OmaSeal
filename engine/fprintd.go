package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	fprintBusName       = "net.reactivated.Fprint"
	fprintManagerPath   = dbus.ObjectPath("/net/reactivated/Fprint/Manager")
	fprintManagerIface  = "net.reactivated.Fprint.Manager"
	fprintDeviceIface   = "net.reactivated.Fprint.Device"
	fprintVerifyTimeout = 15 * time.Second
)

// FprintdVerify starts a best-effort fprintd fingerprint verification.
//
// If the fprintd daemon is not available or no reader is enrolled, it returns
// nil so that the caller can proceed without a fingerprint gate. This mirrors
// the macOS Touch ID best-effort posture: protect where possible, but do not
// hard-fail on machines without biometric hardware.
//
// If fprintd is available and the user explicitly fails verification (no
// match, timeout, etc.) it returns an error and the caller should not release
// the secret.
func FprintdVerify(ctx context.Context, reason string) error {
	_ = reason

	conn, err := dbus.SystemBus()
	if err != nil {
		return nil
	}
	defer conn.Close()

	mgr := conn.Object(fprintBusName, fprintManagerPath)
	var devicePath dbus.ObjectPath
	if err := mgr.Call(fprintManagerIface+".GetDefaultDevice", 0).Store(&devicePath); err != nil {
		log.Printf("oma-ring: fprintd not available, skipping biometric prompt (%v)", err)
		return nil
	}
	if devicePath == "" || devicePath == "/" {
		return nil
	}

	dev := conn.Object(fprintBusName, devicePath)
	if err := dev.Call(fprintDeviceIface+".Claim", 0, "").Err; err != nil {
		log.Printf("oma-ring: fprintd Claim failed, skipping biometric prompt (%v)", err)
		return nil
	}
	defer dev.Call(fprintDeviceIface+".Release", 0)

	matchRule := fmt.Sprintf("type='signal',interface='%s',member='VerifyStatus',path='%s'", fprintDeviceIface, devicePath)
	if err := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule).Err; err != nil {
		log.Printf("oma-ring: fprintd AddMatch failed, skipping biometric prompt (%v)", err)
		return nil
	}
	defer conn.BusObject().Call("org.freedesktop.DBus.RemoveMatch", 0, matchRule)

	ch := make(chan *dbus.Signal, 4)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)

	if err := dev.Call(fprintDeviceIface+".VerifyStart", 0, "any").Err; err != nil {
		log.Printf("oma-ring: fprintd VerifyStart failed, skipping biometric prompt (%v)", err)
		return nil
	}
	defer dev.Call(fprintDeviceIface+".VerifyStop", 0)

	ctx, cancel := context.WithTimeout(ctx, fprintVerifyTimeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return errors.New("fingerprint verification timeout")
		case sig, ok := <-ch:
			if !ok {
				return errors.New("fingerprint signal channel closed")
			}
			if sig.Path != devicePath || sig.Name != fprintDeviceIface+".VerifyStatus" {
				continue
			}
			if len(sig.Body) < 2 {
				continue
			}
			result, _ := sig.Body[0].(string)
			done, _ := sig.Body[1].(bool)
			switch result {
			case "verify-match":
				return nil
			case "verify-no-match", "verify-disconnected", "verify-unknown-error":
				if done {
					return fmt.Errorf("fingerprint verification failed: %s", result)
				}
			case "verify-retry-scan", "verify-swipe-too-short", "verify-finger-not-centered", "verify-remove-and-retry":
				// transient; wait for another signal
			default:
				if done {
					return fmt.Errorf("fingerprint verification ended: %s", result)
				}
			}
		}
	}
}
