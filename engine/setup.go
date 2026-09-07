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

func runSetup() {
	fmt.Fprintln(os.Stderr, "Running doctor first...")
	results := doctorChecks()
	printDoctorResults(results)
	fmt.Fprintln(os.Stderr, "")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Omarchy / Quickshell ===")
	fmt.Fprintf(os.Stderr, "The plugin is ready. Make sure it is in `~/.config/omarchy/plugins/io.github.duketopceo.omaseal` or installed via AUR.\n")
	fmt.Fprintf(os.Stderr, "Run `omarchy-restart-shell` after enabling.\n")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Agent MCP clients ===")
	if isStdinTTY() {
		reader := bufio.NewReader(os.Stdin)
		for _, agent := range mcpCanonicalAgents {
			fmt.Fprintf(os.Stderr, "Install MCP config for %s? [y/N] ", agent)
			text, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "  could not read response: %v\n", err)
				continue
			}
			if strings.ToLower(strings.TrimSpace(text)) != "y" {
				continue
			}
			path, err := installMCP(agent, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error installing %s: %v\n", agent, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "  installed: %s\n", path)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Run `omaseal mcp install <agent>` for each agent you want to enable.")
		fmt.Fprintln(os.Stderr, "Or run `omaseal mcp install-all` to enable all supported agents.")
		fmt.Fprintln(os.Stderr, "Supported agents: claude, codex, cursor, devin, agy, hermes")
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Shell alias ===")
	fmt.Fprintf(os.Stderr, "Add to `~/.bashrc` or `~/.zshrc` if `~/.local/bin` is not on PATH:\n")

	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot locate the running binary. Run `omaseal doctor` after installation.")
	} else {
		fmt.Fprintf(os.Stderr, "  export PATH=\"%s:$PATH\"\n", filepath.Dir(bin))
	}

	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("fprintd-verify"); err == nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "=== Fingerprint ===")
			fmt.Fprintf(os.Stderr, "Enroll a finger with `fprintd-enroll` to enable biometric reveal.\n")
		}
	}
}
