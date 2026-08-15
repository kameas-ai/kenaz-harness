package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// session_engine_test.go drives the threshold-mode engine through fakes for
// every dependency. The fakes are intentionally minimal — just enough
// to record calls and replay scripted answers — so the tests can pin
// the algorithm's behavior precisely (plan §2.4 / WP03 acceptance).

// fakeStore records ApplyCompaction calls and serves a fixed
// ListActiveMessages payload.
type fakeStore struct {
	messages []SessionMessage
	listErr  error

	applyCalls []applyCompactionCall
	applyErr   error
}

type applyCompactionCall struct {
	sessionID   string
	summary     SessionMessage
	originalIDs []string
	archivedAt  time.Time
}

func (s *fakeStore) ListActiveMessages(_ context.Context, _ string) ([]SessionMessage, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]SessionMessage(nil), s.messages...), nil
}

func (s *fakeStore) ApplyCompaction(_ context.Context, sessionID string, summary SessionMessage, originalIDs []string, archivedAt time.Time) error {
	s.applyCalls = append(s.applyCalls, applyCompactionCall{
		sessionID:   sessionID,
		summary:     summary,
		originalIDs: append([]string(nil), originalIDs...),
		archivedAt:  archivedAt,
	})
	return s.applyErr
}

// fakeLLM records CallForSummary calls and replays a scripted answer.
type fakeLLM struct {
	text         string
	inputTokens  int
	outputTokens int
	err          error

	calls []llmCall
}

type llmCall struct {
	model        ProviderProfileRef
	systemPrompt string
	userPrompt   string
}

func (l *fakeLLM) CallForSummary(_ context.Context, model ProviderProfileRef, systemPrompt, userPrompt string) (string, int, int, error) {
	l.calls = append(l.calls, llmCall{model: model, systemPrompt: systemPrompt, userPrompt: userPrompt})
	if l.err != nil {
		return "", 0, 0, l.err
	}
	return l.text, l.inputTokens, l.outputTokens, nil
}

// fakeCapabilities replays a fixed MaxContextTokens answer.
type fakeCapabilities struct {
	max int
	ok  bool
}

func (c fakeCapabilities) MaxContextTokens(_ ProviderProfileRef) (int, bool) {
	return c.max, c.ok
}

// fakeAudit records every Emit call.
type fakeAudit struct {
	events []auditEvent
}

type auditEvent struct {
	kind    audit.Kind
	payload any
}

func (a *fakeAudit) Emit(_ context.Context, kind audit.Kind, payload any) {
	a.events = append(a.events, auditEvent{kind: kind, payload: payload})
}

// fakeTokenizer returns a deterministic count: one token per rune in
// the content (ignoring system prompt / framing). Tests use this so
// boundary index math is fully predictable.
type fakeTokenizer struct{}

func (fakeTokenizer) Count(systemPrompt string, msgs []TokenizerMessage) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
	}
	// Add the system prompt's runes too — the engine calls Count on the
	// full request shape during the pre-flight check, and we want that
	// path to remain deterministic.
	total += len(systemPrompt)
	return total
}

// fixedClock is the injectable clock; returns a static instant so
// archived_at / compacted_at comparisons are exact.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// build100MessageFixture creates a deterministic 100-message session.
// Each message's Content is "user N" / "assistant N" exactly 10 chars
// long → 10 tokens per message under fakeTokenizer → 1000 tokens total.
// That makes oldestPct math exact.
func build100MessageFixture() []SessionMessage {
	out := make([]SessionMessage, 100)
	for i := 0; i < 100; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// Keep each content string exactly 10 chars wide for token math.
		content := fmt.Sprintf("%-10s", fmt.Sprintf("turn-%03d", i))
		out[i] = SessionMessage{
			ID:       fmt.Sprintf("msg-%03d", i),
			Role:     role,
			Content:  content[:10],
			Sequence: int64(i),
		}
	}
	return out
}

