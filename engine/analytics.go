package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Every successful secret read is counted: `get`, `reveal`, and `resolve`
// are logged by their CLI handlers, while MCP/IPC read paths emit an
// explicit `access` line so agent traffic is tallied too.
var accessLogRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})\s+(?:get|reveal|resolve|access)\s+(.+)$`)

// AccessStat records the usage frequency and recency for a given secret.
type AccessStat struct {
	Service      string    `json:"service"`
	Account      string    `json:"account"`
	Count        int       `json:"access_count"`
	LastAccessed time.Time `json:"last_accessed"`
}

// AnalyticsReport is the top-level summary of keyring usage.
type AnalyticsReport struct {
	TotalAccesses int          `json:"total_accesses"`
	UniqueSecrets int          `json:"unique_secrets"`
	Stats         []AccessStat `json:"stats"`
}

// statKey is the canonical map key for a credential: the exact
// "service/account" payload as logged. It is deliberately case-sensitive
// (Secret Service attribute matching is case-sensitive) and unsplit — items
// whose service or account contains "/" still match their own log lines.
func statKey(service, account string) string {
	return service + "/" + account
}

// ParseAccessLogs reads the OmaSeal log file and aggregates access statistics.
func ParseAccessLogs() (map[string]AccessStat, error) {
	path := LogPath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine log path: %v", logInitErr)
	}
	return ParseAccessLogsFromFile(path)
}

// ParseAccessLogsFromFile parses access statistics from a given log file.
func ParseAccessLogsFromFile(path string) (map[string]AccessStat, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]AccessStat), nil
		}
		return nil, err
	}
	defer f.Close()

	stats := make(map[string]AccessStat)
	scanner := bufio.NewScanner(f)
	// WriteLog bounds field lengths, but a pre-existing or hand-edited log
	// could hold longer lines; 1 MiB headroom keeps one giant line from
	// erroring every stats call.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	layout := "2006/01/02 15:04:05"

	for scanner.Scan() {
		line := scanner.Text()
		m := accessLogRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		// WriteLog stamps local time; parse in the local zone so
		// last_accessed is not shifted by the host's UTC offset.
		ts, err := time.ParseInLocation(layout, m[1], time.Local)
		if err != nil {
			continue
		}

		// Key by the exact logged payload so services containing "/" match
		// their items. The stored Service/Account split is display-only —
		// first-"/" is always correct for OmaSeal-written items.
		service, account, ok := strings.Cut(m[2], "/")
		if !ok {
			continue
		}
		k := m[2]

		cur, ok := stats[k]
		if !ok {
			stats[k] = AccessStat{
				Service:      service,
				Account:      account,
				Count:        1,
				LastAccessed: ts,
			}
		} else {
			cur.Count++
			if ts.After(cur.LastAccessed) {
				cur.LastAccessed = ts
			}
			stats[k] = cur
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}

// GetAnalyticsReport builds a summarized usage report from the access logs.
func GetAnalyticsReport() (*AnalyticsReport, error) {
	statsMap, err := ParseAccessLogs()
	if err != nil {
		return nil, err
	}
	return BuildAnalyticsReport(statsMap), nil
}

// BuildAnalyticsReport constructs an AnalyticsReport from an existing stats
// map, sorted most-used first. Consumers slice the prefix they want.
func BuildAnalyticsReport(statsMap map[string]AccessStat) *AnalyticsReport {
	var statsList []AccessStat
	total := 0
	for _, s := range statsMap {
		statsList = append(statsList, s)
		total += s.Count
	}

	sort.Slice(statsList, func(i, j int) bool {
		if statsList[i].Count != statsList[j].Count {
			return statsList[i].Count > statsList[j].Count
		}
		return statsList[i].LastAccessed.After(statsList[j].LastAccessed)
	})

	return &AnalyticsReport{
		TotalAccesses: total,
		UniqueSecrets: len(statsList),
		Stats:         statsList,
	}
}

// EnrichItemsWithStats decorates Item structs with access count and last accessed time.
func EnrichItemsWithStats(items []Item, stats map[string]AccessStat) []Item {
	for i := range items {
		k := statKey(items[i].Service, items[i].Account)
		if stat, ok := stats[k]; ok {
			items[i].AccessCount = stat.Count
			t := stat.LastAccessed
			items[i].LastAccessed = &t
		}
	}
	return items
}

// SortItemsByUsage sorts secret items descending by their access count.
// The QML panel mirrors this comparator for its "used" sort mode — keep
// the two orderings in sync.
func SortItemsByUsage(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].AccessCount != items[j].AccessCount {
			return items[i].AccessCount > items[j].AccessCount
		}
		if items[i].LastAccessed != nil && items[j].LastAccessed != nil {
			return items[i].LastAccessed.After(*items[j].LastAccessed)
		}
		if items[i].LastAccessed != nil {
			return true
		}
		if items[j].LastAccessed != nil {
			return false
		}
		return statKey(items[i].Service, items[i].Account) < statKey(items[j].Service, items[j].Account)
	})
}

// SortItemsByRecency sorts secret items by last access, most recent first;
// items never accessed fall to the bottom sorted by name.
func SortItemsByRecency(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastAccessed != nil && items[j].LastAccessed != nil {
			return items[i].LastAccessed.After(*items[j].LastAccessed)
		}
		if items[i].LastAccessed != nil {
			return true
		}
		if items[j].LastAccessed != nil {
			return false
		}
		return statKey(items[i].Service, items[i].Account) < statKey(items[j].Service, items[j].Account)
	})
}

// listWithUsage is the shared list+enrich+sort pipeline used by the CLI,
// IPC, and MCP list surfaces so all three return identical ordering.
// sortMode is "used", "recent", "name", or "" (name order, the default).
func listWithUsage(service, sortMode string) ([]Item, error) {
	items, err := List(service)
	if err != nil {
		return nil, err
	}
	stats, serr := ParseAccessLogs()
	if serr != nil {
		WriteLog("list: access-log parse failed, usage columns empty: %v", serr)
	} else if stats != nil {
		items = EnrichItemsWithStats(items, stats)
	}
	switch sortMode {
	case "used", "hits":
		SortItemsByUsage(items)
	case "recent":
		SortItemsByRecency(items)
	case "", "name":
		// List already returns name order.
	default:
		return nil, newError("invalid_sort", "omaseal list --sort=used|recent|name",
			fmt.Errorf("unknown sort %q", sortMode))
	}
	return items, nil
}

// handleStats renders the usage analytics report.
func handleStats() {
	jsonOut := hasFlag(os.Args, "--json")
	report, err := GetAnalyticsReport()
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

	if len(report.Stats) == 0 {
		fmt.Println("No secret access activity recorded yet.")
		return
	}

	const topN = 20
	top := report.Stats
	if len(top) > topN {
		top = top[:topN]
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "RANK\tHITS\tSERVICE\tACCOUNT\tLAST ACCESSED")
	for i, stat := range top {
		last := stat.LastAccessed.Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, "#%d\t%d\t%s\t%s\t%s\n", i+1, stat.Count, sanitizeField(stat.Service), sanitizeField(stat.Account), last)
	}
	w.Flush()
	if len(report.Stats) > topN {
		fmt.Printf("\n… and %d more (use --json for the full list)\n", len(report.Stats)-topN)
	}
}

// sanitizeField strips control characters from keyring metadata before it
// reaches a terminal, log line, or generated file. Foreign items can carry
// arbitrary attribute strings; OmaSeal-written names are already
// charset-constrained by validComponent.
func sanitizeField(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
