package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// runSetup is the guided onboarding path: verify the environment, show which
// agents are detected and already wired, offer to install MCP config for the
// rest, and set a primary agent. `omaseal setup --yes` skips the prompts so
// installers and dotfile scripts can run it unattended.
func runSetup() {
	yes := hasFlag(os.Args, "--yes") || hasFlag(os.Args, "-y")

	fmt.Fprintln(os.Stderr, "Running doctor first...")
	results := doctorChecks()
	printDoctorResults(results)
	fmt.Fprintln(os.Stderr)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Agents ===")
	rows := agentStatusRows()
	detected := []mcpAgentStatus{}
	for _, r := range rows {
		fmt.Fprintf(os.Stderr, "  %-10s detected:%-4s installed:%-4s %s\n",
			r.Name, yesNo(r.Detected), yesNo(r.Installed), r.Config)
		if r.Detected {
			detected = append(detected, r)
		}
	}
	if len(detected) == 0 {
		fmt.Fprintln(os.Stderr, "  no supported agents detected yet")
	}

	p, _ := loadAgentPolicy()
	if p.PrimaryAgent != "" {
		fmt.Fprintf(os.Stderr, "  primary agent: %s\n", p.PrimaryAgent)
	}
	if len(p.Agents) > 0 {
		fmt.Fprintf(os.Stderr, "  defaults:      %s\n", strings.Join(p.Agents, ", "))
	}

	if isStdinTTY() || yes {
		maybeInstallDetected(detected, yes)
		maybeSetPrimary(rows, yes)
	} else {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Run `omaseal setup --yes` to auto-wire every detected agent,")
		fmt.Fprintln(os.Stderr, "or `omaseal mcp install-detected` / `omaseal mcp install <agent>`.")
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Omarchy / Quickshell ===")
	fmt.Fprintln(os.Stderr, "The plugin is ready. Make sure it is in `~/.config/omarchy/plugins/io.github.duketopceo.omaseal` or installed via AUR.")
	fmt.Fprintln(os.Stderr, "Run `omarchy-restart-shell` after enabling.")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Shell PATH ===")
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot locate the running binary. Run `omaseal doctor` after installation.")
	} else {
		dir := filepath.Dir(bin)
		onPath := false
		for _, p := range filepath.SplitList(os.Getenv("PATH")) {
			if p == dir {
				onPath = true
				break
			}
		}
		if onPath {
			fmt.Fprintf(os.Stderr, "%s is on PATH.\n", dir)
		} else {
			fmt.Fprintf(os.Stderr, "Add to `~/.bashrc` or `~/.zshrc`:\n  export PATH=\"%s:$PATH\"\n", dir)
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Verify ===")
	fmt.Fprintln(os.Stderr, "Run `omaseal selftest` for a keyring round-trip check.")
	fmt.Fprintln(os.Stderr, "Agents pick up OmaSeal on their next start; the MCP server tells them to use it.")

	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("fprintd-verify"); err == nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "=== Fingerprint ===")
			fmt.Fprintln(os.Stderr, "Enroll a finger with `fprintd-enroll` to enable biometric reveal.")
		}
	}
}

// maybeInstallDetected wires OmaSeal into every detected agent that does not
// already have it. On a TTY it asks once ([Y/n]); with --yes it just does it.
func maybeInstallDetected(detected []mcpAgentStatus, yes bool) {
	todo := []string{}
	for _, r := range detected {
		if !r.Installed {
			todo = append(todo, r.Name)
		}
	}
	if len(todo) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr)
	if !yes {
		fmt.Fprintf(os.Stderr, "Install OmaSeal MCP for detected agents (%s)? [Y/n] ", strings.Join(todo, ", "))
		reader := bufio.NewReader(os.Stdin)
		text, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "  could not read response: %v\n", err)
			return
		}
		answer := strings.ToLower(strings.TrimSpace(text))
		if answer != "" && answer != "y" && answer != "yes" {
			return
		}
	}
	installForAgents(todo, "")
}

// maybeSetPrimary suggests a primary agent when none is configured. On a TTY
// it asks once; with --yes it picks the first detected agent automatically.
func maybeSetPrimary(rows []mcpAgentStatus, yes bool) {
	p, _ := loadAgentPolicy()
	if p.PrimaryAgent != "" {
		return
	}
	var firstDetected string
	for _, r := range rows {
		if r.Detected {
			firstDetected = r.Name
			break
		}
	}
	if firstDetected == "" {
		return
	}

	fmt.Fprintln(os.Stderr)
	if !yes && isStdinTTY() {
		fmt.Fprintf(os.Stderr, "Set %s as your primary agent? [Y/n] ", firstDetected)
		reader := bufio.NewReader(os.Stdin)
		text, err := reader.ReadString('\n')
		if err != nil || (strings.ToLower(strings.TrimSpace(text)) != "" &&
			strings.ToLower(strings.TrimSpace(text)) != "y" &&
			strings.ToLower(strings.TrimSpace(text)) != "yes") {
			return
		}
	}
	if err := SetPrimaryAgent(firstDetected); err != nil {
		fmt.Fprintf(os.Stderr, "  could not set primary agent: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "  primary agent: %s\n", firstDetected)
}