// newTestEngine wraps NewSessionEngine with sensible test defaults (deterministic
// clock, no recent-window clamp by default — tests opt into it).
func newTestEngine(t *testing.T, store SessionMessageStore, llm LLMCaller, caps CapabilityLookup, em AuditEmitter, recentWindow int) SessionEngine {
	t.Helper()
	eng, err := NewSessionEngine(SessionEngineConfig{
		Store:        store,
		LLM:          llm,
		Capabilities: caps,
		Audit:        em,
		RecentWindow: func() int { return recentWindow },
		Tokenizer:    fakeTokenizer{},
		Now:          fixedClock(time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewSessionEngine: %v", err)
	}
	return eng
}

// TestCompact_HappyPath is the WP03 headline acceptance: 100 msgs,
// oldestPct=0.30, exactly one ApplyCompaction with the expected slice
// archived and a synthetic system summary inserted.
func TestCompact_HappyPath(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{
		text:         "compressed context",
		outputTokens: 50,
	}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0) // recentWindow=0 disables the clamp

	model := ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"}
	id, err := eng.Compact(context.Background(), "sess-1", model, 0.30)
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	if id == "" {
		t.Fatalf("Compact returned empty id on a non-no-op run")
	}

	// Total tokens = 1000 (10 per message × 100); 30% target = 300, which
	// our running-sum loop hits at boundary = 30 (10 tokens × 30 msgs).
	wantBoundary := 30

	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	call := store.applyCalls[0]
	if got := len(call.originalIDs); got != wantBoundary {
		t.Fatalf("originalIDs len = %d, want %d", got, wantBoundary)
	}
	for i := 0; i < wantBoundary; i++ {
		want := fmt.Sprintf("msg-%03d", i)
		if call.originalIDs[i] != want {
			t.Fatalf("originalIDs[%d] = %q, want %q", i, call.originalIDs[i], want)
		}
	}

	// Summary row asserts.
	if call.summary.Role != "system" {
		t.Errorf("summary role = %q, want system", call.summary.Role)
	}
	if !strings.HasPrefix(call.summary.Content, summaryContentPrefix) {
		t.Errorf("summary content missing prefix: %q", call.summary.Content)
	}
	if !strings.HasSuffix(call.summary.Content, summaryContentSuffix) {
		t.Errorf("summary content missing suffix: %q", call.summary.Content)
	}
	if !strings.Contains(call.summary.Content, "compressed context") {
		t.Errorf("summary content missing LLM text: %q", call.summary.Content)
	}
	if call.summary.Sequence != 0 {
		t.Errorf("summary sequence = %d, want 0 (lowest of span)", call.summary.Sequence)
	}
	if call.summary.ID == "" {
		t.Errorf("summary id should be non-empty")
	}
	if call.summary.ID != id {
		t.Errorf("returned id %q != stored summary id %q", id, call.summary.ID)
	}

	// LLM was called exactly once with the right model.
	if got := len(llm.calls); got != 1 {
		t.Fatalf("LLM calls = %d, want 1", got)
	}
	if llm.calls[0].model != model {
		t.Errorf("LLM call model = %+v, want %+v", llm.calls[0].model, model)
	}

	// Audit emitted exactly one KindSessionCompacted event.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindSessionCompacted {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindSessionCompacted)
	}
	pl, ok := em.events[0].payload.(audit.SessionCompactedPayload)
	if !ok {
		t.Fatalf("audit payload type = %T, want SessionCompactedPayload", em.events[0].payload)
	}
	if pl.SessionID != "sess-1" {
		t.Errorf("payload SessionID = %q, want sess-1", pl.SessionID)
	}
	if pl.TokensInSpan != 300 {
		t.Errorf("payload TokensInSpan = %d, want 300", pl.TokensInSpan)
	}
	if pl.TokensAfterSummary != 50 {
		t.Errorf("payload TokensAfterSummary = %d, want 50", pl.TokensAfterSummary)
	}
	wantRatio := 50.0 / 300.0
	if pl.CompressionRatio < wantRatio-0.0001 || pl.CompressionRatio > wantRatio+0.0001 {
		t.Errorf("payload CompressionRatio = %f, want %f", pl.CompressionRatio, wantRatio)
	}
}

