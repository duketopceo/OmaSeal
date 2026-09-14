package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

type RulePolicy string

const (
	PolicyAllow RulePolicy = "ALLOW"
	PolicyDeny  RulePolicy = "DENY"
	PolicyAsk   RulePolicy = "ASK"
)

// ManifestRule is a single robots.txt-style rule for AI agents.
type ManifestRule struct {
	Policy      RulePolicy `json:"policy"`
	Pattern     string     `json:"pattern"`
	Description string     `json:"description"`
}

// Manifest represents the AI access policy manifest.
type Manifest struct {
	Path  string         `json:"path"`
	Rules []ManifestRule `json:"rules"`
}

// ManifestPath returns the path to the ai-manifest.txt file. Reads must not
// create the config dir as a side effect — manifestDir makes it only on write.
func ManifestPath() (string, error) {
	dir := omasealConfigDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve config dir")
	}
	return filepath.Join(dir, "ai-manifest.txt"), nil
}

// manifestDir creates the config dir for manifest writes.
func manifestDir() (string, error) {
	path, err := ManifestPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// LoadManifest parses the ai-manifest.txt file.
func LoadManifest() (*Manifest, error) {
	path, err := ManifestPath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Path: path, Rules: nil}, nil
		}
		return nil, err
	}
	defer f.Close()

	var rules []ManifestRule
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		policyStr := strings.ToUpper(parts[0])
		var policy RulePolicy
		switch policyStr {
		case "ALLOW":
			policy = PolicyAllow
		case "DENY":
			policy = PolicyDeny
		case "ASK":
			policy = PolicyAsk
		default:
			continue
		}

		pattern := parts[1]
		desc := ""

		// Check for " - " separator
		hyphenIdx := strings.Index(line, " - ")
		if hyphenIdx != -1 {
			desc = strings.TrimSpace(line[hyphenIdx+3:])
		} else if len(parts) > 2 {
			desc = strings.Join(parts[2:], " ")
			desc = strings.TrimPrefix(desc, "- ")
		}

		rules = append(rules, ManifestRule{
			Policy:      policy,
			Pattern:     pattern,
			Description: desc,
		})
	}

	return &Manifest{Path: path, Rules: rules}, scanner.Err()
}

// CheckPolicy determines whether an AI agent is ALLOW, DENY, or ASK for a secret.
func (m *Manifest) CheckPolicy(service, account string) (RulePolicy, string) {
	target := strings.ToLower(service) + "/" + strings.ToLower(account)
	serviceWildcard := strings.ToLower(service) + "/*"

	// 1. Exact match
	for _, r := range m.Rules {
		if strings.EqualFold(r.Pattern, target) {
			return r.Policy, r.Description
		}
	}

	// 2. Service wildcard
	for _, r := range m.Rules {
		if strings.EqualFold(r.Pattern, serviceWildcard) {
			return r.Policy, r.Description
		}
	}

	// 3. Catch-all wildcard
	for _, r := range m.Rules {
		if r.Pattern == "*" || r.Pattern == "*/*" {
			return r.Policy, r.Description
		}
	}

	// Default policy for unspecified items is ASK
	return PolicyAsk, "Unspecified in AI manifest; prompt confirmation required"
}

// Scaffolding helper for init. First substring match wins, so the table is
// ordered most-permissive-guess first; anything unmatched defaults to ASK.
var defaultPolicyTable = []struct {
	substrs []string
	policy  RulePolicy
	desc    string
}{
	{[]string{"openrouter", "perplexity", "kurultai", "claude", "codex", "devin", "openai", "anthropic", "gemini", "groq", "composio"}, PolicyAllow, "%s agent inference and tool execution"},
	{[]string{"hetzner", "tailscale", "github", "cloudflare", "n8n", "shippedit", "aws", "docker", "ssh"}, PolicyAsk, "%s infrastructure and automation access"},
	{[]string{"bank", "schwab", "google", "apple", "uber", "hulu", "starbucks", "termius", "experian", "cleanbrowsing"}, PolicyDeny, "Personal %s credentials (restricted)"},
}

