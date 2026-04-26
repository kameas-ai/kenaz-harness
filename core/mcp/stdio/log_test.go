package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureHandler is a slog.Handler that records every record into a
// goroutine-safe slice for inspection by the test.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *captureHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

func TestLogSink_LevelMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"notice", slog.LevelInfo},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"critical", slog.LevelError},
		{"alert", slog.LevelError},
		{"emergency", slog.LevelError},
		{"unknown", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := mapLogLevel(tc.in); got != tc.want {
			t.Fatalf("mapLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLogSink_Handle_PopulatesRecord(t *testing.T) {
	t.Parallel()
	cap := &captureHandler{}
	sink := NewLogSink(slog.New(cap))

	raw := json.RawMessage(`{"level":"warning","logger":"server.x","data":{"message":"x"}}`)
	sink.Handle(context.Background(), "brave-search", raw)

	rs := cap.snapshot()
	if len(rs) != 1 {
		t.Fatalf("records = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.Level != slog.LevelWarn {
		t.Fatalf("level = %v, want Warn", r.Level)
	}
	if !strings.Contains(r.Message, "brave-search") || !strings.Contains(r.Message, "message") {
		t.Fatalf("message = %q", r.Message)
	}
	gotAttrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		gotAttrs[a.Key] = a.Value.Any()
		return true
	})
	if gotAttrs["mcp.recipe"] != "brave-search" {
		t.Fatalf("mcp.recipe = %v", gotAttrs["mcp.recipe"])
	}
	if gotAttrs["mcp.level"] != "warning" {
		t.Fatalf("mcp.level = %v", gotAttrs["mcp.level"])
	}
	if gotAttrs["mcp.logger"] != "server.x" {
		t.Fatalf("mcp.logger = %v", gotAttrs["mcp.logger"])
	}
	if _, ok := gotAttrs["mcp.data"]; !ok {
		t.Fatalf("mcp.data attr missing")
	}
}

func TestLogSink_MalformedDropped(t *testing.T) {
	t.Parallel()
	cap := &captureHandler{}
	sink := NewLogSink(slog.New(cap))
	// Garbage JSON — sink logs at debug and drops; the production
	// behavior we care about is "no panic, no crash".
	sink.Handle(context.Background(), "x", json.RawMessage(`{not-json`))
	// Empty payload — also dropped.
	sink.Handle(context.Background(), "x", nil)
	// We don't assert on the captured records here — the only contract
	// is that the call returns and never panics.
}

// TestLog_ServerNotificationPath drives the full reader-loop:
// fake server emits a notifications/message at warning level after
// initialize; the host's LogSink must surface it through slog.
func TestLog_ServerNotificationPath(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	cap := &captureHandler{}
	logger := slog.New(cap)
	inst := newServerInstance("fake", logger, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--emit-log", "warning"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range cap.snapshot() {
			if r.Level == slog.LevelWarn && strings.Contains(r.Message, "fake") {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("warning-level notification never reached slog")
}

// TestLog_BufferedWriterCompatibility is a smoke test that LogSink
// works with a real JSON handler — the production logger is JSON-
// backed and we don't want a struct-tag drift to break only there.
func TestLog_JSONHandlerCompatible(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sink := NewLogSink(logger)
	sink.Handle(context.Background(), "r1", json.RawMessage(`{"level":"info","data":{"k":"v"}}`))
	if !strings.Contains(buf.String(), `"mcp.recipe":"r1"`) {
		t.Fatalf("json handler output: %s", buf.String())
	}
}
