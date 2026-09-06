package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func runSetup() {
	fmt.Fprintln(os.Stderr, "Running doctor first...")
	results := doctorChecks()
	printDoctorResults(results)
	fmt.Fprintln(os.Stderr, "")

	bin, err := os.Executable()
	if err != nil {
		bin = "omaseal"
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Omarchy / Quickshell ===")
	fmt.Fprintf(os.Stderr, "The plugin is ready. Make sure it is in `~/.config/omarchy/plugins/io.github.duketopceo.omaseal` or installed via AUR.\n")
	fmt.Fprintf(os.Stderr, "Run `omarchy-restart-shell` after enabling.\n")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== BrowserOS / Omarchy apps ===")
	fmt.Fprintf(os.Stderr, "BrowserOS resolves `omaseal://browseros/<provider>/<field>` references.\n")
	fmt.Fprintf(os.Stderr, "Store provider API keys with the \"Store credentials in OmaSeal\" checkbox.\n")

	mcp := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"omaseal": map[string]interface{}{
				"command": bin,
				"args":    []string{"mcp"},
			},
		},
	}
	b, _ := json.MarshalIndent(mcp, "", "  ")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Claude / Codex / MCP clients ===")
	fmt.Fprintf(os.Stderr, "Add this to your MCP config (e.g. `~/.claude/mcp.json` or `~/.codex/mcp.json`):\n%s\n", string(b))

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "=== Shell alias ===")
	fmt.Fprintf(os.Stderr, "Add to `~/.bashrc` or `~/.zshrc` if `~/.local/bin` is not on PATH:\n")
	fmt.Fprintf(os.Stderr, "  export PATH=\"%s:$PATH\"\n", filepath.Dir(bin))

	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("fprintd-verify"); err == nil {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "=== Fingerprint ===")
			fmt.Fprintf(os.Stderr, "Enroll a finger with `fprintd-enroll` to enable biometric reveal.\n")
		}
	}
}