// TestCompact_ModelTooSmall pre-flight rejects spans larger than the
// compaction model's MaxContextTokens with the typed error carrying both
// numbers; the LLM is never called.
func TestCompact_ModelTooSmall(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{}
	// Cap is 50 tokens — far smaller than the prompt we'll build.
	caps := fakeCapabilities{max: 50, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess-1", ProviderProfileRef{}, 0.30)
	if err == nil {
		t.Fatalf("expected ErrCompactionModelTooSmall, got nil")
	}
	var typed *ErrCompactionModelTooSmall
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *ErrCompactionModelTooSmall", err)
	}
	if typed.ModelMaxTokens != 50 {
		t.Errorf("ModelMaxTokens = %d, want 50", typed.ModelMaxTokens)
	}
	if typed.NeedsTokens <= 50 {
		t.Errorf("NeedsTokens = %d, want > 50 (so the error makes sense)", typed.NeedsTokens)
	}
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0 (pre-flight should reject before any wire call)", got)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0", got)
	}

	// Audit should record one KindCompactionFailed with model_too_small.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindCompactionFailed {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindCompactionFailed)
	}
	pl, ok := em.events[0].payload.(audit.CompactionFailedPayload)
	if !ok {
		t.Fatalf("audit payload type = %T, want CompactionFailedPayload", em.events[0].payload)
	}
	if pl.ErrorKind != "model_too_small" {
		t.Errorf("payload ErrorKind = %q, want model_too_small", pl.ErrorKind)
	}
}

// TestCompact_ToolPairBoundary validates that a boundary landing inside
// a tool_use/tool_result pair is snapped so the pair stays intact.
func TestCompact_ToolPairBoundary(t *testing.T) {
	// Build a small fixture where index 5 is a tool_use whose
	// tool_result is at index 7. A naive boundary at index 6 would split
	// the pair; the snap helper should push it forward to 8.
	msgs := []SessionMessage{
		{ID: "m0", Role: "user", Content: "u0", Sequence: 0},
		{ID: "m1", Role: "assistant", Content: "a1", Sequence: 1},
		{ID: "m2", Role: "user", Content: "u2", Sequence: 2},
		{ID: "m3", Role: "assistant", Content: "a3", Sequence: 3},
		{ID: "m4", Role: "user", Content: "u4", Sequence: 4},
		{ID: "m5", Role: "assistant", Content: "a5", Sequence: 5, ToolUseID: "tu-1"},
		{ID: "m6", Role: "user", Content: "u6", Sequence: 6}, // filler between use/result
		{ID: "m7", Role: "tool", Content: "t7", Sequence: 7, ToolResultForID: "tu-1"},
		{ID: "m8", Role: "user", Content: "u8", Sequence: 8},
		{ID: "m9", Role: "assistant", Content: "a9", Sequence: 9},
	}

	// Direct snap helper test — easier to reason about than indirect via
	// Compact's running-sum.
	for _, tc := range []struct {
		name string
		idx  int
		want int
	}{
		{"before pair", 5, 5},    // boundary at the tool_use itself: pair stays whole on the kept side
		{"middle of pair", 6, 8}, // boundary between use and result: snap to past the result
		{"at result", 7, 8},      // boundary at the result row: snap to past the result
		{"after pair", 8, 8},     // boundary already past the pair: no change
		{"end of slice", 10, 10}, // boundary at len: no change
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := snapBoundaryForToolPairs(msgs, tc.idx)
			if got != tc.want {
				t.Fatalf("snapBoundaryForToolPairs(%d) = %d, want %d", tc.idx, got, tc.want)
			}
		})
	}

	// Now drive an end-to-end Compact run that lands a boundary inside
	// the pair to confirm the snap is wired up. Tokens-per-msg = 2 (each
	// content is 2 chars). Total = 20. oldestPct = 0.30 → target = 6 →
	// boundary lands at 3 under the deterministic counter, but we want
	// to land it at 6 to test the pair clamp. Use 0.65 → target = 13 →
	// boundary at 7 (after 6 messages add up to 12, the 7th brings it
	// to 14 which clears 13). 7 lands at the result row → snap to 8.
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	if _, err := eng.Compact(context.Background(), "sess-tool", ProviderProfileRef{}, 0.65); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	originals := store.applyCalls[0].originalIDs
	// Boundary should be 8 → originals = m0..m7. Critically, m7 (the
	// tool_result) MUST be present; orphaning it would be the bug.
	if len(originals) != 8 {
		t.Fatalf("originalIDs len = %d, want 8", len(originals))
	}
	if originals[7] != "m7" {
		t.Errorf("originalIDs[7] = %q, want m7 (the tool_result)", originals[7])
	}
}

