package event

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event/log"
	"github.com/sigil-tech/kaneaz-harness/core/event/redact"
)

// newTestEmitter builds a fully-wired emitter against an in-memory
// backend and a default-policy redaction pipeline. Returns the
// emitter, the underlying memory backend (for verification), and a
// teardown func to keep the helper symmetric with future fixtures.
func newTestEmitter(t *testing.T, opts ...EmitterOption) (Emitter, *log.MemoryBackend) {
	t.Helper()
	backend := log.NewMemoryBackend()
	store := log.NewStore(backend)

	pol := redact.DefaultPolicy()
	resolver := redact.NewStaticResolver(map[string][]byte{
		pol.SaltRef.Keychain: []byte("test-salt-EMITTER-XXXXXXXXXXX"),
	})
	pipe, err := redact.New(pol, resolver)
	if err != nil {
		t.Fatalf("redact.New: %v", err)
	}
	em, err := NewEmitter(store, pipe, opts...)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	return em, backend
}

// TestNewEmitter_NilStoreRejected pins the constructor's nil-guard for
// the store dependency.
func TestNewEmitter_NilStoreRejected(t *testing.T) {
	pol := redact.DefaultPolicy()
	resolver := redact.NewStaticResolver(map[string][]byte{pol.SaltRef.Keychain: []byte("s")})
	pipe, _ := redact.New(pol, resolver)
	if _, err := NewEmitter(nil, pipe); err == nil {
		t.Fatalf("nil store should be rejected")
	}
}

// TestNewEmitter_NilPipelineRejected pins C-004: the redaction pipeline
// is non-bypassable and a nil pipeline cannot be used to construct an
// emitter.
func TestNewEmitter_NilPipelineRejected(t *testing.T) {
	store := log.NewStore(log.NewMemoryBackend())
	_, err := NewEmitter(store, nil)
	if err == nil {
		t.Fatalf("nil pipeline should be rejected")
	}
	if !errors.Is(err, ErrRedactionBypassed) {
		t.Fatalf("nil pipeline error should classify as ErrRedactionBypassed, got %v", err)
	}
}

// TestAppend_RejectsMalformedKind covers FR-002 and the public
// ErrInvalidKind classifier.
func TestAppend_RejectsMalformedKind(t *testing.T) {
	em, _ := newTestEmitter(t)
	_, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("not-namespaced"), // missing dotted segment
		Payload:   map[string]any{"hello": "world"},
	})
	if err == nil {
		t.Fatalf("expected malformed kind rejection")
	}
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("expected ErrInvalidKind, got %v", err)
	}
}

// TestAppend_RejectsUnknownEmitter covers FR-017's namespace allowlist.
func TestAppend_RejectsUnknownEmitter(t *testing.T) {
	em, _ := newTestEmitter(t)
	_, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("rogue/source"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected unknown emitter rejection")
	}
	if !errors.Is(err, ErrUnknownEmitter) {
		t.Fatalf("expected ErrUnknownEmitter, got %v", err)
	}
}

// TestAppend_AcceptsUnregisteredButWellFormedKind pins NFR-006: novel
// well-formed kinds pass through opaquely so older readers stay
// forward-compatible.
func TestAppend_AcceptsUnregisteredButWellFormedKind(t *testing.T) {
	em, _ := newTestEmitter(t)
	out, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.future.kind"),
		Payload:   map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatalf("well-formed unknown kind should be accepted, got %v", err)
	}
	if out.Kind != Kind("llm.future.kind") {
		t.Fatalf("kind round-trip failed: %q", out.Kind)
	}
}

// TestAppend_RedactionApplied confirms the pipeline runs on the payload
// before persistence (FR-005, C-004). We feed an AWS-style key and
// assert it does not survive into the persisted row.
func TestAppend_RedactionApplied(t *testing.T) {
	em, backend := newTestEmitter(t)
	plaintext := "AKIAIOSFODNN7EXAMPLE"
	out, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"body": "key=" + plaintext + " more"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	row, err := backend.GetRow(context.Background(), string(out.EventID))
	if err != nil {
		t.Fatalf("GetRow: %v", err)
	}
	if strings.Contains(string(row.Payload), plaintext) {
		t.Fatalf("plaintext credential leaked into stored row: %s", row.Payload)
	}
	if !strings.Contains(string(row.Payload), "redacted:") {
		t.Fatalf("expected redaction placeholder in stored row, got %s", row.Payload)
	}
	if out.RedactionSummary.NoOp {
		t.Fatalf("redaction summary should report a hit, got NoOp=true")
	}
	if out.RedactionSummary.SaltFingerprint == "" {
		t.Fatalf("expected salt fingerprint on redaction summary")
	}
}

