package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// rolling_test.go drives the maximal-mode rolling-summary path
// (RollingSummarize) through the same fakes WP03's engine_test.go
// uses. We DO NOT redefine those fakes — the tests below reuse
// fakeStore / fakeLLM / fakeCapabilities / fakeAudit / fakeTokenizer
// from engine_test.go since both files compile into the same test
// binary.
//
// The acceptance criteria from tasks.md WP04 drive the test set:
//   - First-roll: empty session → 6 pairs added → tail = last 4 pairs;
//     older 2 pairs collapse into one rolling summary.
//   - Subsequent-roll: after a first-roll state, 4 more pairs added;
//     the previous rolling summary + the newly-rolled turns collapse
//     into ONE fresh rolling summary; previous summary is archived.
//   - No-op tick: nothing past the tail; no LLM call, no audit-failed,
//     no archival.
//   - Model-too-small graceful-degrade: pile + framing exceeds the
//     compaction model cap; typed error returned, no LLM call, no
//     archive.
//   - Tool-pair preservation: window math never splits a tool pair.
//   - LLM error path: provider error returns + emits failed audit;
//     originals untouched.

// buildPairsFixture constructs N user-assistant pairs as a 2N-message
// fixture. Each message's content is a 10-char "u-NNN  " or "a-NNN  "
// token-stable string so tokenizer math stays predictable. Sequences
// are 0..2N-1 ascending.
func buildPairsFixture(numPairs int) []Message {
	out := make([]Message, 0, numPairs*2)
	for i := 0; i < numPairs; i++ {
		out = append(out, Message{
			ID:       fmt.Sprintf("u-%03d", i),
			Role:     "user",
			Content:  fmt.Sprintf("u-%03d-cnt", i),
			Sequence: int64(i * 2),
		})
		out = append(out, Message{
			ID:       fmt.Sprintf("a-%03d", i),
			Role:     "assistant",
			Content:  fmt.Sprintf("a-%03d-cnt", i),
			Sequence: int64(i*2 + 1),
		})
	}
	return out
}

