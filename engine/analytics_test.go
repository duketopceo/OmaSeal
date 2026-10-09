package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// TestAccessLogExactKeys verifies payload-keyed matching: case variants stay
// distinct, a service containing "/" still enriches its own item, and a
// >64KB line does not poison the parse.
func TestAccessLogExactKeys(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "keys.log")

	big := strings.Repeat("x", 80*1024)
	data := `2026/09/14 01:00:00 access GitHub/work
2026/09/14 01:01:00 access github/work
2026/09/14 01:02:00 access github/work
2026/09/14 01:03:00 access a/b/c
2026/09/14 01:04:00 access ` + big + `/x
`
	if err := os.WriteFile(logPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatalf("oversized line broke parse: %v", err)
	}
	if stats[statKey("GitHub", "work")].Count != 1 {
		t.Fatalf("case-folded merge: %+v", stats)
	}
	if stats[statKey("github", "work")].Count != 2 {
		t.Fatalf("case-folded merge: %+v", stats)
	}
	// Slash-in-service payload: item Service="a/b" Account="c" must match.
	items := EnrichItemsWithStats([]Item{{Service: "a/b", Account: "c"}}, stats)
	if items[0].AccessCount != 1 {
		t.Fatalf("slash-service item not enriched: %+v", items[0])
	}
	if stats[statKey(big, "x")].Count != 1 {
		t.Fatalf("oversized-name stat missing: %d entries", len(stats))
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

// op= telemetry lines are emitted only on failure — they feed the failure
// counters and must not collide with the access-log regex.
func TestParseOpTelemetry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "ops.log")

	data := `2026/10/01 13:53:43 op=get target="Cloudflare API Token — Kurultai/duketopceo@gmail.com" dur=12000ms result=timeout code=keyring_timeout
2026/10/01 13:54:01 op=set target="svc/acct" dur=31ms result=error code=keyring_unavailable
2026/10/01 13:54:22 op=mcp-omaseal_get target="github/personal" dur=2ms result=error code=manifest_denied
2026/10/01 13:54:03 get openrouter/management
not-a-log-line op=get target="x/y" dur=1ms result=error code=bogus
`
	if err := os.WriteFile(logPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	telem, err := ParseOpTelemetry(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if telem.Failures != 3 {
		t.Fatalf("failures=%d, want 3", telem.Failures)
	}
	if telem.Timeouts != 1 {
		t.Fatalf("timeouts=%d, want 1", telem.Timeouts)
	}
	if telem.ByCode["keyring_timeout"] != 1 || telem.ByCode["manifest_denied"] != 1 || telem.ByCode["keyring_unavailable"] != 1 {
		t.Fatalf("by_code wrong: %+v", telem.ByCode)
	}
	if telem.SlowestMs != 12000 {
		t.Fatalf("slowest=%d, want 12000", telem.SlowestMs)
	}

	// The access-log parser must ignore op= lines — they carry the same
	// service/account names but are telemetry, not reads.
	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("access stats polluted by op lines: %+v", stats)
	}
}

// Missing log file -> empty telemetry, not an error.
func TestParseOpTelemetryAbsent(t *testing.T) {
	telem, err := ParseOpTelemetry(filepath.Join(t.TempDir(), "absent.log"))
	if err != nil || telem.Failures != 0 {
		t.Fatalf("absent log: telem=%+v err=%v", telem, err)
	}
}

// Chained log lines (trailing ` chain=<hex64>` from the tamper-evident
// writer) must parse identically to unchained ones — access stats, failure
// telemetry, and panel JSON all keep working after chaining ships.
func TestChainedLinesParse(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "chained.log")

	ch64 := " chain=" + strings.Repeat("ab", 32)
	data := "2026/10/01 14:00:00 get openrouter/default" + ch64 + "\n" +
		"2026/10/01 14:00:01 access svc/nested/acct" + ch64 + "\n" +
		"2026/10/01 14:00:02 op=get target=\"x/y\" dur=9ms result=timeout code=keyring_timeout" + ch64 + "\n"
	if err := os.WriteFile(logPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	stats, err := ParseAccessLogsFromFile(logPath)
	if err != nil || len(stats) != 2 {
		t.Fatalf("chained access lines must parse: %+v err=%v", stats, err)
	}
	if stats[statKey("svc", "nested/acct")].Count != 1 {
		t.Fatalf("chain field leaked into account: %+v", stats)
	}

	telem, err := ParseOpTelemetry(logPath)
	if err != nil || telem.Failures != 1 || telem.ByCode["keyring_timeout"] != 1 {
		t.Fatalf("chained telemetry line must parse: %+v err=%v", telem, err)
	}

	// Panel JSON strips the chain field — ReadLogJSON runs chainTail before
	// logLineRe, so exercise the same composition here (including a line
	// with no `source:` separator, which falls back to raw message).
	for _, l := range []string{
		"2026/10/01 14:00:00 get: openrouter/default" + ch64,
		"2026/10/01 14:00:00 plain message" + ch64,
	} {
		stripped, _, _ := chainTail(l)
		if strings.Contains(stripped, "chain=") {
			t.Fatalf("chainTail must drop the chain field: %q -> %q", l, stripped)
		}
		m := logLineRe.FindStringSubmatch(stripped)
		if m != nil && strings.Contains(m[3], "chain=") {
			t.Fatalf("chain field leaked into message: %q", m[3])
		}
	}
}

func TestSanitizeField(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "normal-name_123", "normal-name_123"},
		{"c0 controls", "a\x00b\x1fc", "abc"},
		{"newline stripped", "a\nb", "ab"},
		{"del stripped", "a\x7fb", "ab"},
		{"c1 controls", "a\u0080b\u009fc", "abc"},
		{"csi via c1", "evil\u009b[0mname", "evil[0mname"},
		{"unicode kept", "名前", "名前"},
	}
	for _, tt := range tests {
		if got := sanitizeField(tt.in); got != tt.want {
			t.Errorf("%s: sanitizeField(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestSanitizeFieldLengthCap(t *testing.T) {
	long := strings.Repeat("a", maxSanitizeRunes+50)
	got := sanitizeField(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatal("capped field should carry the truncation marker")
	}
	if got := len([]rune(strings.TrimSuffix(got, "…"))); got != maxSanitizeRunes {
		t.Fatalf("kept %d runes, want %d", got, maxSanitizeRunes)
	}
	// Exactly at the cap: no marker.
	exact := strings.Repeat("b", maxSanitizeRunes)
	if got := sanitizeField(exact); got != exact {
		t.Fatal("at-cap field should pass through unmarked")
	}
	// Stripped controls don't consume the cap.
	mixed := strings.Repeat("\u0080", 500) + strings.Repeat("c", maxSanitizeRunes)
	got = strings.TrimSuffix(sanitizeField(mixed), "…")
	if n := len([]rune(got)); n != maxSanitizeRunes {
		t.Fatalf("controls should not count toward cap, kept %d runes", n)
	}
}