// TestAppend_ChainAdvances pins FR-004's chain-link contract: each new
// event's prev_hash equals the prior event's payload_hash within the
// same session, and a sibling session has its own chain rooted at zero.
func TestAppend_ChainAdvances(t *testing.T) {
	em, _ := newTestEmitter(t)

	sid := MustParseULID("01HFXY8B5VJ6T6T7AXJF9JT9F9")
	first, err := em.Append(context.Background(), AppendInput{
		SessionID: &sid,
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"step": 1},
	})
	if err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if first.PrevHash != ([32]byte{}) {
		t.Fatalf("first event in session should have zero prev_hash")
	}

	second, err := em.Append(context.Background(), AppendInput{
		SessionID: &sid,
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.completed"),
		Payload:   map[string]any{"step": 2},
	})
	if err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if second.PrevHash != first.PayloadHash {
		t.Fatalf("second.prev_hash %x != first.payload_hash %x", second.PrevHash, first.PayloadHash)
	}

	// Sibling session: independent chain rooted at zero.
	other := MustParseULID("01HZZZZZZZZZZZZZZZZZZZZZZZ")
	otherFirst, err := em.Append(context.Background(), AppendInput{
		SessionID: &other,
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"step": 1},
	})
	if err != nil {
		t.Fatalf("sibling Append: %v", err)
	}
	if otherFirst.PrevHash != ([32]byte{}) {
		t.Fatalf("sibling session must root at zero, got %x", otherFirst.PrevHash)
	}
}

// TestAppend_HeadlessChain confirms session-less events still chain
// against a degenerate "headless" stream so verifications can walk
// them without special-casing nil session.
func TestAppend_HeadlessChain(t *testing.T) {
	em, _ := newTestEmitter(t)

	first, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("event-log/"),
		Kind:      Kind("event-log.database.opened"),
		Payload:   map[string]any{"path": ":memory:"},
	})
	if err != nil {
		t.Fatalf("first headless Append: %v", err)
	}
	if first.PrevHash != ([32]byte{}) {
		t.Fatalf("first headless event should root at zero")
	}

	second, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("event-log/"),
		Kind:      Kind("event-log.retention.started"),
		Payload:   map[string]any{"policy": "keep_all"},
	})
	if err != nil {
		t.Fatalf("second headless Append: %v", err)
	}
	if second.PrevHash != first.PayloadHash {
		t.Fatalf("headless chain did not advance: prev=%x want=%x", second.PrevHash, first.PayloadHash)
	}
}

// TestAppend_ULIDMonotonic pins FR-016. Sequential appends produce
// strictly increasing ULIDs even when they fall in the same millisecond.
func TestAppend_ULIDMonotonic(t *testing.T) {
	em, _ := newTestEmitter(t)
	const N = 64
	var prev ULID
	for i := 0; i < N; i++ {
		out, err := em.Append(context.Background(), AppendInput{
			EmitterID: EmitterID("llm/anthropic"),
			Kind:      Kind("llm.bench.tick"),
			Payload:   map[string]any{"i": i},
		})
		if err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
		if i > 0 && string(out.EventID) <= string(prev) {
			t.Fatalf("ULID not monotonic: prev=%s cur=%s at i=%d", prev, out.EventID, i)
		}
		prev = out.EventID
	}
}