// TestFindRecentWindowStart_Direct exercises the helper directly so
// the math is pinned independent of the engine wrapper.
func TestFindRecentWindowStart_Direct(t *testing.T) {
	msgs6 := buildPairsFixture(6)
	for _, tc := range []struct {
		name   string
		msgs   []Message
		window int
		want   int
	}{
		{"6 pairs, window 4", msgs6, 4, 4},   // tail starts at user index 4 (= 5th msg, 0-based)
		{"6 pairs, window 6", msgs6, 6, 0},   // window = total → tail covers all
		{"6 pairs, window 7", msgs6, 7, 0},   // window > pairs → tail covers all
		{"6 pairs, window 1", msgs6, 1, 10},  // last user msg index
		{"empty, window 4", []Message{}, 4, 0},
		{"6 pairs, window 0", msgs6, 0, len(msgs6)},
		{"6 pairs, window -1", msgs6, -1, len(msgs6)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := findRecentWindowStart(tc.msgs, tc.window)
			if got != tc.want {
				t.Fatalf("findRecentWindowStart = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRollingSummarize_FirstRoll covers the headline first-roll
// acceptance: 6 pairs, recentWindow=4, expect msgs 0..3 (older 2
// pairs) folded into a single rolling-summary row whose Content
// carries the rolling prefix and whose Sequence sits at the lowest
// archived sequence.
func TestRollingSummarize_FirstRoll(t *testing.T) {
	msgs := buildPairsFixture(6)
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{
		text:         "rolling-text",
		outputTokens: 5,
	}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	model := ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"}
	id, err := eng.RollingSummarize(context.Background(), "sess-rolling", model, 4)
	if err != nil {
		t.Fatalf("RollingSummarize: %v", err)
	}
	if id == "" {
		t.Fatalf("RollingSummarize returned empty id on a non-no-op run")
	}

	// One ApplyCompaction call.
	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	call := store.applyCalls[0]

	// Originals = msgs[0..3] = u-000, a-000, u-001, a-001 (the older 2
	// pairs). The recent 4 pairs (msgs[4..11]) stay live.
	wantOriginals := []string{"u-000", "a-000", "u-001", "a-001"}
	if got := len(call.originalIDs); got != len(wantOriginals) {
		t.Fatalf("originalIDs len = %d, want %d", got, len(wantOriginals))
	}
	for i, want := range wantOriginals {
		if call.originalIDs[i] != want {
			t.Errorf("originalIDs[%d] = %q, want %q", i, call.originalIDs[i], want)
		}
	}

	// Summary row asserts.
	if call.summary.Role != "system" {
		t.Errorf("summary role = %q, want system", call.summary.Role)
	}
	if !strings.HasPrefix(call.summary.Content, rollingSummaryContentPrefix) {
		t.Errorf("summary missing rolling prefix: %q", call.summary.Content)
	}
	if !strings.HasSuffix(call.summary.Content, rollingSummaryContentSuffix) {
		t.Errorf("summary missing rolling suffix: %q", call.summary.Content)
	}
	if !strings.Contains(call.summary.Content, "rolling-text") {
		t.Errorf("summary missing LLM text: %q", call.summary.Content)
	}
	// Critically: the rolling prefix MUST differ from threshold prefix,
	// so a content sniffer can disambiguate.
	if strings.HasPrefix(call.summary.Content, summaryContentPrefix) {
		t.Errorf("rolling summary content unexpectedly matches threshold prefix: %q", call.summary.Content)
	}
	if call.summary.Sequence != 0 {
		t.Errorf("summary sequence = %d, want 0 (lowest of archived span)", call.summary.Sequence)
	}
	if call.summary.ID != id {
		t.Errorf("returned id %q != stored summary id %q", id, call.summary.ID)
	}

	// LLM was called exactly once with the rolling-prompt body.
	if got := len(llm.calls); got != 1 {
		t.Fatalf("LLM calls = %d, want 1", got)
	}
	if !strings.Contains(llm.calls[0].userPrompt, "<rolling_pile>") {
		t.Errorf("user prompt missing rolling-pile framing: %q", llm.calls[0].userPrompt)
	}
	if !strings.Contains(llm.calls[0].userPrompt, "u-000-cnt") {
		t.Errorf("user prompt missing rolled-turn content: %q", llm.calls[0].userPrompt)
	}

	// Audit recorded one KindSessionCompacted with maximal tier.
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
	if pl.AggressivenessTier != "maximal" {
		t.Errorf("AggressivenessTier = %q, want maximal", pl.AggressivenessTier)
	}
	if pl.SessionID != "sess-rolling" {
		t.Errorf("SessionID = %q, want sess-rolling", pl.SessionID)
	}
	if pl.TokensAfterSummary != 5 {
		t.Errorf("TokensAfterSummary = %d, want 5", pl.TokensAfterSummary)
	}
}

// TestRollingSummarize_SubsequentRoll covers the second tick: after a
// first-roll state (one rolling summary + 4 recent pairs), 4 more
// pairs land. The next call must collapse the previous rolling summary
// + 4 newly-displaced pairs into ONE fresh rolling summary, archiving
// the old summary along with the newly-rolled turns.
func TestRollingSummarize_SubsequentRoll(t *testing.T) {
	// Build the post-first-roll state by hand: prevRolling at sequence
	// 0, then recent 4 pairs (msgs[4..11] from the original fixture)
	// at sequences 4..11, then 4 new pairs at sequences 12..19.
	prevRolling := Message{
		ID:       "prev-roll",
		Role:     "system",
		Content:  rollingSummaryContentPrefix + "old summary text" + rollingSummaryContentSuffix,
		Sequence: 0,
	}
	recent := buildPairsFixture(6)[4:] // msgs[4..11] of the 12-msg fixture
	// Renumber recent to sequences 4..11 (matches the post-roll state).
	for i := range recent {
		recent[i].Sequence = int64(4 + i)
	}
	// Add 4 new pairs at sequences 12..19.
	newer := buildPairsFixture(4)
	for i := range newer {
		newer[i].ID = fmt.Sprintf("new-%s", newer[i].ID)
		newer[i].Sequence = int64(12 + i)
	}

	active := append([]Message{prevRolling}, recent...)
	active = append(active, newer...)

	store := &fakeStore{messages: active}
	llm := &fakeLLM{text: "rolled-again", outputTokens: 7}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id, err := eng.RollingSummarize(context.Background(), "sess-2", ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("RollingSummarize: %v", err)
	}
	if id == "" {
		t.Fatalf("expected new summary id, got empty")
	}

	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	call := store.applyCalls[0]

	// The recent 4 user-assistant pairs in the new state are the four
	// pairs in `newer` (sequences 12..19). Tail starts at the first
	// new user message, which is at active index 1+8 = 9. That means
	// active[0..8] gets archived: prevRolling + the 8 messages of the
	// previous "recent" block.
	wantArchivedCount := 9
	if got := len(call.originalIDs); got != wantArchivedCount {
		t.Fatalf("originalIDs len = %d, want %d (prev summary + 4 displaced pairs)", got, wantArchivedCount)
	}
	// Critical invariant: the previous rolling summary id MUST be in
	// the archive set, otherwise we'd carry two active rolling
	// summaries forward.
	foundPrev := false
	for _, oid := range call.originalIDs {
		if oid == "prev-roll" {
			foundPrev = true
			break
		}
	}
	if !foundPrev {
		t.Errorf("originalIDs missing prev-roll: %v", call.originalIDs)
	}

	// Rolling pile prompt must include the previous summary verbatim
	// (so the model sees its prior compressed context).
	if got := len(llm.calls); got != 1 {
		t.Fatalf("LLM calls = %d, want 1", got)
	}
	if !strings.Contains(llm.calls[0].userPrompt, "old summary text") {
		t.Errorf("user prompt missing previous summary text: %q", llm.calls[0].userPrompt)
	}

	// New summary content carries the rolling prefix and the new LLM
	// text — and only one rolling summary survives logically (the
	// previous one is archived).
	if !strings.HasPrefix(call.summary.Content, rollingSummaryContentPrefix) {
		t.Errorf("summary missing rolling prefix: %q", call.summary.Content)
	}
	if !strings.Contains(call.summary.Content, "rolled-again") {
		t.Errorf("summary missing new LLM text: %q", call.summary.Content)
	}
	if call.summary.Sequence != 0 {
		t.Errorf("summary sequence = %d, want 0 (lowest of archived span)", call.summary.Sequence)
	}
	if call.summary.ID == "prev-roll" {
		t.Errorf("new summary id collides with archived prev-roll")
	}
}

// TestRollingSummarize_NoOpTick covers the case where the entire
// active history fits in the recent window — there's nothing to roll.
// The function must return ("", nil), do no LLM call, and emit one
// no-op KindSessionCompacted audit.
func TestRollingSummarize_NoOpTick(t *testing.T) {
	// 4 pairs = exactly recentWindow. tailStart = 0 → nothing to roll.
	msgs := buildPairsFixture(4)
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id, err := eng.RollingSummarize(context.Background(), "sess-noop", ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("RollingSummarize: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id on no-op, got %q", id)
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
	if pl.AggressivenessTier != "maximal" {
		t.Errorf("AggressivenessTier = %q, want maximal", pl.AggressivenessTier)
	}
}

// TestRollingSummarize_NoOpEmptySession covers the trivial empty
// session path: zero messages → no-op.
func TestRollingSummarize_NoOpEmptySession(t *testing.T) {
	store := &fakeStore{messages: nil}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id, err := eng.RollingSummarize(context.Background(), "sess-empty", ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("RollingSummarize: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id, got %q", id)
	}
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0", got)
	}
}

// TestRollingSummarize_ModelTooSmall covers the graceful-degrade path:
// the rolling pile + framing exceeds the compaction model cap. The
// function must return *ErrCompactionModelTooSmall carrying both
// numbers, never call the LLM, and never write to the store. The
// chat runner (WP08) will catch this and silently treat the turn as
// aggressive tier.
func TestRollingSummarize_ModelTooSmall(t *testing.T) {
	msgs := buildPairsFixture(6)
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 50, ok: true} // tiny cap — pile + framing busts it
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 4)

	_, err := eng.RollingSummarize(context.Background(), "sess-toosmall", ProviderProfileRef{}, 4)
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
		t.Errorf("NeedsTokens = %d, want > 50", typed.NeedsTokens)
	}
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0 (pre-flight should reject before any wire call)", got)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0 (originals must be untouched)", got)
	}

	// Audit must record one KindCompactionFailed with model_too_small.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindCompactionFailed {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindCompactionFailed)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "model_too_small" {
		t.Errorf("ErrorKind = %q, want model_too_small", pl.ErrorKind)
	}
	if pl.AggressivenessTier != "maximal" {
		t.Errorf("AggressivenessTier = %q, want maximal", pl.AggressivenessTier)
	}
}

