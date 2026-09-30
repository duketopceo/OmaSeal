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

	// Rate limit covers the ATTEMPT, not just successful feeds — a caller
	// looping this command to flood presence prompts burns budget too.
	unlock, err := checkSudoRate(time.Now())
	if err != nil {
		WriteLog("sudo: rate-limited %s: %v", cmdArgs[0], err)
		fmt.Fprintf(os.Stderr, "sudo: %v\n", err)
		os.Exit(1)
	}
	defer unlock()

	WriteLog("sudo: attempt %s via %s/%s", cmdArgs[0], service, account)
	if err := requirePresenceStrict(context.Background(),
		"feed sudo password to "+cmdArgs[0],
		"run sudo yourself — omaseal sudo requires user presence"); err != nil {
		WriteLog("sudo: presence refused for %s: %v", cmdArgs[0], err)
		fmt.Fprintf(os.Stderr, "sudo: %v\n", err)
		os.Exit(1)
	}

	secret, err := Get(service, account)
	if err != nil {
		WriteLog("sudo: no secret for %s/%s", service, account)
		fmt.Fprintf(os.Stderr, "sudo: no secret for %s/%s\n", service, account)
		os.Exit(127)
	}

	// Fixed-path sudo: a PATH shim must never receive the password stream.
	sudoBins := fixedPaths("sudo")
	if len(sudoBins) == 0 {
		fmt.Fprintln(os.Stderr, "sudo: /usr/bin/sudo (or /bin/sudo) not found")
		os.Exit(1)
	}
	argv := append([]string{"-S", "-p", ""}, cmdArgs...)
	cmd := exec.Command(sudoBins[0], argv...)
	// sudo -S reads exactly one line per auth attempt; the caller's stdin
	// continues to the command after it. A wrong stored password lets sudo
	// eat the NEXT stdin line — same failure shape as a mistyped password.
	cmd.Stdin = io.MultiReader(strings.NewReader(secret+"\n"), os.Stdin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case s := <-sig:
				if cmd.Process != nil {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "sudo: %v\n", err)
		os.Exit(1)
	}
	WriteLog("sudo: fed to %s", cmdArgs[0])
}