// TestAppend_ConcurrentSafe stresses the single-writer guarantee. N
// goroutines hammer the same session; every Append must succeed and
// the resulting chain must be unbroken (prev_hash[i] == payload_hash[i-1]).
func TestAppend_ConcurrentSafe(t *testing.T) {
	em, backend := newTestEmitter(t)
	sid := MustParseULID("01HFXY8B5VJ6T6T7AXJF9JT9F9")

	const G, K = 8, 25
	var wg sync.WaitGroup
	wg.Add(G)
	errCh := make(chan error, G*K)
	for g := 0; g < G; g++ {
		go func(g int) {
			defer wg.Done()
			for k := 0; k < K; k++ {
				_, err := em.Append(context.Background(), AppendInput{
					SessionID: &sid,
					EmitterID: EmitterID("llm/anthropic"),
					Kind:      Kind("llm.bench.tick"),
					Payload:   map[string]any{"g": g, "k": k},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Append failed: %v", err)
	}

	// Walk the session in id order and confirm the chain links.
	rows, err := backend.SelectBySession(context.Background(), string(sid), "", 0, false)
	if err != nil {
		t.Fatalf("SelectBySession: %v", err)
	}
	if len(rows) != G*K {
		t.Fatalf("got %d rows, want %d", len(rows), G*K)
	}
	var prevHash [32]byte
	for i, r := range rows {
		if r.PrevHash != prevHash {
			t.Fatalf("row[%d] prev_hash=%x want=%x", i, r.PrevHash, prevHash)
		}
		prevHash = r.PayloadHash
	}
}

// TestAppend_PayloadCanonicalized confirms the persisted payload bytes
// are canonical (sorted keys / no whitespace) so byte-stable replay
// (NFR-004) holds.
func TestAppend_PayloadCanonicalized(t *testing.T) {
	em, backend := newTestEmitter(t)
	out, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"b": 2, "a": 1},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	row, err := backend.GetRow(context.Background(), string(out.EventID))
	if err != nil {
		t.Fatalf("GetRow: %v", err)
	}
	// Canonical encoding of {"a":1,"b":2} is exactly that string.
	if string(row.Payload) != `{"a":1,"b":2}` {
		t.Fatalf("payload not canonical: %s", row.Payload)
	}
}

// TestAppend_RedactionSummaryPersisted asserts the summary lands in the
// row alongside the payload so audit can reason about which matchers
// fired without re-running redaction (FR-005 / R6).
func TestAppend_RedactionSummaryPersisted(t *testing.T) {
	em, backend := newTestEmitter(t)
	out, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"body": "key=AKIAIOSFODNN7EXAMPLE"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	row, err := backend.GetRow(context.Background(), string(out.EventID))
	if err != nil {
		t.Fatalf("GetRow: %v", err)
	}
	var sum RedactionSummary
	if err := json.Unmarshal([]byte(row.RedactionSummary), &sum); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if sum.NoOp {
		t.Fatalf("expected matcher hits in stored summary")
	}
	if len(sum.MatchersFired) == 0 {
		t.Fatalf("expected at least one matcher in stored summary")
	}
}

// TestAppend_DefaultEmitterIDFallback covers WithDefaultEmitterID:
// callers that leave AppendInput.EmitterID empty inherit the configured
// default. Ensures the production wiring can pin a single emitter id
// without forcing every call site to repeat it.
func TestAppend_DefaultEmitterIDFallback(t *testing.T) {
	em, _ := newTestEmitter(t, WithDefaultEmitterID(EmitterID("llm/anthropic")))
	out, err := em.Append(context.Background(), AppendInput{
		Kind:    Kind("llm.request.started"),
		Payload: map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if out.EmitterID != EmitterID("llm/anthropic") {
		t.Fatalf("default emitter id did not apply: %q", out.EmitterID)
	}
}

// TestAppend_ClockOverride pins WithEmitterClock — useful for the spec
// audit suite that wants deterministic EmittedAt fields.
func TestAppend_ClockOverride(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	em, _ := newTestEmitter(t, WithEmitterClock(func() time.Time { return fixed }))
	out, err := em.Append(context.Background(), AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"x": 1},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !out.EmittedAt.Equal(fixed) {
		t.Fatalf("clock override ignored: got %v want %v", out.EmittedAt, fixed)
	}
}

// TestAppend_ContextCancellation confirms the emitter honors context
// cancellation before doing any work.
func TestAppend_ContextCancellation(t *testing.T) {
	em, _ := newTestEmitter(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := em.Append(ctx, AppendInput{
		EmitterID: EmitterID("llm/anthropic"),
		Kind:      Kind("llm.request.started"),
		Payload:   map[string]any{"x": 1},
	})
	if err == nil {
		t.Fatalf("cancelled context should fail Append")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
