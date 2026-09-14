package main

import (
	"bytes"
	"log"
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

	report := BuildAnalyticsReport(stats)
	if report.TotalAccesses != 6 {
		t.Errorf("expected 6 total accesses, got %d", report.TotalAccesses)
	}
	if report.UniqueSecrets != 2 {
		t.Errorf("expected 2 unique secrets, got %d", report.UniqueSecrets)
	}
	if len(report.Stats) != 2 || report.Stats[0].Service != "openrouter" {
		t.Errorf("expected stats sorted most-used first, got %+v", report.Stats)
	}
}

func TestSortItemsByUsage(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)

	items := []Item{
		{Service: "svcB", Account: "acct", AccessCount: 5, LastAccessed: &earlier},
		{Service: "svcA", Account: "acct", AccessCount: 10, LastAccessed: &now},
		{Service: "svcC", Account: "acct", AccessCount: 0, LastAccessed: nil},
		// Ties: same count -> more recent access first; both stale -> name order.
		{Service: "svcD", Account: "b", AccessCount: 5, LastAccessed: &now},
		{Service: "svcE", Account: "a", AccessCount: 0, LastAccessed: nil},
	}

	SortItemsByUsage(items)

	order := []string{}
	for _, it := range items {
		order = append(order, it.Service)
	}
	want := []string{"svcA", "svcD", "svcB", "svcC", "svcE"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("SortItemsByUsage order %v, want %v", order, want)
		}
	}
}

// TestParseAccessLogEdges covers the skip paths: missing file, corrupt
// timestamps, verb lines without a service/account separator, and accounts
// that legitimately contain '/'.
func TestParseAccessLogEdges(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing file -> empty map, not an error.
	stats, err := ParseAccessLogsFromFile(filepath.Join(tmpDir, "absent.log"))
	if err != nil || len(stats) != 0 {
		t.Fatalf("missing file: stats=%v err=%v", stats, err)
	}

	data := `not-a-timestamp get openrouter/default
2026/09/14 01:00:00 get noslash
2026/09/14 01:05:00 access browseros/openrouter-work/apiKey
`
	logPath := filepath.Join(tmpDir, "edge.log")
	if err := os.WriteFile(logPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err = ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 parsed stat, got %+v", stats)
	}
	stat, ok := stats[statKey("browseros", "openrouter-work/apiKey")]
	if !ok || stat.Account != "openrouter-work/apiKey" {
		t.Fatalf("multi-slash account misattributed: %+v", stat)
	}
}

// TestAccessLogProducerParser bridges the WriteLog producers and the parser:
// the emitted line shape must stay regex-compatible or this test fails.
func TestAccessLogProducerParser(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer SetLogOutput()
	WriteLog("access %s/%s", "svc", "acct")
	WriteLog("get %s/%s", "svc2", "acct2")

	logPath := filepath.Join(t.TempDir(), "prod.log")
	if err := os.WriteFile(logPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("WriteLog output no longer parses: %q -> %+v", buf.String(), stats)
	}
}
