package logstore_test

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logstore"
)

// ── ring buffer eviction ────────────────────────────────────────────────────

func TestStore_Eviction(t *testing.T) {
	s := logstore.New(3)
	rows := []logstore.Row{
		{Timestamp: "t1", Level: logstore.LevelInfo, Source: "a", Message: "first"},
		{Timestamp: "t2", Level: logstore.LevelInfo, Source: "a", Message: "second"},
		{Timestamp: "t3", Level: logstore.LevelInfo, Source: "a", Message: "third"},
		{Timestamp: "t4", Level: logstore.LevelInfo, Source: "a", Message: "fourth"}, // evicts first
	}
	for _, r := range rows {
		s.Append(r)
	}
	snap := s.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 rows, got %d", len(snap))
	}
	// Oldest should now be "second" (first was evicted).
	if snap[0].Message != "second" {
		t.Errorf("oldest want %q, got %q", "second", snap[0].Message)
	}
	if snap[2].Message != "fourth" {
		t.Errorf("newest want %q, got %q", "fourth", snap[2].Message)
	}
}

func TestStore_Empty(t *testing.T) {
	s := logstore.New(10)
	if snap := s.Snapshot(); len(snap) != 0 {
		t.Fatalf("want empty snapshot, got %d rows", len(snap))
	}
}

func TestStore_BelowCap(t *testing.T) {
	s := logstore.New(100)
	for i := 0; i < 50; i++ {
		s.Append(logstore.Row{Message: fmt.Sprintf("msg%d", i), Level: logstore.LevelInfo})
	}
	snap := s.Snapshot()
	if len(snap) != 50 {
		t.Fatalf("want 50 rows, got %d", len(snap))
	}
	if snap[0].Message != "msg0" {
		t.Errorf("want msg0, got %q", snap[0].Message)
	}
}

func TestStore_DefaultCap(t *testing.T) {
	s := logstore.New(0)
	// Write DefaultCap+10 rows; only DefaultCap should survive.
	for i := 0; i < logstore.DefaultCap+10; i++ {
		s.Append(logstore.Row{Message: fmt.Sprintf("row%d", i), Level: logstore.LevelInfo})
	}
	snap := s.Snapshot()
	if len(snap) != logstore.DefaultCap {
		t.Fatalf("want %d rows, got %d", logstore.DefaultCap, len(snap))
	}
}

// ── redaction ───────────────────────────────────────────────────────────────

func TestRedactMessage_Bearer(t *testing.T) {
	in := "connecting with Authorization: Bearer sk-abcdefghijklmnop1234567890"
	out := logstore.RedactMessage(in)
	if contains(out, "sk-") {
		t.Errorf("bearer token not redacted: %q", out)
	}
}

func TestRedactMessage_SecretRef(t *testing.T) {
	in := "resolved @secret://my-provider/token to plaintext value"
	out := logstore.RedactMessage(in)
	if contains(out, "@secret://") {
		t.Errorf("secret ref not redacted: %q", out)
	}
}

func TestRedactMessage_HexToken(t *testing.T) {
	in := "auth ok token=abcdef1234567890abcdef1234567890"
	out := logstore.RedactMessage(in)
	if contains(out, "abcdef1234567890abcdef1234567890") {
		t.Errorf("hex token not redacted: %q", out)
	}
}

func TestRedactMessage_Clean(t *testing.T) {
	in := "server started on port 8080"
	out := logstore.RedactMessage(in)
	if out != in {
		t.Errorf("clean message mutated: got %q", out)
	}
}

func TestStore_AppendsRedact(t *testing.T) {
	s := logstore.New(10)
	s.Append(logstore.Row{
		Message: "Bearer eyJhbGciOiJSUzI1NiJ9.secretpayload",
		Level:   logstore.LevelInfo,
	})
	snap := s.Snapshot()
	if contains(snap[0].Message, "eyJhbGciOiJSUzI1NiJ9") {
		t.Errorf("token not redacted in stored row: %q", snap[0].Message)
	}
}

// ── List / filter ────────────────────────────────────────────────────────────

func TestStore_ListLevelFilter(t *testing.T) {
	s := logstore.New(100)
	s.Append(logstore.Row{Level: logstore.LevelDebug, Message: "d", Source: "x"})
	s.Append(logstore.Row{Level: logstore.LevelInfo, Message: "i", Source: "x"})
	s.Append(logstore.Row{Level: logstore.LevelWarn, Message: "w", Source: "x"})
	s.Append(logstore.Row{Level: logstore.LevelError, Message: "e", Source: "x"})

	rows := s.List(logstore.Filter{Level: logstore.LevelWarn})
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (warn+error), got %d", len(rows))
	}
}

