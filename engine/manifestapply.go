package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// policyRank orders restrictiveness so "expansion" is a mechanical comparison:
// moving a target up this ladder grants an agent more access.
func policyRank(p RulePolicy) int {
	switch p {
	case PolicyDeny:
		return 0
	case PolicyAsk:
		return 1
	case PolicyAllow:
		return 2
	}
	return 1 // unknown/fallback behaves like ASK
}

// governsJevCredential reports whether a pattern would govern openrouter/* —
// the Jev companion's own credential path. apply hard-rejects these regardless
// of confirmation so the advisor can never move its own leash, directly or via
// a wildcard that covers it.
func governsJevCredential(pattern string) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "*" || p == "*/*" {
		return true // covers openrouter/* among everything else
	}
	return p == "openrouter" || strings.HasPrefix(p, "openrouter/")
}

// isExpansionChange recomputes whether a change grants more access — the
// proposal's expands_access flag is a display hint and is never trusted.
func isExpansionChange(m *Manifest, c ProposalChange) bool {
	switch c.Action {
	case "remove":
		// Removing a DENY lifts a hard prohibition.
		for _, r := range m.Rules {
			if strings.EqualFold(r.Pattern, c.Pattern) {
				return r.Policy == PolicyDeny
			}
		}
		return false
	case "add", "set":
		newRank := policyRank(RulePolicy(strings.ToUpper(c.Policy)))
		oldRank := policyRank(PolicyAsk) // fallback when no same-pattern rule exists
		for _, r := range m.Rules {
			if strings.EqualFold(r.Pattern, c.Pattern) {
				oldRank = policyRank(r.Policy)
				break
			}
		}
		// Rank increase covers every expansion shape: ASK→ALLOW, DENY→anything,
		// and a new ALLOW wildcard (fallback oldRank=ASK < ALLOW).
		return newRank > oldRank
	}
	return false
}