// TestCompact_RecentWindowClamp asserts that a high oldestPct on a
// small history gets clamped by the recent-window protection: the
// engine returns a no-op (empty id, nil error) and emits the documented
// compression_ratio = 1.0 audit.
func TestCompact_RecentWindowClamp(t *testing.T) {
	// Six user-tagged messages. recentWindow = 4 must leave the last 4
	// user turns untouched. oldestPct = 0.95 wants to compact almost
	// everything → snap pulls the boundary to keep 4 user turns → with
	// only 6 user messages total, only the first 2 are eligible. But
	// the test wants the no-op path: use a fixture with exactly 4 user
	// turns so the clamp pulls boundary to 0.
	msgs := []SessionMessage{
		{ID: "m0", Role: "user", Content: "u0", Sequence: 0},
		{ID: "m1", Role: "assistant", Content: "a1", Sequence: 1},
		{ID: "m2", Role: "user", Content: "u2", Sequence: 2},
		{ID: "m3", Role: "assistant", Content: "a3", Sequence: 3},
		{ID: "m4", Role: "user", Content: "u4", Sequence: 4},
		{ID: "m5", Role: "assistant", Content: "a5", Sequence: 5},
		{ID: "m6", Role: "user", Content: "u6", Sequence: 6},
		{ID: "m7", Role: "assistant", Content: "a7", Sequence: 7},
	}
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 4)

	id, err := eng.Compact(context.Background(), "sess-rw", ProviderProfileRef{}, 0.95)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty summary id on no-op, got %q", id)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0 (no-op)", got)
	}
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0 (no-op)", got)
	}

	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindSessionCompacted {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindSessionCompacted)
	}
	pl := em.events[0].payload.(audit.SessionCompactedPayload)
	if pl.CompressionRatio != 1.0 {
		t.Errorf("no-op CompressionRatio = %f, want 1.0", pl.CompressionRatio)
	}
}

// TestCompact_LLMErrorPath asserts originals stay untouched when the
// LLM call fails, and the audit records KindCompactionFailed.
func TestCompact_LLMErrorPath(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	wantErr := errors.New("provider tantrum")
	llm := &fakeLLM{err: wantErr}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess-fail", ProviderProfileRef{}, 0.30)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Compact err = %v, want %v", err, wantErr)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0 (originals must be untouched on failure)", got)
	}

	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindCompactionFailed {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindCompactionFailed)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "provider_error" {
		t.Errorf("ErrorKind = %q, want provider_error", pl.ErrorKind)
	}
}

// TestCompact_LLMCapExceededClassified asserts a "context_length_exceeded"
// provider error is classified as cap_exceeded in the audit and is
// NOT retried by the engine (one LLM call only).
func TestCompact_LLMCapExceededClassified(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{err: errors.New("provider says: context_length_exceeded for model X")}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess-cap", ProviderProfileRef{}, 0.30)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got := len(llm.calls); got != 1 {
		t.Errorf("LLM calls = %d, want 1 (cap-exceeded must NOT retry)", got)
	}
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "cap_exceeded" {
		t.Errorf("ErrorKind = %q, want cap_exceeded", pl.ErrorKind)
	}
}

