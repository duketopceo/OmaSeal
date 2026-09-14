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
2026/09/14 01:15:00 reveal openrouter/default
2026/09/14 01:20:00 resolve kurultai/antigravity
2026/09/14 01:25:00 access openrouter/default
2026/09/14 01:30:00 mcp tool: omaseal_get openrouter/default
2026/09/14 01:35:00 ipc get
2026/09/14 01:40:00 set openrouter/default
`
	if err := os.WriteFile(logPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatalf("ParseAccessLogs failed: %v", err)
	}

	k1 := statKey("openrouter", "default")
	if stat, ok := stats[k1]; !ok || stat.Count != 4 {
		t.Errorf("expected openrouter/default count 4 (get+reveal+access), got %+v", stat)
	}

	k2 := statKey("kurultai", "antigravity")
	if stat, ok := stats[k2]; !ok || stat.Count != 2 {
		t.Errorf("expected kurultai/antigravity count 2 (get+resolve), got %+v", stat)
	}

	report := BuildAnalyticsReport(stats, 10)
	if report.TotalAccesses != 6 {
		t.Errorf("expected 6 total accesses, got %d", report.TotalAccesses)
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