// applyChanges rewrites the manifest file's rule lines surgically — comments,
// grouping headers, and blank lines are preserved. Rule order does not affect
// CheckPolicy (exact → wildcard → catch-all passes), so additions append at EOF.
func applyChanges(content string, changes []ProposalChange) string {
	lines := strings.Split(content, "\n")
	for _, c := range changes {
		switch c.Action {
		case "remove":
			for i, ln := range lines {
				f := strings.Fields(ln)
				if len(f) >= 2 && !strings.HasPrefix(ln, "#") && strings.EqualFold(f[1], c.Pattern) {
					lines = append(lines[:i], lines[i+1:]...)
					break
				}
			}
		case "set":
			for i, ln := range lines {
				f := strings.Fields(ln)
				if len(f) >= 2 && !strings.HasPrefix(ln, "#") && strings.EqualFold(f[1], c.Pattern) {
					lines[i] = fmt.Sprintf("%-6s %-35s - %s", strings.ToUpper(c.Policy), c.Pattern, c.Description)
					break
				}
			}
		case "add":
			line := fmt.Sprintf("%-6s %-35s - %s", strings.ToUpper(c.Policy), c.Pattern, c.Description)
			// Insert before a trailing empty line when present, else append.
			if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "" {
				lines = append(lines[:n-1], line, "")
			} else {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// handleManifestApply implements `omaseal manifest apply <proposal> [--yes]`.
// It is a human gate: renders parsed changes, refuses manifest drift and
// openrouter/* self-edits, and requires per-item confirmation for every
// capability-expanding change. --yes (or no TTY) applies non-expanding changes
// only and exits nonzero listing the skipped expansions.
func handleManifestApply(args []string, yes bool) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: omaseal manifest apply <proposal-file> [--yes]")
		os.Exit(1)
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		printError("reading proposal: ", err)
		os.Exit(1)
	}
	var p Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		printError("parsing proposal: ", err)
		os.Exit(1)
	}
	if len(p.Changes) == 0 {
		fmt.Println("proposal contains no changes — nothing to apply")
		return
	}

	m, err := LoadManifest()
	if err != nil {
		printError("loading manifest: ", err)
		os.Exit(1)
	}
	cur, err := manifestFileSHA256(m.Path)
	if err != nil {
		printError("hashing manifest: ", err)
		os.Exit(1)
	}
	if cur != p.ManifestSHA256 {
		fmt.Fprintf(os.Stderr, "manifest changed since this proposal was generated (%s)\nre-run `omaseal manifest audit --proposal <path>` and review the fresh proposal\n", p.GeneratedAt.Format("2006-01-02 15:04"))
		os.Exit(1)
	}

	// R6: a proposal touching Jev's credential path is rejected wholesale —
	// partial application would let the unsafe change ride along.
	for _, c := range p.Changes {
		if governsJevCredential(c.Pattern) {
			fmt.Fprintf(os.Stderr, "refusing proposal: change to %q would govern openrouter/* (Jev's credential path)\nedit ai-manifest.txt by hand if this is genuinely intended\n", c.Pattern)
			os.Exit(1)
		}
	}

	// Render the parsed truth — this is what the human approves, regardless of
	// what the proposal claims about itself.
	fmt.Printf("proposal by %s, generated %s — %d change(s):\n\n", p.Generator, p.GeneratedAt.Format("2006-01-02 15:04"), len(p.Changes))
	expanding := make([]bool, len(p.Changes))
	for i, c := range p.Changes {
		expanding[i] = isExpansionChange(m, c)
		tag := "keep"
		if expanding[i] {
			tag = "EXPAND"
		} else if c.Action == "remove" || (c.Action == "set" && policyRank(RulePolicy(strings.ToUpper(c.Policy))) < policyRank(PolicyAsk)) {
			tag = "reduce"
		}
		fmt.Printf("  %d. [%s] %s %s %s\n", i+1, tag, c.Action, strings.ToUpper(c.Policy), c.Pattern)
		if c.Description != "" {
			fmt.Printf("     desc: %s\n", sanitizeField(c.Description))
		}
		if c.Rationale != "" {
			fmt.Printf("     why:  %s\n", sanitizeField(c.Rationale))
		}
	}
	fmt.Println()

	tty := isStdinTTY()
	if !tty && !yes {
		fmt.Fprintln(os.Stderr, "no TTY for review prompts — re-run interactively, or --yes to apply non-expanding changes only")
		os.Exit(1)
	}

	var accepted []ProposalChange
	var skipped []ProposalChange
	if yes || !tty {
		for i, c := range p.Changes {
			if expanding[i] {
				skipped = append(skipped, c)
			} else {
				accepted = append(accepted, c)
			}
		}
	} else {
		for i, c := range p.Changes {
			if expanding[i] {
				if confirm(fmt.Sprintf("apply expansion %d: %s %s %s? [y/N] ", i+1, c.Action, strings.ToUpper(c.Policy), c.Pattern)) {
					accepted = append(accepted, c)
				} else {
					skipped = append(skipped, c)
				}
			} else {
				accepted = append(accepted, c)
			}
		}
		if len(accepted) > 0 && !confirm(fmt.Sprintf("Apply %d non-expanding change(s)? [Y/n] ", len(accepted))) {
			accepted = nil
		}
	}

	if len(accepted) == 0 {
		fmt.Println("no changes accepted")
	} else {
		raw, err := os.ReadFile(m.Path)
		if err != nil && !os.IsNotExist(err) {
			printError("reading manifest: ", err)
			os.Exit(1)
		}
		out := applyChanges(string(raw), accepted)
		if err := writeFileMode(m.Path, []byte(out), 0600); err != nil {
			printError("writing manifest: ", err)
			os.Exit(1)
		}
		fmt.Printf("applied %d change(s) to %s\n", len(accepted), m.Path)
	}

	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "\nskipped %d expanding change(s) (need interactive confirmation):\n", len(skipped))
		for _, c := range skipped {
			fmt.Fprintf(os.Stderr, "  - %s %s %s\n", c.Action, strings.ToUpper(c.Policy), c.Pattern)
		}
		os.Exit(1)
	}
}
