package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// ManifestPath returns the path to the ai-manifest.txt file.
func ManifestPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "omaseal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "ai-manifest.txt"), nil
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

// Scaffolding helper for init
func defaultPolicyFor(service string) (RulePolicy, string) {
	s := strings.ToLower(service)

	// AI & Agent keys
	aiServices := []string{"openrouter", "perplexity", "kurultai", "claude", "codex", "devin", "openai", "anthropic", "gemini", "groq", "composio"}
	for _, a := range aiServices {
		if strings.Contains(s, a) {
			return PolicyAllow, fmt.Sprintf("%s agent inference and tool execution", service)
		}
	}

	// Infra & Developer tools
	infraServices := []string{"hetzner", "tailscale", "github", "cloudflare", "n8n", "shippedit", "aws", "docker", "ssh"}
	for _, a := range infraServices {
		if strings.Contains(s, a) {
			return PolicyAsk, fmt.Sprintf("%s infrastructure and automation access", service)
		}
	}

	// Personal & sensitive
	personalServices := []string{"bank", "schwab", "google", "apple", "uber", "hulu", "starbucks", "termius", "experian", "cleanbrowsing", "cleanbrowsing"}
	for _, a := range personalServices {
		if strings.Contains(s, a) {
			return PolicyDeny, fmt.Sprintf("Personal %s credentials (restricted)", service)
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
		target := fmt.Sprintf("%s/%s", it.Service, it.Account)
		if seen[target] {
			continue
		}
		seen[target] = true

		policy, desc := defaultPolicyFor(it.Service)
		line := fmt.Sprintf("%-6s %-35s - %s\n", policy, target, desc)

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