// TestRollingSummarize_ToolPairPreservation covers the invariant that
// window math never splits a tool_use/tool_result pair. We build a
// fixture where the recent-window math would naively land tailStart
// inside a tool exchange; the snap helper must push it forward so the
// pair stays whole on the live-tail side.
func TestRollingSummarize_ToolPairPreservation(t *testing.T) {
	// 6 pairs but with a tool exchange straddling the natural tail
	// boundary. Naive recentWindow=4 lands tailStart at index 4 (the
	// 5th message, which is "user" of pair-2). We seed a tool_use at
	// index 3 (assistant of pair-1) whose tool_result lands at index
	// 5 — tailStart of 4 splits the pair → snap must push tailStart
	// to 6.
	msgs := []Message{
		{ID: "u0", Role: "user", Content: "u0", Sequence: 0},
		{ID: "a0", Role: "assistant", Content: "a0", Sequence: 1},
		{ID: "u1", Role: "user", Content: "u1", Sequence: 2},
		{ID: "a1", Role: "assistant", Content: "a1", Sequence: 3, ToolUseID: "tu-x"},
		{ID: "u2", Role: "user", Content: "u2", Sequence: 4}, // would-be tailStart
		{ID: "tr", Role: "tool", Content: "tr", Sequence: 5, ToolResultForID: "tu-x"},
		{ID: "u3", Role: "user", Content: "u3", Sequence: 6},
		{ID: "a3", Role: "assistant", Content: "a3", Sequence: 7},
		{ID: "u4", Role: "user", Content: "u4", Sequence: 8},
		{ID: "a4", Role: "assistant", Content: "a4", Sequence: 9},
		{ID: "u5", Role: "user", Content: "u5", Sequence: 10},
		{ID: "a5", Role: "assistant", Content: "a5", Sequence: 11},
	}

	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	if _, err := eng.RollingSummarize(context.Background(), "sess-tp", ProviderProfileRef{}, 4); err != nil {
		t.Fatalf("RollingSummarize: %v", err)
	}
	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	originals := store.applyCalls[0].originalIDs
	// Snap must produce originals = msgs[0..5] = u0,a0,u1,a1,u2,tr.
	// Critically: the tool_result "tr" MUST be in the archive set
	// alongside its opener "a1" — splitting them would orphan the
	// tool_result on the live side.
	if len(originals) != 6 {
		t.Fatalf("originalIDs len = %d, want 6 (snap must push past tool_result): %v", len(originals), originals)
	}
	foundOpener, foundCloser := false, false
	for _, oid := range originals {
		if oid == "a1" {
			foundOpener = true
		}
		if oid == "tr" {
			foundCloser = true
		}
	}
	if !foundOpener || !foundCloser {
		t.Errorf("tool pair must stay whole; originals = %v", originals)
	}
}