func defaultPolicyFor(service string) (RulePolicy, string) {
	s := strings.ToLower(service)
	for _, row := range defaultPolicyTable {
		for _, sub := range row.substrs {
			if strings.Contains(s, sub) {
				return row.policy, fmt.Sprintf(row.desc, service)
			}
		}
	}
	return PolicyAsk, fmt.Sprintf("%s credential", service)
}

// GenerateDefaultManifest builds a starter robots.txt manifest from existing items.
func GenerateDefaultManifest(items []Item) string {
	var b strings.Builder
	b.WriteString("# OmaSeal AI Agent Access Manifest (robots.txt format)\n")
	b.WriteString("# Governs AI agent tool access and automated keyring usage.\n")
	b.WriteString("#\n")
	b.WriteString("# Policies:\n")
	b.WriteString("#   ALLOW <pattern> - <description>  (Agent may read without biometric/user gate)\n")
	b.WriteString("#   ASK   <pattern> - <description>  (Agent requires user confirmation before access)\n")
	b.WriteString("#   DENY  <pattern> - <description>  (Agent access strictly forbidden)\n\n")

	// Group items into Allow, Ask, Deny
	var allows, asks, denies []string
	seen := make(map[string]bool)

	for _, it := range items {
		service, account := sanitizeField(it.Service), sanitizeField(it.Account)
		// Names containing whitespace would corrupt the space-separated
		// rule format — skip them rather than write an unparseable line.
		if service == "" || account == "" || strings.ContainsAny(service+account, " \t") {
			continue
		}
		target := fmt.Sprintf("%s/%s", service, account)
		if seen[target] {
			continue
		}
		seen[target] = true

		policy, desc := defaultPolicyFor(service)
		line := fmt.Sprintf("%-6s %-35s - %s\n", policy, target, sanitizeField(desc))

		switch policy {
		case PolicyAllow:
			allows = append(allows, line)
		case PolicyAsk:
			asks = append(asks, line)
		case PolicyDeny:
			denies = append(denies, line)
		}
	}

	b.WriteString("# ── Allowed AI & Agent Tools ──\n")
	for _, l := range allows {
		b.WriteString(l)
	}

	b.WriteString("\n# ── Infrastructure & Automation (Confirmation Required) ──\n")
	for _, l := range asks {
		b.WriteString(l)
	}

	b.WriteString("\n# ── Restricted & Personal Credentials ──\n")
	for _, l := range denies {
		b.WriteString(l)
	}

	b.WriteString("\n# Fallback policy for any unlisted credentials\n")
	b.WriteString("ASK    *                                   - Unspecified credentials require confirmation\n")

	return b.String()
}

// handleManifest implements `omaseal manifest [show|init|check|path]`.
// The manifest governs agent (MCP) access per secret: DENY refuses the call,
// ASK requires an unlocked session even in open mode, ALLOW proceeds.
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
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.Policy, sanitizeField(r.Pattern), sanitizeField(r.Description))
		}
		w.Flush()

	case "init":
		path, err := manifestDir()
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
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			printError("writing manifest: ", err)
			os.Exit(1)
		}
		var allow, ask, deny int
		for _, it := range items {
			p, _ := defaultPolicyFor(sanitizeField(it.Service))
			switch p {
			case PolicyAllow:
				allow++
			case PolicyAsk:
				ask++
			case PolicyDeny:
				deny++
			}
		}
		fmt.Printf("Initialized AI agent manifest at %s (%d ALLOW / %d ASK / %d DENY rules).\n", path, allow, ask, deny)
		fmt.Println("Review and edit the file — it takes effect on the next agent call.")

	case "check":
		service, account, err := argCredentialsLoose(extraArgs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Usage: omaseal manifest check <service> <account>")
			os.Exit(1)
		}
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
		fmt.Printf("%s: %s/%s (%s)\n", policy, sanitizeField(service), sanitizeField(account), sanitizeField(desc))

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