// TestCompact_CapabilityLookupUnknown asserts that when the capability
// lookup says ok=false (descriptor unknown), the pre-flight is skipped
// and any subsequent LLM cap-exceeded error is still classified.
func TestCompact_CapabilityLookupUnknown(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{err: errors.New("max context exceeded")}
	caps := fakeCapabilities{max: 0, ok: false}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess-unk", ProviderProfileRef{}, 0.30)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// LLM should have been called exactly once (pre-flight skipped).
	if got := len(llm.calls); got != 1 {
		t.Errorf("LLM calls = %d, want 1", got)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "cap_exceeded" {
		t.Errorf("ErrorKind = %q, want cap_exceeded", pl.ErrorKind)
	}
}

// TestCompact_AuditNilTolerant asserts the engine does not panic when
// no audit emitter is wired up. (Audit must never be a hard dependency.)
func TestCompact_AuditNilTolerant(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	eng, err := NewSessionEngine(SessionEngineConfig{
		Store:        store,
		LLM:          llm,
		Capabilities: caps,
		Audit:        nil, // explicit
		RecentWindow: func() int { return 0 },
		Tokenizer:    fakeTokenizer{},
	})
	if err != nil {
		t.Fatalf("NewSessionEngine: %v", err)
	}
	if _, err := eng.Compact(context.Background(), "sess", ProviderProfileRef{}, 0.30); err != nil {
		t.Fatalf("Compact with nil audit: %v", err)
	}
}

// TestCompact_StoreErrorBeforeCall asserts a list-active-messages error
// is bubbled untouched and emits the storage-error classifier.
func TestCompact_StoreErrorBeforeCall(t *testing.T) {
	wantErr := errors.New("disk on fire")
	store := &fakeStore{listErr: wantErr}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess", ProviderProfileRef{}, 0.30)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Compact err = %v, want %v", err, wantErr)
	}
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "store_error" {
		t.Errorf("ErrorKind = %q, want store_error", pl.ErrorKind)
	}
}

// TestCompact_PersistErrorAfterLLM asserts the audit distinguishes a
// persistence failure (after a successful LLM call) from an LLM-side
// failure. Originals are NOT marked archived because the transaction
// failed.
func TestCompact_PersistErrorAfterLLM(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture(), applyErr: errors.New("tx aborted")}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess", ProviderProfileRef{}, 0.30)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "persist_error" {
		t.Errorf("ErrorKind = %q, want persist_error", pl.ErrorKind)
	}
}

// TestCompact_ZeroOldestPct asserts the trivial no-op path: oldestPct=0
// does no I/O and still emits the documented audit.
func TestCompact_ZeroOldestPct(t *testing.T) {
	store := &fakeStore{messages: build100MessageFixture()}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id, err := eng.Compact(context.Background(), "sess", ProviderProfileRef{}, 0)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty summary id, got %q", id)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0", got)
	}
}

// TestSnapBoundaryForRecentWindow_Direct exercises the helper directly
// with a few clear cases.
func TestSnapBoundaryForRecentWindow_Direct(t *testing.T) {
	msgs := []SessionMessage{
		{ID: "m0", Role: "user"},
		{ID: "m1", Role: "assistant"},
		{ID: "m2", Role: "user"},
		{ID: "m3", Role: "assistant"},
		{ID: "m4", Role: "user"},
		{ID: "m5", Role: "assistant"},
	}
	for _, tc := range []struct {
		name   string
		idx    int
		window int
		want   int
	}{
		{"window 0 disables", 4, 0, 4},
		{"already enough", 2, 1, 2}, // tail = m2..m5 carries 2 user turns >= 1
		{"shrink to expose 2", 4, 2, 2},
		{"shrink to expose 3", 5, 3, 0},
		{"idx already 0", 0, 5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := snapBoundaryForRecentWindow(msgs, tc.idx, tc.window)
			if got != tc.want {
				t.Fatalf("snapBoundaryForRecentWindow(%d, window=%d) = %d, want %d", tc.idx, tc.window, got, tc.want)
			}
		})
	}
}

