package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
)

const appName = "omaseal"

func usage() {
	fmt.Fprint(os.Stderr, `omaseal — system keyring for Omarchy

Usage:
  omaseal set <service> <account>          store secret from stdin
  omaseal get <service> <account>          print stored secret
  omaseal reveal <service> <account>       print secret after fprintd gate
  omaseal del <service> <account>          delete stored secret
  omaseal list [service] [--json] [--sort=used]
                                           list stored secrets (optionally sorted by hits)
  omaseal stats [--json]                   display access analytics & usage leaderboard
  omaseal manifest [show|init|check|path]  AI agent access policy (robots.txt format)
  omaseal resolve <service> <account>      resolve + cache from keyring/op/bw/prompt
  omaseal import 1password [vault]         import all 1Password items
  omaseal import bitwarden                 import all Bitwarden items
  omaseal mcp                              start MCP stdio server
  omaseal mcp install <agent> [--dir .]    write mcp config for one agent
  omaseal mcp install-detected             wire every detected + assigned agent
  omaseal mcp install-all                  write mcp config for every known agent
  omaseal mcp status [--json]              show detected/installed agent configs
  omaseal ipc <method> <json-args>         JSON IPC for other plugins
  omaseal ping                             health check (json with --json)
  omaseal selftest                         keyring round-trip test
  omaseal doctor                           check the environment and dependencies
  omaseal logs [n]                         show recent non-secret log lines
  omaseal setup [--yes]                    onboarding: doctor + agent wiring
  omaseal agent mode <open|ask|lock> [min] set agent/MCP trust mode
  omaseal agent unlock                     biometric unlock for ask mode
  omaseal agent lock                       revoke agent session
  omaseal agent status [--json]            show agent policy and session
  omaseal agent keepalive [on|off]         session renews on activity (ask mode)
  omaseal agent primary <name>             set your main agent
  omaseal agent defaults [names...|--clear] set assigned default agents

References:
  Everywhere <service> <account> is accepted, a single omaseal://<service>/<account>
  reference works too; the account may contain '/' (e.g. omaseal://browseros/
  openrouter-work/apiKey). 'omaseal list' accepts omaseal://<service>[/].

Examples:
  omaseal set openrouter default < secret.txt
  omaseal get openrouter default
  omaseal get omaseal://openrouter/default
  omaseal reveal openrouter default
  omaseal del openrouter default
  omaseal list --sort=used
  omaseal list omaseal://browseros/
  omaseal stats
  omaseal manifest
  omaseal manifest check openrouter default
  omaseal resolve openrouter default
  omaseal resolve omaseal://browseros/openrouter-work/apiKey
  omaseal import 1password pace-dev
  omaseal ipc ping '{}'
  omaseal ipc get '{"service":"openrouter","account":"default"}'
`)
}

func main() {
	SetLogOutput()

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	WriteLog("omaseal %s: command %s", version, cmd)

	switch cmd {
	case "version", "--version", "-v":
		printVersion()
		return
	case "set":
		handleSet()
	case "get":
		handleGet()
	case "reveal":
		handleReveal()
	case "del", "delete":
		handleDel()
	case "list":
		handleList()
	case "stats", "analytics":
		handleStats()
	case "manifest", "robots":
		handleManifest()
	case "resolve":
		handleResolve()
	case "import":
		handleImport()
	case "mcp":
		if len(os.Args) >= 3 && os.Args[2] == "install" {
			handleMCPInstall()
		} else if len(os.Args) >= 3 && os.Args[2] == "install-all" {
			handleMCPInstallAll()
		} else if len(os.Args) >= 3 && os.Args[2] == "install-detected" {
			handleMCPInstallDetected()
		} else if len(os.Args) >= 3 && os.Args[2] == "status" {
			handleMCPStatus()
		} else {
			runMCP()
		}
	case "ipc":
		handleIPC()
	case "ping":
		handlePing()
	case "selftest":
		handleSelfTest()
	case "doctor":
		handleDoctor()
	case "logs":
		handleLogs()
	case "setup":
		runSetup()
	case "agent":
		handleAgent()
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(1)
	}
}

