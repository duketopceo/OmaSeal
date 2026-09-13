package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	// optional checks degrade to a warning instead of failing the doctor run.
	optional bool
}

func handleDoctor() {
	for _, a := range os.Args[2:] {
		if a == "--json" {
			runDoctorJSON()
			return
		}
	}
	runDoctor()
}

func runDoctorJSON() {
	results := doctorChecks()
	checks := make([]pingCheck, len(results))
	ok := true
	for i, r := range results {
		checks[i] = pingCheck{Name: r.name, Ok: r.ok, Optional: r.optional, Message: r.message}
		if !r.ok && !r.optional {
			ok = false
		}
	}
	res := pingResult{
		Version: version,
		Commit:  commit,
		Target:  target(),
		Ok:      ok,
		Checks:  checks,
		Help:    "omaseal setup",
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error marshaling doctor result:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func doctorChecks() []checkResult {
	return []checkResult{
		checkBinary(),
		checkSecretService(),
		checkFprintd(),
		checkOnePassword(),
		checkBitwarden(),
		checkGUIPrompt(),
		checkPath(),
	}
}

func printDoctorResults(results []checkResult) bool {
	ok := true
	for _, r := range results {
		mark := "ok"
		if !r.ok {
			if r.optional {
				mark = "warn"
			} else {
				mark = "FAIL"
				ok = false
			}
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
		fmt.Fprintln(os.Stderr, "\nSome required checks failed. Run `omaseal setup` for next steps.")
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "\nAll required checks passed. OmaSeal is ready to use.")
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
			name:     "fprintd",
			ok:       false,
			optional: true,
			message: "`fprintd` is not installed.\n" +
				"  - `reveal` will fall through without a fingerprint gate.\n" +
				"  - Install and enable `fprintd` and run `fprintd-enroll` to enable biometric gating.",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	if err := fprintdAvailable(ctx); err != nil {
		return checkResult{
			name:     "fprintd",
			ok:       false,
			optional: true,
			message: err.Error() + "\n" +
				"  - `reveal` will fall through without a fingerprint gate.\n" +
				"  - Start `fprintd.service`, run `fprintd-enroll`, and try again.",
		}
	}

	return checkResult{name: "fprintd", ok: true, message: "fprintd is available and has an enrolled device"}
}

func checkOnePassword() checkResult {
	if !commandExists("op") {
		return checkResult{
			name:     "1password (op)",
			ok:       false,
			optional: true,
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
		name:     "1password (op)",
		ok:       false,
		optional: true,
		message: "`op` CLI is installed but not signed in.\n" +
			"  - Run `op signin` to enable `omaseal resolve` / `import 1password`.",
	}
}

func checkBitwarden() checkResult {
	if !commandExists("bw") {
		return checkResult{
			name:     "bitwarden (bw)",
			ok:       false,
			optional: true,
			message: "`bw` CLI not found.\n" +
				"  - Install the Bitwarden CLI and run `bw login` to enable `omaseal resolve` / `import bitwarden`.",
		}
	}
	if os.Getenv("BW_SESSION") != "" {
		return checkResult{name: "bitwarden (bw)", ok: true, message: "`bw` CLI found and BW_SESSION is set"}
	}
	return checkResult{
		name:     "bitwarden (bw)",
		ok:       false,
		optional: true,
		message: "`bw` CLI found but `BW_SESSION` is not set.\n" +
			"  - Run `bw login`, then use a command-scoped session:\n" +
			"    `BW_SESSION=\"$(bw unlock --raw)\" omaseal resolve <service> <account>` or `omaseal import bitwarden`.",
	}
}

// checkGUIPrompt reports the effective masked graphical prompter so a headless
// `omaseal resolve` has somewhere to ask. It is optional: no prompter only
// limits prompting, never the keyring itself.
func checkGUIPrompt() checkResult {
	if !graphicalSession() {
		return checkResult{
			name:     "gui-prompt",
			ok:       false,
			optional: true,
			message: "No graphical session (WAYLAND_DISPLAY/DISPLAY unset).\n" +
				"  - Headless `omaseal resolve` prompts need a TTY or a graphical session.",
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := selectGUIPrompter(ctx)
	if err != nil {
		return checkResult{
			name:     "gui-prompt",
			ok:       false,
			optional: true,
			message: "No usable masked graphical prompter found.\n" +
				"  - Install `pinentry` with a GUI backend or `zenity` for headless `omaseal resolve` prompts.\n" +
				"  - `OMASEAL_GUI_PROMPT=pinentry|zenity|off` overrides prompter selection.",
		}
	}
	return checkResult{name: "gui-prompt", ok: true, optional: true, message: "graphical prompt via " + p.name()}
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
		name:    "PATH",
		ok:      false,
		message: fmt.Sprintf("`%s` is not on your PATH.\n  - Add `export PATH=\"%s:$PATH\"` to your shell profile.", dir, dir),
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