// TestNewEngine_RequiresDeps asserts the constructor's dependency
// validation. Missing required fields should fail-fast at construction.
func TestNewEngine_RequiresDeps(t *testing.T) {
	cases := []struct {
		name string
		cfg  SessionEngineConfig
	}{
		{"missing store", SessionEngineConfig{LLM: &fakeLLM{}, Capabilities: fakeCapabilities{}}},
		{"missing llm", SessionEngineConfig{Store: &fakeStore{}, Capabilities: fakeCapabilities{}}},
		{"missing capabilities", SessionEngineConfig{Store: &fakeStore{}, LLM: &fakeLLM{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSessionEngine(tc.cfg); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

// TestCompact_BackwardClampIsLoadBearing pins engine.Compact's step 4b —
// the backward pair clamp that runs AFTER the recent-window clamp.
//
// WHY IT NEEDS ITS OWN TEST. Step 4b was shipped with three separate
// "mutation evidence" comments claiming its deletion would fail a named
// test. Re-running that mutation on the merged tree failed NOTHING, in
// any package: the sqlite sweep's percentages never produce a boundary
// the recent-window clamp then drags back into a pair, and
// TestSnapClampsCompose composes the helpers by hand and so cannot
// observe whether the ENGINE calls the third one.
//
// The shape below is the one that separates two clamps from three: the
// recent-window clamp always comes to rest on a user row (it decrements
// until the user count clears, and the row that clears it is a user
// row), so the only way it lands inside a pair is if a user row sits
// inside one. This asserts against the archived set the engine actually
// wrote, not against a boundary integer.
//
// Mutation evidence: delete `boundary = snapBoundaryBackForToolPairs(...)`
// from Compact and this fails at recentWindow=1.
func TestCompact_BackwardClampIsLoadBearing(t *testing.T) {
	t.Parallel()
	msgs := []SessionMessage{
		{ID: "m0", Role: "user", Content: "first question", Sequence: 0},
		{ID: "use-A", Role: "tool", Content: "bash(cmd=<string>)", Sequence: 1, ToolUseID: "A"},
		{ID: "m2", Role: "user", Content: "an interjection", Sequence: 2},
		{ID: "res-A", Role: "tool", Content: "the tool output", Sequence: 3, ToolResultForID: "A"},
		{ID: "m4", Role: "assistant", Content: "the final answer", Sequence: 4},
	}

	for _, rw := range []int{0, 1, 2, 3} {
		for pctTenths := 1; pctTenths <= 10; pctTenths++ {
			pct := float64(pctTenths) / 10
			store := &fakeStore{messages: msgs}
			eng, err := NewSessionEngine(SessionEngineConfig{
				Store:        store,
				LLM:          &fakeLLM{text: "summary"},
				Capabilities: fakeCapabilities{max: 1 << 20, ok: true},
				Tokenizer:    fakeTokenizer{},
				RecentWindow: func() int { return rw },
				Now:          func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
			})
			if err != nil {
				t.Fatalf("NewSessionEngine: %v", err)
			}
			if _, err := eng.Compact(context.Background(), "s",
				ProviderProfileRef{ProviderID: "p", ModelID: "m"}, pct); err != nil {
				t.Fatalf("rw=%d pct=%.1f: Compact: %v", rw, pct, err)
			}
			if len(store.applyCalls) == 0 {
				continue // no-op compaction; nothing was archived
			}
			archived := map[string]bool{}
			for _, id := range store.applyCalls[0].originalIDs {
				archived[id] = true
			}
			if archived["use-A"] != archived["res-A"] {
				t.Errorf("rw=%d pct=%.1f: archived use-A=%v res-A=%v — the span boundary "+
					"cut through an intact tool_use/tool_result pair, leaving the orphan "+
					"half that step 4b exists to prevent",
					rw, pct, archived["use-A"], archived["res-A"])
			}
		}
	}
}
