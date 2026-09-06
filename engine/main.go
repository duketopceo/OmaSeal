// oma-ring — system keyring CLI for Omarchy.
// Built on gnome-keyring / libsecret via zalando/go-keyring.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const appName = "oma-ring"

func usage() {
	fmt.Fprintln(os.Stderr, `oma-ring — system keyring for Omarchy

Usage:
  oma-ring set <service> <account>          store secret from stdin
  oma-ring get <service> <account>          print stored secret
  oma-ring del <service> <account>          delete stored secret

Examples:
  printf 'sk-or-...' | oma-ring set openrouter default
  oma-ring get openrouter default
  oma-ring del openrouter default
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "set":
		if len(args) != 2 {
			usage()
			os.Exit(1)
		}
		service, account := args[0], args[1]
		secret, err := readSecret()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error reading secret:", err)
			os.Exit(1)
		}
		if secret == "" {
			fmt.Fprintln(os.Stderr, "error: secret cannot be empty")
			os.Exit(1)
		}
		if err := keyring.Set(service, account, secret); err != nil {
			fmt.Fprintln(os.Stderr, "error storing secret:", err)
			os.Exit(1)
		}
		fmt.Println("ok")

	case "get":
		if len(args) != 2 {
			usage()
			os.Exit(1)
		}
		service, account := args[0], args[1]
		secret, err := keyring.Get(service, account)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error retrieving secret:", err)
			os.Exit(1)
		}
		fmt.Print(secret)

	case "del", "delete":
		if len(args) != 2 {
			usage()
			os.Exit(1)
		}
		service, account := args[0], args[1]
		if err := keyring.Delete(service, account); err != nil {
			fmt.Fprintln(os.Stderr, "error deleting secret:", err)
			os.Exit(1)
		}
		fmt.Println("ok")

	case "help", "-h", "--help":
		usage()

	default:
		usage()
		os.Exit(1)
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
