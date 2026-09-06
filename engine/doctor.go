package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type checkResult struct {
	name    string
	ok      bool
	message string
}

func doctorChecks() []checkResult {
	return []checkResult{
		checkBinary(),
		checkSecretService(),
		checkFprintd(),
		checkOnePassword(),
		checkBitwarden(),
		checkPath(),
	}
}

func printDoctorResults(results []checkResult) bool {
	ok := true
	for _, r := range results {
		mark := "ok"
		if !r.ok {
			mark = "FAIL"
			ok = false
		}
		fmt.Fprintf(os.Stderr, "%-18s %s\n", mark, r.name)
		if r.message != "" {
			for _, line := range strings.Split(r.message, "\n") {
				fmt.Fprintf(os.Stderr, "                   %s\n", line)
			}
		}
	}
	return ok
}

func runDoctor() {
	results := doctorChecks()
	ok := printDoctorResults(results)
	if !ok {
		fmt.Fprintln(os.Stderr, "\nSome checks failed. Run `omaseal setup` for next steps.")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "\nAll checks passed. OmaSeal is ready to use.")
}

func checkBinary() checkResult {
	return checkResult{name: "binary", ok: true, message: versionInfo()}
}

func versionInfo() string {
	return fmt.Sprintf("omaseal %s (%s) %s", version, commit, target())
}

func checkSecretService() checkResult {
	if !commandExists("gnome-keyring-daemon") {
		return checkResult{
			name: "secret-service",
			ok:   false,
			message: "`gnome-keyring-daemon` not found.\n" +
				"  - Install `gnome-keyring` and `libsecret` from your package manager.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-e").Output()
	if err == nil && containsLine(string(out), "gnome-keyring-d") {
		return checkResult{name: "secret-service", ok: true, message: "gnome-keyring-daemon is running"}
	}

	return checkResult{
		name: "secret-service",
		ok:   false,
		message: "`gnome-keyring-daemon` is installed but not running.\n" +
			"  - On Omarchy, the keyring is normally started by the session.\n" +
			"  - Try `gnome-keyring-daemon --start --daemonize` or a shell restart.",
	}
}

func containsLine(text, needle string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func checkFprintd() checkResult {
	if !commandExists("fprintd") {
		return checkResult{
			name: "fprintd",
			ok:   false,
			message: "`fprintd` is not installed.\n" +
				"  - `reveal` will fall through without a fingerprint gate.\n" +
				"  - Install and enable `fprintd` and run `fprintd-enroll` to enable biometric gating.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "fprintd.service").Run(); err == nil {
		return checkResult{name: "fprintd", ok: true, message: "fprintd.service is active"}
	}

	return checkResult{
		name: "fprintd",
		ok:   false,
		message: "`fprintd` is installed but the service is not active.\n" +
			"  - Start it with `systemctl start fprintd.service --user` or `sudo systemctl start fprintd`.\n" +
			"  - Run `fprintd-enroll` after it is active.",
	}
}

func checkOnePassword() checkResult {
	if !commandExists("op") {
		return checkResult{
			name: "1password (op)",
			ok:   false,
			message: "`op` CLI not found.\n" +
				"  - Install the 1Password CLI and run `op signin` to enable `omaseal resolve` / `import 1password`.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "op", "vault", "list", "--format=json").Output()
	if err == nil && len(bytes.TrimSpace(out)) > 0 {
		return checkResult{name: "1password (op)", ok: true, message: "`op` CLI found and authenticated"}
	}

	return checkResult{
		name: "1password (op)",
		ok:   false,
		message: "`op` CLI is installed but not signed in.\n" +
			"  - Run `op signin` to enable `omaseal resolve` / `import 1password`.",
	}
}

func checkBitwarden() checkResult {
	if !commandExists("bw") {
		return checkResult{
			name: "bitwarden (bw)",
			ok:   false,
			message: "`bw` CLI not found.\n" +
				"  - Install the Bitwarden CLI and run `bw login` to enable `omaseal resolve` / `import bitwarden`.",
		}
	}
	if os.Getenv("BW_SESSION") != "" {
		return checkResult{name: "bitwarden (bw)", ok: true, message: "`bw` CLI found and BW_SESSION is set"}
	}
	return checkResult{
		name: "bitwarden (bw)",
		ok:   false,
		message: "`bw` CLI found but `BW_SESSION` is not set.\n" +
			"  - Run `bw login` and `export BW_SESSION=...` to enable `omaseal resolve` / `import bitwarden`.",
	}
}

func checkPath() checkResult {
	self, err := os.Executable()
	if err != nil {
		return checkResult{name: "PATH", ok: false, message: "Cannot locate the running binary"}
	}
	dir := filepath.Dir(self)
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return checkResult{name: "PATH", ok: true, message: fmt.Sprintf("`%s` is on PATH", dir)}
		}
	}
	return checkResult{
		name: "PATH",
		ok:   false,
		message: fmt.Sprintf("`%s` is not on your PATH.\n  - Add `export PATH=\"%s:$PATH\"` to your shell profile.", dir, dir),
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