// TestRollingSummarize_LLMErrorPath asserts originals stay untouched
// when the LLM fails, and the audit records KindCompactionFailed
// (with the rolling tier tag).
func TestRollingSummarize_LLMErrorPath(t *testing.T) {
	msgs := buildPairsFixture(6)
	store := &fakeStore{messages: msgs}
	wantErr := errors.New("provider tantrum")
	llm := &fakeLLM{err: wantErr}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.RollingSummarize(context.Background(), "sess-fail", ProviderProfileRef{}, 4)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RollingSummarize err = %v, want %v", err, wantErr)
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
	if pl.AggressivenessTier != "maximal" {
		t.Errorf("AggressivenessTier = %q, want maximal", pl.AggressivenessTier)
	}
}

// TestRollingSummarize_StoreListError covers the I/O failure path
// before any work happens — bubble untouched, classify as store_error.
func TestRollingSummarize_StoreListError(t *testing.T) {
	wantErr := errors.New("disk on fire")
	store := &fakeStore{listErr: wantErr}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.RollingSummarize(context.Background(), "sess", ProviderProfileRef{}, 4)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RollingSummarize err = %v, want %v", err, wantErr)
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "store_error" {
		t.Errorf("ErrorKind = %q, want store_error", pl.ErrorKind)
	}
}

