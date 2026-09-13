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
  omaseal list [service] [--json]          list stored secrets
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
  omaseal list
  omaseal list omaseal://browseros/
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
	service, account, err := argCredentials(os.Args[2:])
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
	service, account, err := argCredentials(os.Args[2:])
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

	var positionals []string
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--json" {
			continue
		}
		if strings.HasPrefix(os.Args[i], "-") {
			fmt.Fprintln(os.Stderr, "error: unknown flag:", os.Args[i])
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
	fmt.Fprintln(w, "SERVICE\tACCOUNT\tLABEL")
	for _, it := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\n", it.Service, it.Account, it.Label)
	}
	w.Flush()
}

func handleReveal() {
	service, account, err := argCredentials(os.Args[2:])
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
	service, account, err := argCredentials(os.Args[2:])
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

// handleSelfTest runs a set/get/delete round-trip against the live keyring.
// Agents and users can call it to verify the whole stack end to end.
func handleSelfTest() {
	service := "omaseal-selftest"
	account := fmt.Sprintf("selftest-%d", os.Getpid())
	secret := fmt.Sprintf("omaseal-selftest-%d", time.Now().UnixNano())

	fail := func(step string, err error) {
		printError("selftest "+step+": ", err)
		os.Exit(1)
	}

	if err := Set(service, account, secret); err != nil {
		fail("set", err)
	}
	got, err := Get(service, account)
	if err != nil {
		_ = Delete(service, account)
		fail("get", err)
	}
	if got != secret {
		_ = Delete(service, account)
		fail("compare", fmt.Errorf("round-trip mismatch"))
	}
	if err := Delete(service, account); err != nil {
		fail("delete", err)
	}
	if _, err := Get(service, account); err == nil {
		fail("verify-delete", fmt.Errorf("secret still readable after delete"))
	}
	WriteLog("selftest passed")
	fmt.Println("selftest ok: set/get/delete round-trip passed")
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
