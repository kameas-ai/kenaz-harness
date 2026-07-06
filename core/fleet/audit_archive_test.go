package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// fakeEmitter is a race-safe audit emitter for tests.
type fakeEmitter struct {
	mu      sync.Mutex
	emitted []contextaudit.Event
}

func (f *fakeEmitter) Emit(_ context.Context, e contextaudit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, e)
	return nil
}

func (f *fakeEmitter) snapshot() []contextaudit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contextaudit.Event, len(f.emitted))
	copy(out, f.emitted)
	return out
}

// fakeSigner signs with a static "test" token.
type fakeSigner struct{}

func (fakeSigner) Sign(_ []byte) (string, string, error) {
	return "test-sig", "sha256:testfp", nil
}

func makeTailEvent(id string, prevHash [32]byte) contextaudit.TailEvent {
	payload := []byte(`{"test":true}`)
	ph := sha256.Sum256([]byte(id + string(payload)))
	return contextaudit.TailEvent{
		ID:          id,
		Kind:        "test.event",
		EmittedAt:   time.Now(),
		Payload:     payload,
		PayloadHash: ph,
		PrevHash:    prevHash,
	}
}

func TestAuditCursor_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := saveAuditCursor(dir, "01CURSOR"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := readAuditCursor(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "01CURSOR" {
		t.Errorf("want 01CURSOR, got %q", got)
	}
}

func TestAuditCursor_MissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := readAuditCursor(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("want empty cursor for missing file, got %q", got)
	}
}

func TestAuditCursor_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	// Write twice; tmp file should not persist.
	_ = saveAuditCursor(dir, "ID1")
	_ = saveAuditCursor(dir, "ID2")

	entries, _ := os.ReadDir(dir + "/fleet")
	for _, e := range entries {
		if e.Name() == "audit_cursor.txt.tmp" {
			t.Error("tmp file should not exist after successful rename")
		}
	}

	got, _ := readAuditCursor(dir)
	if got != "ID2" {
		t.Errorf("want ID2, got %q", got)
	}
}

func TestBuildAuditBatch(t *testing.T) {
	e1 := makeTailEvent("EV01", [32]byte{})
	e2 := makeTailEvent("EV02", e1.PayloadHash)
	events := []contextaudit.TailEvent{e1, e2}

	body, err := buildAuditBatch(events, fakeSigner{})
	if err != nil {
		t.Fatalf("buildAuditBatch: %v", err)
	}

	var batch auditBatchPayload
	if err := json.Unmarshal(body, &batch); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(batch.Events) != 2 {
		t.Errorf("want 2 events, got %d", len(batch.Events))
	}
	if batch.Signature != "test-sig" {
		t.Errorf("want test-sig, got %q", batch.Signature)
	}
	if batch.DevicePubkeyFingerprint != "sha256:testfp" {
		t.Errorf("want sha256:testfp, got %q", batch.DevicePubkeyFingerprint)
	}
}

// stubPoster is a test AuditHTTPPoster that records posted bodies and
// returns a configurable status code.
type stubPoster struct {
	mu       sync.Mutex
	bodies   [][]byte
	status   int
}

func (s *stubPoster) Post(_ context.Context, _ string, _ string, body io.Reader) (*http.Response, error) {
	data, _ := io.ReadAll(body)
	s.mu.Lock()
	s.bodies = append(s.bodies, data)
	s.mu.Unlock()
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	rec := httptest.NewRecorder()
	rec.WriteHeader(status)
	_ = json.NewEncoder(rec).Encode(map[string]string{"ok": "true"})
	return rec.Result(), nil
}

func (s *stubPoster) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.bodies))
	copy(out, s.bodies)
	return out
}

func TestArchiverFlush_CursorAdvances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	poster := &stubPoster{}

	tr := &contextaudit.MemoryTailReader{}
	e1 := makeTailEvent("EV001", [32]byte{})
	e2 := makeTailEvent("EV002", e1.PayloadHash)
	tr.Append(e1)
	tr.Append(e2)

	emitter := &fakeEmitter{}

	archiver := NewAuditArchiver(AuditArchiverConfig{
		Poster:        poster,
		DataDir:       dir,
		Tail:          tr,
		Signer:        fakeSigner{},
		Emitter:       emitter,
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})

	ctx := context.Background()
	if err := archiver.flushOnce(ctx); err != nil {
		t.Fatalf("flushOnce: %v", err)
	}

	// Cursor should advance to EV002.
	archiver.mu.RLock()
	cur := archiver.cursor
	archiver.mu.RUnlock()
	if cur != "EV002" {
		t.Errorf("cursor: want EV002, got %q", cur)
	}

	// Persisted cursor.
	saved, _ := readAuditCursor(dir)
	if saved != "EV002" {
		t.Errorf("saved cursor: want EV002, got %q", saved)
	}

	// KindFleetAuditArchived emitted.
	events := emitter.snapshot()
	found := false
	for _, e := range events {
		if e.Kind == contextaudit.KindFleetAuditArchived {
			found = true
		}
	}
	if !found {
		t.Error("want KindFleetAuditArchived emitted")
	}

	// Server was called.
	if len(poster.snapshot()) == 0 {
		t.Error("want at least one POST to fleet endpoint")
	}
}

func TestArchiverFlush_NothingToFlush(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tr := &contextaudit.MemoryTailReader{}
	archiver := NewAuditArchiver(AuditArchiverConfig{
		DataDir:       dir,
		Tail:          tr,
		BatchSize:     100,
		BatchInterval: 10 * time.Second,
	})

	ctx := context.Background()
	if err := archiver.flushOnce(ctx); err != nil {
		t.Fatalf("flushOnce on empty tail: %v", err)
	}
}