// TestRollingSummarize_PersistError mirrors the threshold-mode
// PersistErrorAfterLLM test: tx aborted after a successful LLM call
// gets the persist_error classifier so audit log distinguishes it
// from an LLM-side failure.
func TestRollingSummarize_PersistError(t *testing.T) {
	msgs := buildPairsFixture(6)
	store := &fakeStore{messages: msgs, applyErr: errors.New("tx aborted")}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	if _, err := eng.RollingSummarize(context.Background(), "sess", ProviderProfileRef{}, 4); err == nil {
		t.Fatalf("expected error, got nil")
	}
	pl := em.events[0].payload.(audit.CompactionFailedPayload)
	if pl.ErrorKind != "persist_error" {
		t.Errorf("ErrorKind = %q, want persist_error", pl.ErrorKind)
	}
}

// TestRollingSummarize_AuditNilTolerant asserts a nil emitter doesn't
// panic. (Audit must never be a hard dependency.)
func TestRollingSummarize_AuditNilTolerant(t *testing.T) {
	msgs := buildPairsFixture(6)
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "ok", outputTokens: 1}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	eng, err := NewEngine(EngineConfig{
		Store:        store,
		LLM:          llm,
		Capabilities: caps,
		Audit:        nil,
		RecentWindow: func() int { return 0 },
		Tokenizer:    fakeTokenizer{},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := eng.RollingSummarize(context.Background(), "sess", ProviderProfileRef{}, 4); err != nil {
		t.Fatalf("RollingSummarize with nil audit: %v", err)
	}
}

// TestRollingSummarize_OnlyOneActiveSummaryAtATime is a stronger form
// of the subsequent-roll invariant: across two rolls in sequence, the
// store should NEVER carry more than one row whose Content begins
// with the rolling-summary prefix at any point. The first apply
// archives no prior summary; the second apply MUST archive the first
// summary alongside the new turns.
func TestRollingSummarize_OnlyOneActiveSummaryAtATime(t *testing.T) {
	// Round 1: empty-prior, 6 pairs in.
	store := &fakeStore{messages: buildPairsFixture(6)}
	llm := &fakeLLM{text: "round-1", outputTokens: 5}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id1, err := eng.RollingSummarize(context.Background(), "sess", ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("round 1 RollingSummarize: %v", err)
	}
	round1 := store.applyCalls[0]
	for _, oid := range round1.originalIDs {
		if strings.HasPrefix(oid, "cmp-") {
			t.Errorf("round 1 unexpectedly archived a synthetic summary id: %q", oid)
		}
	}

	// Construct round-2 active state: round-1 summary at sequence 0,
	// recent 4 pairs from the original fixture (msgs[4..11]) at
	// sequences 4..11, plus 4 new pairs at sequences 12..19.
	prevSummary := round1.summary
	prevSummary.Sequence = 0

	recent := buildPairsFixture(6)[4:]
	for i := range recent {
		recent[i].Sequence = int64(4 + i)
	}
	newer := buildPairsFixture(4)
	for i := range newer {
		newer[i].ID = fmt.Sprintf("new-%s", newer[i].ID)
		newer[i].Sequence = int64(12 + i)
	}
	round2State := append([]Message{prevSummary}, recent...)
	round2State = append(round2State, newer...)

	store.messages = round2State
	store.applyCalls = nil
	llm.text = "round-2"
	llm.outputTokens = 6

	id2, err := eng.RollingSummarize(context.Background(), "sess", ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("round 2 RollingSummarize: %v", err)
	}
	if id1 == id2 {
		t.Errorf("round 2 reused round 1 id: %q", id1)
	}
	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("round 2 ApplyCompaction calls = %d, want 1", got)
	}
	round2 := store.applyCalls[0]

	foundPrev := false
	for _, oid := range round2.originalIDs {
		if oid == id1 {
			foundPrev = true
			break
		}
	}
	if !foundPrev {
		t.Errorf("round 2 did not archive round 1 summary id %q; originals = %v", id1, round2.originalIDs)
	}

	// And the new summary must sit at the head sequence (lowest seq
	// in the archived span = 0, the prev-summary's slot).
	if round2.summary.Sequence != 0 {
		t.Errorf("round 2 summary sequence = %d, want 0", round2.summary.Sequence)
	}
}
