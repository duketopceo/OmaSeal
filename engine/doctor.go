package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	emitCheckJSON(doctorChecks(), "omaseal setup")
}

// emitCheckJSON renders check results in the shared ping/doctor JSON shape.
func emitCheckJSON(results []checkResult, help string) {
	checks, ok := toPingChecks(results)
	res := pingResult{
		Version: version,
		Commit:  commit,
		Target:  target(),
		Ok:      ok,
		Checks:  checks,
		Help:    help,
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error marshaling check result:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

// doctorChecks runs every check concurrently — each is an independent probe
// and several spawn subprocesses with multi-second timeouts, so serial runs
// cost the sum of all timeouts instead of the slowest one.
func doctorChecks() []checkResult {
	checks := []func() checkResult{
		checkBinary,
		checkSecretService,
		checkKeyringEncryption,
		checkFprintd,
		checkOnePassword,
		checkBitwarden,
		checkGUIPrompt,
		checkPath,
	}
	results := make([]checkResult, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c()
		}()
	}
	wg.Wait()
	return results
}

// toPingChecks converts check results to the JSON shape and reports whether
// all required (non-optional) checks passed.
func toPingChecks(results []checkResult) ([]pingCheck, bool) {
	checks := make([]pingCheck, len(results))
	ok := true
	for i, r := range results {
		checks[i] = pingCheck{Name: r.name, Ok: r.ok, Optional: r.optional, Message: r.message}
		if !r.ok && !r.optional {
			ok = false
		}
	}
	return checks, ok
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

// keyringsDir returns the directory holding gnome-keyring *.keyring files.
func keyringsDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "keyrings")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "keyrings")
}

// countPlainSecrets scans gnome-keyring .keyring content for `secret=` values
// stored as printable text. A password-protected keyring writes the secret as
// an encrypted binary blob; an empty-password keyring (common on autologin
// setups where no password reaches PAM) writes the literal secret value.
// Returns (plaintext count, total secrets seen).
func countPlainSecrets(data []byte) (plain, total int) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("secret=")) {
			continue
		}
		v := bytes.TrimSpace(line[len("secret="):])
		if len(v) == 0 {
			continue
		}
		total++
		if bytes.IndexFunc(v, func(r rune) bool { return r < 0x20 || r > 0x7e }) < 0 {
			plain++
		}
	}
	return plain, total
}

// checkKeyringEncryption verifies secrets are not stored plaintext on disk.
// OmaSeal enforces its manifest policy on the Secret Service API path; a
// plaintext keyring file bypasses that policy entirely for anyone who can
// read the file, so this surfaces as a loud warning even though functionally
// everything still works.
func checkKeyringEncryption() checkResult {
	dir := keyringsDir()
	if dir == "" {
		return checkResult{name: "keyring-encryption", ok: true, optional: true,
			message: "cannot determine keyring directory"}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.keyring"))
	if err != nil || len(files) == 0 {
		return checkResult{name: "keyring-encryption", ok: true, optional: true,
			message: "no keyring files yet — nothing stored"}
	}
	var plain, total int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		p, t := countPlainSecrets(data)
		plain += p
		total += t
	}
	if plain == 0 {
		return checkResult{name: "keyring-encryption", ok: true,
			message: "keyring secrets are encrypted at rest"}
	}
	return checkResult{
		name:     "keyring-encryption",
		ok:       false,
		optional: true,
		message: fmt.Sprintf("%d/%d stored secrets are PLAINTEXT on disk (%s)\n", plain, total, dir) +
			"  - The keyring has an empty password — common on autologin setups where no password reaches PAM.\n" +
			"  - Fix: `omaseal keyring migrate` (graphical session required — the daemon prompts for the new password).\n" +
			"  - Use your login password so PAM auto-unlocks; disable autologin or it will prompt once per session.\n" +
			"  - Note: full-disk encryption still protects the file when powered off; this gap is for local readers while running.",
	}
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
	if errors.Is(err, errGUIDisabled) {
		return checkResult{name: "gui-prompt", ok: true, optional: true, message: "disabled by OMASEAL_GUI_PROMPT=off"}
	}
	if err != nil {
		return checkResult{
			name:     "gui-prompt",
			ok:       false,
			optional: true,
			message: "No usable masked graphical prompter: " + err.Error() + "\n" +
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
	if dirOnPATH(dir) {
		return checkResult{name: "PATH", ok: true, message: fmt.Sprintf("`%s` is on PATH", dir)}
	}
	return checkResult{
		name:    "PATH",
		ok:      false,
		message: fmt.Sprintf("`%s` is not on your PATH.\n  - Add `export PATH=\"%s:$PATH\"` to your shell profile.", dir, dir),
	}
}

// dirOnPATH reports whether dir appears verbatim in PATH.
func dirOnPATH(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return true
		}
	}
	return false
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
