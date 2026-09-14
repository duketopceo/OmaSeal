package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// handleClipclear implements `omaseal clipclear <service> <account>`: clear
// the Wayland clipboard only if it still holds the given secret. The panel
// schedules this after copying so a stale timer cannot wipe whatever the
// user copied in the meantime. The secret stays inside this process — it is
// never passed on a command line or through a file.
func handleClipclear() {
	service, account, err := argCredentials(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Usage: omaseal clipclear <service> <account>")
		os.Exit(1)
	}
	if err := clipClearIfMatches(service, account); err != nil {
		printError("clipboard clear failed: ", err)
		os.Exit(1)
	}
	WriteLog("clipclear %s/%s", service, account)
}

// clipClearIfMatches compares the current clipboard contents to the stored
// secret and clears the selection only when they are identical.
func clipClearIfMatches(service, account string) error {
	secret, err := Get(service, account)
	if err != nil {
		return err
	}

	paste, err := exec.Command("wl-paste", "--no-newline").Output()
	if err != nil {
		// Nothing readable on the clipboard — nothing to clear.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return fmt.Errorf("wl-paste: %w", err)
	}
	if !bytes.Equal(paste, []byte(secret)) {
		return nil
	}
	if out, err := exec.Command("wl-copy", "--clear").CombinedOutput(); err != nil {
		return fmt.Errorf("wl-copy --clear: %w (%s)", err, out)
	}
	return nil
}
