package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strings"
	"syscall"
	"time"

	"github.com/zalando/go-keyring"
)

const sudoUsage = "usage: omaseal sudo [-r svc/acct] [--] <cmd> [args]"

// defaultSudoRef names the sudo password item for the invoking user:
// sudo/$USER (e.g. sudo/lukekimball). -r overrides it.
func defaultSudoRef() string {
	u := os.Getenv("USER")
	if u == "" {
		if cu, err := user.Current(); err == nil {
			u = cu.Username
		}
	}
	if u == "" {
		u = "default"
	}
	return "sudo/" + u
}

// parseSudoArgs splits [-r ref] [--] cmd args — the first bare token (or
// everything after --) starts the command, mirroring `omaseal run`.
func parseSudoArgs(args []string) (ref string, cmdArgs []string, err error) {
	ref = defaultSudoRef()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return ref, args[i+1:], nil
		case a == "-r" || a == "--ref":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s needs a svc/acct value", a)
			}
			ref = args[i+1]
			i++
		case strings.HasPrefix(a, "-r=") || strings.HasPrefix(a, "--ref="):
			ref = strings.SplitN(a, "=", 2)[1]
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("unknown flag %q", a)
		default:
			return ref, args[i:], nil
		}
	}
	return ref, nil, nil
}

// handleSudo feeds the stored sudo password to `sudo -S` — but only after a
// strict presence check and within a rate limit. The feature is deliberately
// CLI-only: no MCP tool, no IPC verb, nothing in setup — an agent that can
// invoke it still cannot pass the presence check it would trigger.
func handleSudo() {
	ref, cmdArgs, err := parseSudoArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "sudo: %v — %s\n", err, sudoUsage)
		os.Exit(2)
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintf(os.Stderr, "sudo: no command — %s\n", sudoUsage)
		os.Exit(2)
	}
	service, account, err := runRef(ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sudo: %v — %s\n", err, sudoUsage)
		os.Exit(2)
	}
	os.Exit(sudoFeed(context.Background(), service, account, cmdArgs, os.Stderr))
}

// sudoGetSecret is the keyring seam — tests stub it to prove the secret is
// never read when presence fails.
var sudoGetSecret = Get

// sudoFeed is the gated core of `omaseal sudo`, split from handleSudo so
// tests can exercise the rate/presence/read ordering without os.Exit. It
// returns the process exit code and writes user-facing errors to errw —
// never the secret.
func sudoFeed(ctx context.Context, service, account string, cmdArgs []string, errw io.Writer) int {
	// Rate limit covers the ATTEMPT, not just successful feeds — a caller
	// looping this command to flood presence prompts burns budget too.
	unlock, err := checkSudoRate(time.Now())
	if err != nil {
		WriteLog("sudo: rate-limited %s: %v", cmdArgs[0], err)
		fmt.Fprintf(errw, "sudo: %v\n", err)
		return 1
	}
	defer unlock()

	WriteLog("sudo: attempt %s via %s/%s", cmdArgs[0], service, account)
	if err := requirePresenceStrict(ctx,
		"feed sudo password to "+cmdArgs[0],
		"run sudo yourself — omaseal sudo requires user presence"); err != nil {
		WriteLog("sudo: presence refused for %s: %v", cmdArgs[0], err)
		fmt.Fprintf(errw, "sudo: %v\n", err)
		return 1
	}

	secret, err := sudoGetSecret(service, account)
	if err != nil {
		// Same contract as `omaseal run`: a genuine miss is 127; a locked or
		// unavailable keyring is 1 — it must not masquerade as "no secret".
		if errors.Is(err, keyring.ErrNotFound) || codeFromError(err) == "not_found" {
			WriteLog("sudo: no secret for %s/%s", service, account)
			fmt.Fprintf(errw, "sudo: no secret for %s/%s\n", service, account)
			return 127
		}
		WriteLog("sudo: keyring error for %s/%s: %v", service, account, err)
		fmt.Fprintf(errw, "sudo: cannot read %s/%s: %v\n", service, account, err)
		return 1
	}

	// Fixed-path sudo: a PATH shim must never receive the password stream.
	sudoBins := fixedPaths("sudo")
	if len(sudoBins) == 0 {
		fmt.Fprintln(errw, "sudo: /usr/bin/sudo (or /bin/sudo) not found")
		return 1
	}
	sudoBin := sudoBins[0]

	// Auth first via `sudo -v` with ONLY the secret on stdin: a wrong stored
	// password fails there instead of consuming the caller's stdin, and the
	// secret can never reach the target command's stream. The target then
	// runs via `sudo -n` on the timestamp cache — timestamp_timeout=0 fails
	// closed with "a password is required".
	auth := exec.Command(sudoBin, "-v", "-S", "-p", "")
	auth.Stdin = strings.NewReader(secret + "\n")
	auth.Stdout, auth.Stderr = nil, os.Stderr
	stopAuth := forwardSignals(auth)
	err = auth.Run()
	stopAuth()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			WriteLog("sudo: auth rejected for %s (exit %d)", cmdArgs[0], ee.ExitCode())
			fmt.Fprintf(errw, "sudo: authentication failed — stored secret at %s/%s did not satisfy sudo\n", service, account)
			return ee.ExitCode()
		}
		WriteLog("sudo: auth spawn failed for %s: %v", cmdArgs[0], err)
		fmt.Fprintf(errw, "sudo: %v\n", err)
		return 1
	}

	cmd := exec.Command(sudoBin, append([]string{"-n"}, cmdArgs...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	stopCmd := forwardSignals(cmd)
	err = cmd.Run()
	stopCmd()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			WriteLog("sudo: %s exited %d", cmdArgs[0], ee.ExitCode())
			return ee.ExitCode()
		}
		WriteLog("sudo: spawn failed for %s: %v", cmdArgs[0], err)
		fmt.Fprintf(errw, "sudo: %v\n", err)
		return 1
	}
	WriteLog("sudo: fed to %s", cmdArgs[0])
	return 0
}

// forwardSignals relays INT/TERM to the child. The returned stop func ends
// the relay — call it after Run so a dead child isn't signalled by a stale
// registration while a later command runs.
func forwardSignals(cmd *exec.Cmd) func() {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sig {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()
	return func() { signal.Stop(sig); close(sig) }
}
