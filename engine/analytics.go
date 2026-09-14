package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

var getLogRe = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2})\s+get\s+(.+)$`)

// AccessStat records the usage frequency and recency for a given secret.
type AccessStat struct {
	Service      string    `json:"service"`
	Account      string    `json:"account"`
	Count        int       `json:"count"`
	LastAccessed time.Time `json:"last_accessed"`
}

// AnalyticsReport is the top-level summary of keyring usage.
type AnalyticsReport struct {
	TotalAccesses int          `json:"total_accesses"`
	UniqueSecrets int          `json:"unique_secrets"`
	TopSecrets    []AccessStat `json:"top_secrets"`
	Stats         []AccessStat `json:"stats,omitempty"`
}

func statKey(service, account string) string {
	return strings.ToLower(service) + "\x00" + strings.ToLower(account)
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
	layout := "2006/01/02 15:04:05"

	for scanner.Scan() {
		line := scanner.Text()
		m := getLogRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		tsStr := m[1]
		target := m[2]

		ts, err := time.Parse(layout, tsStr)
		if err != nil {
			continue
		}

		idx := strings.Index(target, "/")
		if idx == -1 {
			continue
		}

		service := target[:idx]
		account := target[idx+1:]
		k := statKey(service, account)

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
func GetAnalyticsReport(topN int) (*AnalyticsReport, error) {
	statsMap, err := ParseAccessLogs()
	if err != nil {
		return nil, err
	}
	return BuildAnalyticsReport(statsMap, topN), nil
}

// BuildAnalyticsReport constructs an AnalyticsReport from an existing stats map.
func BuildAnalyticsReport(statsMap map[string]AccessStat, topN int) *AnalyticsReport {
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

	limit := len(statsList)
	if topN > 0 && topN < limit {
		limit = topN
	}

	top := make([]AccessStat, limit)
	copy(top, statsList[:limit])

	return &AnalyticsReport{
		TotalAccesses: total,
		UniqueSecrets: len(statsList),
		TopSecrets:    top,
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
		return strings.ToLower(items[i].Service) < strings.ToLower(items[j].Service)
	})
}
