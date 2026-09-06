package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
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
  omaseal ipc <method> <json-args>         JSON IPC for other plugins

Examples:
  printf 'sk-or-...' | omaseal set openrouter default
  omaseal get openrouter default
  omaseal reveal openrouter default
  omaseal del openrouter default
  omaseal list
  omaseal resolve openrouter default
  omaseal import 1password pace-dev
  omaseal ipc ping '{}'
  omaseal ipc get '{"service":"openrouter","account":"default"}'
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
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
		runMCP()
	case "ipc":
		handleIPC()
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(1)
	}
}

func handleSet() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	service, account := os.Args[2], os.Args[3]
	secret, err := readSecret()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading secret:", err)
		os.Exit(1)
	}
	if secret == "" {
		fmt.Fprintln(os.Stderr, "error: secret cannot be empty")
		os.Exit(1)
	}
	if err := Set(service, account, secret); err != nil {
		fmt.Fprintln(os.Stderr, "error storing secret:", err)
		os.Exit(1)
	}
	fmt.Println("ok")
}

func handleGet() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	service, account := os.Args[2], os.Args[3]
	secret, err := Get(service, account)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error retrieving secret:", err)
		os.Exit(1)
	}
	fmt.Print(secret)
}

func handleDel() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	service, account := os.Args[2], os.Args[3]
	if err := Delete(service, account); err != nil {
		fmt.Fprintln(os.Stderr, "error deleting secret:", err)
		os.Exit(1)
	}
	fmt.Println("ok")
}

func handleList() {
	jsonOut := hasFlag(os.Args, "--json")

	var service string
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] != "--json" {
			service = os.Args[i]
		}
	}

	items, err := List(service)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error listing secrets:", err)
		os.Exit(1)
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
	fmt.Fprintln(w, "SERVICE\tACCOUNT\tLABEL")
	for _, it := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\n", it.Service, it.Account, it.Label)
	}
	w.Flush()
}

func handleReveal() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	if err := FprintdVerify(context.Background(), fmt.Sprintf("reveal %s/%s", os.Args[2], os.Args[3])); err != nil {
		fmt.Fprintln(os.Stderr, "fingerprint gate:", err)
		os.Exit(1)
	}
	secret, err := Get(os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error getting secret:", err)
		os.Exit(1)
	}
	fmt.Print(secret)
}

func handleIPC() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	method := os.Args[2]
	jsonArgs := os.Args[3]
	runIPC(method, jsonArgs)
}

func handleResolve() {
	if len(os.Args) != 4 {
		usage()
		os.Exit(1)
	}
	secret, err := Resolve(os.Args[2], os.Args[3], true, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error resolving secret:", err)
		os.Exit(1)
	}
	fmt.Print(secret)
}

func handleImport() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	source := os.Args[2]
	var err error
	switch source {
	case "1password", "op":
		vault := ""
		if len(os.Args) >= 4 {
			vault = os.Args[3]
		}
		err = ImportOnePassword(vault)
	case "bitwarden", "bw":
		err = ImportBitwarden()
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error importing:", err)
		os.Exit(1)
	}
	fmt.Println("ok")
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
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(line, "\n"), nil
}

func isStdinTTY() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}