func TestStore_ListSourceFilter(t *testing.T) {
	s := logstore.New(100)
	s.Append(logstore.Row{Level: logstore.LevelInfo, Source: "mcp:foo", Message: "a"})
	s.Append(logstore.Row{Level: logstore.LevelInfo, Source: "sessions", Message: "b"})

	rows := s.List(logstore.Filter{Source: "mcp"})
	if len(rows) != 1 || rows[0].Message != "a" {
		t.Fatalf("source filter failed: %+v", rows)
	}
}

func TestStore_ListSearchFilter(t *testing.T) {
	s := logstore.New(100)
	s.Append(logstore.Row{Level: logstore.LevelInfo, Source: "x", Message: "connection timeout"})
	s.Append(logstore.Row{Level: logstore.LevelInfo, Source: "x", Message: "server started"})

	rows := s.List(logstore.Filter{Search: "timeout"})
	if len(rows) != 1 || rows[0].Message != "connection timeout" {
		t.Fatalf("search filter failed: %+v", rows)
	}
}

func TestStore_ListLimit(t *testing.T) {
	s := logstore.New(100)
	for i := 0; i < 20; i++ {
		s.Append(logstore.Row{Level: logstore.LevelInfo, Message: fmt.Sprintf("m%d", i)})
	}
	rows := s.List(logstore.Filter{Limit: 5})
	if len(rows) != 5 {
		t.Fatalf("want 5, got %d", len(rows))
	}
}

func TestStore_ListNewestFirst(t *testing.T) {
	s := logstore.New(100)
	s.Append(logstore.Row{Level: logstore.LevelInfo, Message: "first"})
	s.Append(logstore.Row{Level: logstore.LevelInfo, Message: "second"})
	s.Append(logstore.Row{Level: logstore.LevelInfo, Message: "third"})

	rows := s.List(logstore.Filter{})
	if rows[0].Message != "third" {
		t.Errorf("want newest first, got %q", rows[0].Message)
	}
}

// ── slog handler ─────────────────────────────────────────────────────────────

func TestHandler_Captures(t *testing.T) {
	s := logstore.New(100)
	h := logstore.NewHandler(s, nil)
	logger := slog.New(h)
	logger.Info("test message")
	rows := s.List(logstore.Filter{})
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].Message != "test message" {
		t.Errorf("want %q, got %q", "test message", rows[0].Message)
	}
	if rows[0].Level != logstore.LevelInfo {
		t.Errorf("want info level, got %q", rows[0].Level)
	}
}

func TestHandler_WithSource(t *testing.T) {
	s := logstore.New(100)
	h := logstore.NewHandler(s, nil).WithSource("mcp:my-recipe")
	logger := slog.New(h)
	logger.Warn("spawn failed")
	rows := s.List(logstore.Filter{})
	if len(rows) == 0 {
		t.Fatal("no rows captured")
	}
	if rows[0].Source != "mcp:my-recipe" {
		t.Errorf("want source mcp:my-recipe, got %q", rows[0].Source)
	}
}

// ── concurrency / race ───────────────────────────────────────────────────────

func TestStore_ConcurrentWrites(t *testing.T) {
	s := logstore.New(1000)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Append(logstore.Row{
					Level:   logstore.LevelInfo,
					Message: fmt.Sprintf("goroutine %d message %d", n, j),
				})
			}
		}(i)
	}
	wg.Wait()
	snap := s.Snapshot()
	// We wrote 50*100=5000 rows into a cap-1000 buffer; at most 1000 survive.
	if len(snap) > 1000 {
		t.Fatalf("snapshot too large: %d", len(snap))
	}
}

func TestStore_AppendRaw(t *testing.T) {
	s := logstore.New(100)
	s.AppendRaw("mcp:foo", "line one\nline two\n\nline three", logstore.LevelWarn)
	rows := s.List(logstore.Filter{Source: "mcp:foo"})
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(rows), rows)
	}
	// Newest first
	if rows[0].Message != "line three" {
		t.Errorf("want newest last-line, got %q", rows[0].Message)
	}
}

// ── timestamp ────────────────────────────────────────────────────────────────

func TestHandler_TimestampSet(t *testing.T) {
	s := logstore.New(10)
	h := logstore.NewHandler(s, nil)
	logger := slog.New(h)
	before := time.Now()
	logger.Info("ts check")
	after := time.Now()
	rows := s.List(logstore.Filter{})
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	ts, err := time.Parse(time.RFC3339Nano, rows[0].Timestamp)
	if err != nil {
		t.Fatalf("invalid timestamp %q: %v", rows[0].Timestamp, err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v out of range [%v, %v]", ts, before, after)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
