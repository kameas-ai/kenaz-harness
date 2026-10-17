package mlproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	collogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// lockedBuf is a goroutine-safe log sink.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// One INFO summary line per shipped batch — accepted / duplicates /
// rejected / bytes — and never any record content. Before this the
// shipper logged nothing on success, so a working pipeline was
// indistinguishable from an idle one in the harness log.
//
// NOT parallel: logging.Replace is process-global.
func TestShipper_LogsOneSummaryLinePerShippedBatch(t *testing.T) {
	sink := &lockedBuf{}
	logging.Replace(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { logging.Replace(logging.FileHandler()) })

	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	const marker = "SECRETCONTENTMARKER"
	r.seedEvents(4, 0)
	// A record whose body carries the marker: it must not reach the log.
	r.seedEventsWithPad(1, marker)
	var mu sync.Mutex
	sentBytes := 0
	r.poster.setFn(func(_ int, _ *collogsv1.ExportLogsServiceRequest, size int) (PostResult, error) {
		mu.Lock()
		sentBytes = size
		mu.Unlock()
		return PostResult{Status: 200, Body: []byte(`{"accepted":5,"ml":{"accepted":3,"duplicates":1,"rejected":1,"rejections":{"invalid_field":1}}}`)}, nil
	})
	r.shipNow()

	var lines []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if ln == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("log line not JSON: %q", ln)
		}
		if m["msg"] == "mlproducer.ship.batch" {
			lines = append(lines, m)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("mlproducer.ship.batch lines = %d, want exactly 1 per shipped batch; log:\n%s", len(lines), sink.String())
	}
	got := lines[0]
	if got["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got["level"])
	}
	mu.Lock()
	want := map[string]float64{"sent": 5, "accepted": 3, "duplicates": 1, "rejected": 1, "bytes": float64(sentBytes)}
	mu.Unlock()
	for k, v := range want {
		if f, _ := got[k].(float64); f != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if strings.Contains(sink.String(), marker) {
		t.Fatal("record content reached the log")
	}
}
