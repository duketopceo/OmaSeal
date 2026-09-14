package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseAccessLogs(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	data := `2026/09/14 01:00:00 omaseal dev: command get
2026/09/14 01:00:00 get openrouter/default
2026/09/14 01:05:00 get openrouter/default
2026/09/14 01:10:00 get kurultai/antigravity
`
	if err := os.WriteFile(logPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatalf("ParseAccessLogs failed: %v", err)
	}

	k1 := statKey("openrouter", "default")
	if stat, ok := stats[k1]; !ok || stat.Count != 2 {
		t.Errorf("expected openrouter/default count 2, got %+v", stat)
	}

	k2 := statKey("kurultai", "antigravity")
	if stat, ok := stats[k2]; !ok || stat.Count != 1 {
		t.Errorf("expected kurultai/antigravity count 1, got %+v", stat)
	}

	report := BuildAnalyticsReport(stats, 10)
	if report.TotalAccesses != 3 {
		t.Errorf("expected 3 total accesses, got %d", report.TotalAccesses)
	}
	if report.UniqueSecrets != 2 {
		t.Errorf("expected 2 unique secrets, got %d", report.UniqueSecrets)
	}
}

func TestSortItemsByUsage(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	items := []Item{
		{Service: "svcB", Account: "acct", AccessCount: 5, LastAccessed: &earlier},
		{Service: "svcA", Account: "acct", AccessCount: 10, LastAccessed: &now},
		{Service: "svcC", Account: "acct", AccessCount: 0, LastAccessed: nil},
	}

	SortItemsByUsage(items)

	if items[0].Service != "svcA" || items[1].Service != "svcB" || items[2].Service != "svcC" {
		t.Errorf("SortItemsByUsage order unexpected: %+v", items)
	}
}