func handleSet() {
	service, account, err := argCredentials(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	secret, err := readSecret()
	if err != nil {
		printError("reading secret: ", err)
		os.Exit(1)
	}
	if secret == "" {
		fmt.Fprintln(os.Stderr, "error: secret cannot be empty")
		os.Exit(1)
	}
	if err := Set(service, account, secret); err != nil {
		printError("storing secret: ", err)
		os.Exit(1)
	}
	WriteLog("set %s/%s", service, account)
	fmt.Println("ok")
}

func handleGet() {
	service, account, err := argCredentialsLoose(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	secret, err := Get(service, account)
	if err != nil {
		printError("retrieving secret: ", err)
		os.Exit(1)
	}
	WriteLog("get %s/%s", service, account)
	fmt.Print(secret)
}

func handleDel() {
	service, account, err := argCredentialsLoose(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	if err := Delete(service, account); err != nil {
		printError("deleting secret: ", err)
		os.Exit(1)
	}
	WriteLog("del %s/%s", service, account)
	fmt.Println("ok")
}

func handleList() {
	jsonOut := hasFlag(os.Args, "--json")
	sortByUsed := hasFlag(os.Args, "--sort=used") || hasFlag(os.Args, "--sort-by=used") || hasFlag(os.Args, "--hits")

	var positionals []string
	for i := 2; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--json" || arg == "--sort=used" || arg == "--sort-by=used" || arg == "--hits" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintln(os.Stderr, "error: unknown flag:", arg)
			os.Exit(1)
		}
		positionals = append(positionals, os.Args[i])
	}
	service, err := argService(positionals)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	items, err := List(service)
	if err != nil {
		printError("listing secrets: ", err)
		os.Exit(1)
	}
	WriteLog("list: %d secrets", len(items))

	stats, _ := ParseAccessLogs()
	if stats != nil {
		items = EnrichItemsWithStats(items, stats)
	}
	if sortByUsed {
		SortItemsByUsage(items)
	}

	if jsonOut {
		b, _ := json.Marshal(items)
		fmt.Println(string(b))
		return
	}

	if len(items) == 0 {
		fmt.Println("No secrets stored.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if sortByUsed {
		fmt.Fprintln(w, "SERVICE\tACCOUNT\tHITS\tLAST ACCESSED\tLABEL")
		for _, it := range items {
			last := "-"
			if it.LastAccessed != nil {
				last = it.LastAccessed.Format("2006-01-02 15:04:05")
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", it.Service, it.Account, it.AccessCount, last, it.Label)
		}
	} else {
		fmt.Fprintln(w, "SERVICE\tACCOUNT\tLABEL")
		for _, it := range items {
			fmt.Fprintf(w, "%s\t%s\t%s\n", it.Service, it.Account, it.Label)
		}
	}
	w.Flush()
}

func handleStats() {
	jsonOut := hasFlag(os.Args, "--json")
	report, err := GetAnalyticsReport(50)
	if err != nil {
		printError("generating analytics: ", err)
		os.Exit(1)
	}

	if jsonOut {
		b, _ := json.Marshal(report)
		fmt.Println(string(b))
		return
	}

	fmt.Printf("OmaSeal Keyring Usage Analytics\n")
	fmt.Printf("Total Secret Accesses: %d\n", report.TotalAccesses)
	fmt.Printf("Unique Secrets Accessed: %d\n\n", report.UniqueSecrets)

	if len(report.TopSecrets) == 0 {
		fmt.Println("No secret access activity recorded yet.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "RANK\tHITS\tSERVICE\tACCOUNT\tLAST ACCESSED")
	for i, stat := range report.TopSecrets {
		last := stat.LastAccessed.Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, "#%d\t%d\t%s\t%s\t%s\n", i+1, stat.Count, stat.Service, stat.Account, last)
	}
	w.Flush()
}

func handleManifest() {
	jsonOut := hasFlag(os.Args, "--json")

	var subcmd string
	var extraArgs []string
	for _, a := range os.Args[2:] {
		if a == "--json" || a == "--force" || a == "-f" {
			continue
		}
		if subcmd == "" {
			subcmd = a
		} else {
			extraArgs = append(extraArgs, a)
		}
	}
	if subcmd == "" {
		subcmd = "show"
	}

	switch subcmd {
	case "show":
		m, err := LoadManifest()
		if err != nil {
			printError("loading manifest: ", err)
			os.Exit(1)
		}
		if jsonOut {
			b, _ := json.Marshal(m)
			fmt.Println(string(b))
			return
		}
		if len(m.Rules) == 0 {
			fmt.Println("No AI manifest found at", m.Path)
			fmt.Println("Run 'omaseal manifest init' to generate one from existing secrets.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "POLICY\tPATTERN\tDESCRIPTION")
		for _, r := range m.Rules {
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.Policy, r.Pattern, r.Description)
		}
		w.Flush()

	case "init":
		path, err := ManifestPath()
		if err != nil {
			printError("resolving manifest path: ", err)
			os.Exit(1)
		}
		force := hasFlag(os.Args, "--force") || hasFlag(os.Args, "-f")
		if !force {
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(os.Stderr, "Manifest already exists at %s (use --force to overwrite)\n", path)
				os.Exit(1)
			}
		}
		items, err := List("")
		if err != nil {
			printError("listing secrets for manifest: ", err)
			os.Exit(1)
		}
		content := GenerateDefaultManifest(items)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			printError("writing manifest: ", err)
			os.Exit(1)
		}
		fmt.Printf("Initialized AI agent manifest at %s with %d secret rules.\n", path, len(items))

	case "check":
		if len(extraArgs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: omaseal manifest check <service> <account>")
			os.Exit(1)
		}
		service, account := extraArgs[0], extraArgs[1]
		m, err := LoadManifest()
		if err != nil {
			printError("loading manifest: ", err)
			os.Exit(1)
		}
		policy, desc := m.CheckPolicy(service, account)
		if jsonOut {
			b, _ := json.Marshal(map[string]string{
				"service":     service,
				"account":     account,
				"policy":      string(policy),
				"description": desc,
			})
			fmt.Println(string(b))
			return
		}
		fmt.Printf("%s: %s/%s (%s)\n", policy, service, account, desc)

	case "path":
		p, err := ManifestPath()
		if err != nil {
			printError("getting manifest path: ", err)
			os.Exit(1)
		}
		fmt.Println(p)

	default:
		fmt.Fprintln(os.Stderr, "Usage: omaseal manifest [show|init|check|path] [--json]")
		os.Exit(1)
	}
}

func handleReveal() {
	service, account, err := argCredentialsLoose(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	if err := FprintdVerify(context.Background(), fmt.Sprintf("reveal %s/%s", service, account)); err != nil {
		printError("fingerprint gate: ", err)
		os.Exit(1)
	}
	secret, err := Get(service, account)
	if err != nil {
		printError("getting secret: ", err)
		os.Exit(1)
	}
	WriteLog("reveal %s/%s", service, account)
	fmt.Print(secret)
}

func handleIPC() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	method := os.Args[2]
	jsonArgs := os.Args[3]
	WriteLog("ipc %s", method)
	runIPC(method, jsonArgs)
}

func handleResolve() {
	service, account, err := argCredentialsLoose(os.Args[2:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(1)
	}
	secret, err := Resolve(context.Background(), service, account, true, true)
	if err != nil {
		printError("resolving secret: ", err)
		os.Exit(1)
	}
	WriteLog("resolve %s/%s", service, account)
	fmt.Print(secret)
}

func handleImport() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	source := os.Args[2]
	var err error
	ctx := context.Background()
	switch source {
	case "1password", "op":
		vault := ""
		if len(os.Args) >= 4 {
			vault = os.Args[3]
		}
		err = ImportOnePassword(ctx, vault)
	case "bitwarden", "bw":
		err = ImportBitwarden(ctx)
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		printError("importing: ", err)
		os.Exit(1)
	}
	WriteLog("import %s", source)
	fmt.Println("ok")
}

func handleLogs() {
	n := 50
	jsonOut := false
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--json" {
			jsonOut = true
			continue
		}
		if v, err := strconv.Atoi(os.Args[i]); err == nil && v > 0 {
			n = v
		}
	}
	if jsonOut {
		lines, err := ReadLogJSON(n)
		if err != nil {
			printError("reading log: ", err)
			os.Exit(1)
		}
		b, _ := json.Marshal(lines)
		fmt.Println(string(b))
		return
	}
	lines, err := ReadLog(n)
	if err != nil {
		printError("reading log: ", err)
		os.Exit(1)
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

// selftestRoundTrip runs a set/get/delete round-trip against the live keyring.
func selftestRoundTrip() error {
	service := "omaseal-selftest"
	account := fmt.Sprintf("selftest-%d", os.Getpid())
	secret := fmt.Sprintf("omaseal-selftest-%d", time.Now().UnixNano())

	if err := Set(service, account, secret); err != nil {
		return fmt.Errorf("set: %w", err)
	}
	got, err := Get(service, account)
	if err != nil {
		_ = Delete(service, account)
		return fmt.Errorf("get: %w", err)
	}
	if got != secret {
		_ = Delete(service, account)
		return fmt.Errorf("compare: round-trip mismatch")
	}
	if err := Delete(service, account); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if _, err := Get(service, account); err == nil {
		return fmt.Errorf("verify-delete: secret still readable after delete")
	}
	WriteLog("selftest passed")
	return nil
}

// handleSelfTest runs a set/get/delete round-trip against the live keyring.
// Agents and users can call it to verify the whole stack end to end.
func handleSelfTest() {
	if err := selftestRoundTrip(); err != nil {
		printError("selftest ", err)
		os.Exit(1)
	}
	fmt.Println("selftest ok: set/get/delete round-trip passed")
}

// readSecretDeadline reads stdin like readSecret but gives up after d, so
// callers that cannot see their caller's stdin (IPC) fail instead of hanging
// on a held-open pipe.
func readSecretDeadline(d time.Duration) (string, error) {
	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(os.Stdin)
		ch <- result{strings.TrimSuffix(string(b), "\n"), err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-time.After(d):
		return "", fmt.Errorf("timed out waiting for the secret on stdin")
	}
}

// readSecret reads a secret from stdin without a trailing newline.
func readSecret() (string, error) {
	if !isStdinTTY() {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(b), "\n"), nil
	}

	fmt.Fprint(os.Stderr, "Enter secret: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // ReadPassword does not echo the newline
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isStdinTTY() bool {
	return isTerminal(os.Stdin)
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func hasFlag(args []string, name string) bool {
	for _, a := range args[1:] {
		if a == name {
			return true
		}
	}
	return false
}
