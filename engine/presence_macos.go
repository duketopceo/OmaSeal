//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// macosPresenceHelperTimeout bounds how long one Touch ID / password prompt
// may stay open before the helper is killed. A lapsed prompt is a DENIAL, not
// "unavailable": otherwise a prompt raised while the user is away would time
// out and fall through to allow_ungated.
const macosPresenceHelperTimeout = 60 * time.Second

// errPresenceHelperLapsed marks a helper run that timed out or was cancelled
// while the prompt was up.
var errPresenceHelperLapsed = errors.New("presence prompt lapsed")

// Test seams for the macOS helper step (OS-independent).
var (
	presenceExecutableFunc = os.Executable
	presenceHomeDirFunc    = os.UserHomeDir
	presenceUIDFunc        = os.Getuid
	// presenceRunHelperFunc runs the helper and returns its exit code.
	// err != nil means it could not be run to completion (start failure, timeout).
	presenceRunHelperFunc = runPresenceHelper
)

// presenceHelperCandidates lists helper locations in trust order: beside the
// running omaseal binary, then the system app bundle, then the user bundle.
func presenceHelperCandidates() []string {
	var out []string
	if exe, err := presenceExecutableFunc(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		out = append(out, filepath.Join(filepath.Dir(exe), "omaseal-presence"))
	}
	const rel = "Applications/OmaSealBar.app/Contents/MacOS/omaseal-presence"
	out = append(out, "/"+rel)
	if home, err := presenceHomeDirFunc(); err == nil && home != "" {
		out = append(out, filepath.Join(home, rel))
	}
	return out
}

// checkPresenceHelper refuses anything but a regular file owned by the current
// user or root that no other user can write.
func checkPresenceHelper(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("group/world-writable (%v)", fi.Mode().Perm())
	}
	if fi.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("not executable by owner")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read file owner")
	}
	if uid := presenceUIDFunc(); int(st.Uid) != uid && st.Uid != 0 {
		return fmt.Errorf("owned by uid %d, not you or root", st.Uid)
	}
	return nil
}

// findPresenceHelper returns the first candidate that exists and is safe.
// Unsafe candidates are logged and skipped; none found returns "".
func findPresenceHelper() string {
	for _, c := range presenceHelperCandidates() {
		if _, err := os.Lstat(c); err != nil {
			continue
		}
		resolved := c
		if r, err := filepath.EvalSymlinks(c); err == nil {
			resolved = r
		}
		if err := checkPresenceHelper(resolved); err != nil {
			WriteLog("presence: refusing macOS helper %s: %v", c, err)
			continue
		}
		return resolved
	}
	return ""
}

// presenceHelperUsable reports whether a safe helper is installed (status label).
func presenceHelperUsable() bool { return findPresenceHelper() != "" }

// runPresenceHelper executes the helper with no stdin and a minimal env.
func runPresenceHelper(ctx context.Context, path, reason string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, macosPresenceHelperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--reason", reason)
	cmd.Stdin = nil // /dev/null: caller stdin never counts
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = presenceHelperEnv()
	err := cmd.Run()
	if ctx.Err() != nil {
		return 0, fmt.Errorf("%w: %w", errPresenceHelperLapsed, ctx.Err())
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

func presenceHelperEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "LANG"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// macosHelperPresence is the macOS chain step. nil = user authenticated,
// errPresenceDenied = user said no (stops the chain), errMacPresenceUnavailable
// = fall through.
func macosHelperPresence(ctx context.Context, reason string) error {
	path := findPresenceHelper()
	if path == "" {
		return errMacPresenceUnavailable
	}
	code, err := presenceRunHelperFunc(ctx, path, reason)
	if errors.Is(err, errPresenceHelperLapsed) {
		// Unanswered prompt: deny and stop the chain (never fall through to
		// allow_ungated), matching the Linux GUI-confirm rule.
		WriteLog("presence: macOS prompt lapsed: %v", err)
		return errPresenceDenied
	}
	if err != nil {
		// Could not start the helper at all: no mechanism here.
		WriteLog("presence: macOS helper did not start: %v", err)
		return errMacPresenceUnavailable
	}
	switch code {
	case 0:
		return nil
	case 2:
		WriteLog("presence: macOS helper unavailable (exit 2)")
		return errMacPresenceUnavailable
	default:
		// 1 = user denied; anything else (crash, kill, unknown code) fails
		// closed rather than falling through to allow_ungated.
		if code != 1 {
			WriteLog("presence: macOS helper exited %d — treated as denied", code)
		}
		return errPresenceDenied
	}
}
