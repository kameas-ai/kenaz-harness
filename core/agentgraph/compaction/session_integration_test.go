package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// session_integration_test.go drives the compaction engine end-to-end through
// the same engine surface a production chassis would: real SessionEngine
// constructed via NewSessionEngine, in-memory message store + canned-response
// LLM + canned capability lookup + recording audit emitter. The tests
// pin the acceptance-smoke checklist from plan §4 (items 1, 3, 4, 5)
// where the engine itself is the relevant boundary; item 2 (frontend
// toggle) and the chat-runner short-circuit on tier=off live in the
// frontend e2e suite and chat_runner_test.go respectively.
//
// We deliberately reuse the fakes declared in session_engine_test.go
// (fakeStore, fakeLLM, fakeCapabilities, fakeAudit, fakeTokenizer,
// newTestEngine, build100MessageFixture). All _test.go files in the
// same package share the test binary so re-declaring them here would
// just be name collisions.
//
// The integration scope here is "engine + its dependency interfaces
// behave as a unit"; chat-runner-through-engine is covered by
// chat_runner_test.go (WP08) where the chat runner pre-send hook is
// the public seam — duplicating that surface in this package would
// pull in the chat package and break the layering rule (compaction
// has no dependency on rpc/* by design).

// buildLargeFixture returns a fixture sized so the running-token sum
// reaches a target percentage of a synthetic cap. Every message
// contributes 10 tokens under fakeTokenizer (10-char content). With
// totalMessages = N the total is 10*N tokens, and the cap argument
// caller passes lets the test tune "we are at K% of cap" deterministically.
func buildLargeFixture(totalMessages int) []SessionMessage {
	out := make([]SessionMessage, totalMessages)
	for i := 0; i < totalMessages; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// Fixed-width 10 char content keeps token math exact.
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

// TestIntegration_ThresholdTier_BalancedFiresAt80Pct mirrors plan §4
// acceptance-smoke item 1: a balanced-tier session that reaches ~80%
// of cap triggers compaction; the originals are archived; a synthetic
// summary row is inserted at the head sequence; the audit log records
// the success.
//
// SessionEngine-scope translation: instead of running 100 chat turns through
// a runner, we drive Compact directly with the SummarizePct the
// balanced tier would produce (0.30) and assert the engine archives
// exactly 30% of the oldest messages while leaving the rest live.
func TestIntegration_ThresholdTier_BalancedFiresAt80Pct(t *testing.T) {
	// 100 messages × 10 tokens = 1000 tokens total.
	store := &fakeStore{messages: buildLargeFixture(100)}
	llm := &fakeLLM{
		text:         "balanced summary",
		outputTokens: 100, // ~10x compression
	}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 4) // realistic recent-window=4

	// Balanced tier: SummarizePct = 0.30 (plan §2.2).
	balanced := compactionpolicy.Tier(compactionpolicy.AggressivenessBalanced)
	if balanced.SummarizePct != 0.30 {
		t.Fatalf("balanced tier SummarizePct drift: got %f, want 0.30", balanced.SummarizePct)
	}

	model := ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"}
	id, err := eng.Compact(context.Background(), "sess-balanced", model, balanced.SummarizePct)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty summary id; engine returned no-op on a 100-message fixture")
	}

	// Exactly one ApplyCompaction call.
	if got := len(store.applyCalls); got != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", got)
	}
	call := store.applyCalls[0]

	// 30% of 1000 tokens = 300, hit at boundary 30 (10 tokens × 30 msgs).
	// recent-window=4 needs 4 user turns at-or-after boundary; messages
	// 30..99 hold 35 user turns (alternating user/assistant), so the
	// clamp is satisfied at 30 without further pull-back.
	if got := len(call.originalIDs); got != 30 {
		t.Fatalf("originalIDs len = %d, want 30 (oldest 30%% of 100 messages)", got)
	}

	// Summary row asserts: synthetic system role, canonical prefix/suffix,
	// sequence at the head of the archived span (= sequence 0).
	if call.summary.Role != "system" {
		t.Errorf("summary role = %q, want system", call.summary.Role)
	}
	if !strings.HasPrefix(call.summary.Content, summaryContentPrefix) {
		t.Errorf("summary missing canonical prefix: %q", call.summary.Content)
	}
	if !strings.Contains(call.summary.Content, "balanced summary") {
		t.Errorf("summary missing LLM text: %q", call.summary.Content)
	}
	if call.summary.Sequence != 0 {
		t.Errorf("summary sequence = %d, want 0 (head of archived span)", call.summary.Sequence)
	}

	// Audit emission shape: KindSessionCompacted + payload carries the
	// span/output token counts for the compression-ratio readout.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindSessionCompacted {
		t.Errorf("audit kind = %q, want %q", em.events[0].kind, audit.KindSessionCompacted)
	}
	pl := em.events[0].payload.(audit.SessionCompactedPayload)
	if pl.SessionID != "sess-balanced" {
		t.Errorf("payload SessionID = %q, want sess-balanced", pl.SessionID)
	}
	if pl.TokensInSpan != 300 {
		t.Errorf("payload TokensInSpan = %d, want 300", pl.TokensInSpan)
	}
	if pl.TokensAfterSummary != 100 {
		t.Errorf("payload TokensAfterSummary = %d, want 100", pl.TokensAfterSummary)
	}
}

// TestIntegration_ThresholdTier_AggressiveFiresAt60Pct mirrors plan §4
// acceptance-smoke item 3: switching to the aggressive tier triggers
// compaction earlier and folds a larger fraction. We pin both numerics
// (SummarizePct=0.40) and the engine's behavior under that fraction.
func TestIntegration_ThresholdTier_AggressiveFiresAt60Pct(t *testing.T) {
	// Aggressive tier: SummarizePct = 0.40 (plan §2.2).
	aggressive := compactionpolicy.Tier(compactionpolicy.AggressivenessAggressive)
	if aggressive.SummarizePct != 0.40 {
		t.Fatalf("aggressive tier SummarizePct drift: got %f, want 0.40", aggressive.SummarizePct)
	}
	if aggressive.TriggerPct != 0.60 {
		t.Fatalf("aggressive tier TriggerPct drift: got %f, want 0.60", aggressive.TriggerPct)
	}

	store := &fakeStore{messages: buildLargeFixture(100)}
	llm := &fakeLLM{
		text:         "aggressive summary",
		outputTokens: 80,
	}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 4)

	id, err := eng.Compact(context.Background(), "sess-aggressive",
		ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"},
		aggressive.SummarizePct)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if id == "" {
		t.Fatalf("expected non-empty summary id")
	}

	// 40% of 1000 tokens = 400, boundary at message 40.
	call := store.applyCalls[0]
	if got := len(call.originalIDs); got != 40 {
		t.Fatalf("originalIDs len = %d, want 40 (oldest 40%% under aggressive tier)", got)
	}

	// Cross-check: aggressive folds STRICTLY MORE than balanced. The
	// invariant "more aggressive tier folds more turns" is the user
	// promise of the dial.
	balancedFold := 30
	if len(call.originalIDs) <= balancedFold {
		t.Errorf("aggressive folded %d turns; expected strictly more than balanced (%d)",
			len(call.originalIDs), balancedFold)
	}
}

// TestIntegration_MaximalTier_RollsEveryCall mirrors the maximal-mode
// invariant from plan §2.5: only one active rolling summary at a time.
// Two consecutive RollingSummarize calls archive the previous summary
// alongside the newly-folded turns.
func TestIntegration_MaximalTier_RollsEveryCall(t *testing.T) {
	// Maximal tier mode flag.
	maximal := compactionpolicy.Tier(compactionpolicy.AggressivenessMaximal)
	if maximal.Mode != compactionpolicy.ModeRolling {
		t.Fatalf("maximal tier mode drift: got %v, want compactionpolicy.ModeRolling", maximal.Mode)
	}

	// Round 1: 6 pairs of user-assistant; recentWindow=4 keeps 4 pairs
	// live, archives the older 2 pairs into one fresh rolling summary.
	round1Store := &fakeStore{messages: buildPairsFixture(6)}
	llm := &fakeLLM{text: "round-1", outputTokens: 5}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, round1Store, llm, caps, em, 0)

	id1, err := eng.RollingSummarize(context.Background(), "sess-maximal",
		ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("round 1 RollingSummarize: %v", err)
	}
	if id1 == "" {
		t.Fatalf("round 1 returned empty id")
	}
	if got := len(round1Store.applyCalls); got != 1 {
		t.Fatalf("round 1 ApplyCompaction calls = %d, want 1", got)
	}

	// Round 2: build active state = round-1 summary + 4 recent pairs +
	// 4 new pairs; reuse the engine. Critical assertion: round-2
	// archive set MUST include the round-1 summary id; otherwise we'd
	// carry two active rolling summaries forward.
	prevSummary := round1Store.applyCalls[0].summary
	prevSummary.Sequence = 0

	recent := buildPairsFixture(6)[4:] // last 4 of original 6 pairs
	for i := range recent {
		recent[i].Sequence = int64(4 + i)
	}
	newer := buildPairsFixture(4)
	for i := range newer {
		newer[i].ID = fmt.Sprintf("new-%s", newer[i].ID)
		newer[i].Sequence = int64(12 + i)
	}
	round2State := append([]SessionMessage{prevSummary}, recent...)
	round2State = append(round2State, newer...)

	round1Store.messages = round2State
	round1Store.applyCalls = nil
	llm.text = "round-2"
	llm.outputTokens = 6

	id2, err := eng.RollingSummarize(context.Background(), "sess-maximal",
		ProviderProfileRef{}, 4)
	if err != nil {
		t.Fatalf("round 2 RollingSummarize: %v", err)
	}
	if id1 == id2 {
		t.Errorf("round 2 reused round 1 id %q (must be a fresh id)", id1)
	}
	if got := len(round1Store.applyCalls); got != 1 {
		t.Fatalf("round 2 ApplyCompaction calls = %d, want 1", got)
	}

	round2 := round1Store.applyCalls[0]
	foundPrev := false
	for _, oid := range round2.originalIDs {
		if oid == id1 {
			foundPrev = true
			break
		}
	}
	if !foundPrev {
		t.Errorf("round 2 did not archive round 1 summary id %q; originals = %v",
			id1, round2.originalIDs)
	}

	// New summary lives at head sequence = 0 (the slot the prev summary
	// occupied).
	if round2.summary.Sequence != 0 {
		t.Errorf("round 2 summary sequence = %d, want 0", round2.summary.Sequence)
	}

	// Both rounds emitted a KindSessionCompacted with the maximal tier tag.
	if got := len(em.events); got != 2 {
		t.Fatalf("audit events = %d, want 2 (one per round)", got)
	}
	for i, ev := range em.events {
		if ev.kind != audit.KindSessionCompacted {
			t.Errorf("events[%d].kind = %q, want %q", i, ev.kind, audit.KindSessionCompacted)
		}
		pl := ev.payload.(audit.SessionCompactedPayload)
		if pl.AggressivenessTier != "maximal" {
			t.Errorf("events[%d].AggressivenessTier = %q, want maximal", i, pl.AggressivenessTier)
		}
	}
}

// TestIntegration_OffTier_EngineIsBypassed pins plan §4 acceptance-smoke
// item 4 at the engine layer: when aggressiveness=off, nothing invokes
// Compact / RollingSummarize at all, because Tier(off) reports ModeNone.
// The dispatching seam is unit-tested in chat_runner_test.go; here we
// assert the policy invariant that seam reads.
//
// The integration assertion is "Tier(off) returns ModeNone with zero
// TriggerPct/SummarizePct" — a caller reading these values has no path
// that schedules a compaction call.
//
// This matters more than it looks. The tier dial is now a strategy
// (StrategySessionRewrite) reached through the FR-041 pipeline from the
// `compact` node, and the "off" tier is deliberately still *enabled* at
// that site: "off" does not mean "don't run", it means "don't compact,
// and say so honestly when the session is genuinely full". The strategy
// therefore runs on every turn at this tier and decides to do nothing
// by reading exactly these numerics. If a regression flipped any of
// them, that strategy would silently start compacting sessions whose
// owner asked it not to.
func TestIntegration_OffTier_EngineIsBypassed(t *testing.T) {
	off := compactionpolicy.Tier(compactionpolicy.AggressivenessOff)
	if off.Mode != compactionpolicy.ModeNone {
		t.Fatalf("off tier mode drift: got %v, want compactionpolicy.ModeNone", off.Mode)
	}
	if off.TriggerPct != 0 {
		t.Errorf("off tier TriggerPct = %f, want 0 (any non-zero value would flip the runner's switch)",
			off.TriggerPct)
	}
	if off.SummarizePct != 0 {
		t.Errorf("off tier SummarizePct = %f, want 0", off.SummarizePct)
	}

	// And confirm by calling the engine with oldestPct=0 (the value the
	// runner would skip computing): no archival, no LLM call, no apply.
	// This is the documented zero-pct trivial-no-op path
	// (engine.Compact step 0).
	store := &fakeStore{messages: buildLargeFixture(20)}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 1_000_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	id, err := eng.Compact(context.Background(), "sess-off", ProviderProfileRef{}, 0)
	if err != nil {
		t.Fatalf("Compact(oldestPct=0): %v", err)
	}
	if id != "" {
		t.Errorf("expected empty summary id on no-op path, got %q", id)
	}
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0 on the off-tier no-op path", got)
	}
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0 on the off-tier no-op path", got)
	}

	// The engine still emits a no-op KindSessionCompacted with ratio=1.0
	// — that's the documented contract callers like the runner can
	// inspect to verify "the engine ran, nothing to do".
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1 (no-op breadcrumb)", got)
	}
	if em.events[0].kind != audit.KindSessionCompacted {
		t.Errorf("audit kind = %q, want %q (no-op breadcrumb)",
			em.events[0].kind, audit.KindSessionCompacted)
	}
}

// TestIntegration_AuditEmissions_FullChain pins the audit row's payload
// shape end-to-end: fields populated, JSON-shape preserved, kind set
// to KindSessionCompacted on success. The audit pipeline is the single
// observable surface the chat-runner UI uses to report compaction
// outcomes back to the user, so a regression that drops any field here
// silently breaks the surface.
func TestIntegration_AuditEmissions_FullChain(t *testing.T) {
	store := &fakeStore{messages: buildLargeFixture(50)}
	llm := &fakeLLM{
		text:         "summary text payload",
		inputTokens:  500,
		outputTokens: 75,
	}
	caps := fakeCapabilities{max: 200_000, ok: true}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	model := ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-haiku-4-5"}
	_, err := eng.Compact(context.Background(), "sess-audit", model, 0.40)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}

	ev := em.events[0]
	if ev.kind != audit.KindSessionCompacted {
		t.Fatalf("kind = %q, want %q", ev.kind, audit.KindSessionCompacted)
	}

	pl, ok := ev.payload.(audit.SessionCompactedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want SessionCompactedPayload", ev.payload)
	}

	// Required fields populated:
	if pl.SessionID != "sess-audit" {
		t.Errorf("payload.SessionID = %q, want sess-audit", pl.SessionID)
	}
	if pl.ModelUsed != model.String() {
		t.Errorf("payload.ModelUsed = %q, want %q", pl.ModelUsed, model.String())
	}
	if pl.TokensInSpan == 0 {
		t.Errorf("payload.TokensInSpan = 0, want > 0")
	}
	if pl.TokensAfterSummary != 75 {
		t.Errorf("payload.TokensAfterSummary = %d, want 75", pl.TokensAfterSummary)
	}
	// Compression ratio = output / span; must be > 0 on a successful run.
	if pl.CompressionRatio <= 0 || pl.CompressionRatio >= 1.0 {
		t.Errorf("payload.CompressionRatio = %f, want 0 < ratio < 1.0",
			pl.CompressionRatio)
	}
}

// TestIntegration_CompressionRatio_Math pins the compression-ratio
// math used by the audit dashboard. Two Compact runs at the same span
// size with different LLM output sizes must produce strictly
// proportional ratios. If a regression flips numerator/denominator the
// dashboard would render misleading copy.
func TestIntegration_CompressionRatio_Math(t *testing.T) {
	// Both runs use the same 100-message fixture and oldestPct=0.30
	// (300 tokens in span). Output sizes are 50 and 30 tokens.
	makeRun := func(outputTokens int) audit.SessionCompactedPayload {
		store := &fakeStore{messages: build100MessageFixture()}
		llm := &fakeLLM{text: "ok", outputTokens: outputTokens}
		caps := fakeCapabilities{max: 1_000_000, ok: true}
		em := &fakeAudit{}
		eng := newTestEngine(t, store, llm, caps, em, 0)
		if _, err := eng.Compact(context.Background(), "s", ProviderProfileRef{}, 0.30); err != nil {
			t.Fatalf("Compact: %v", err)
		}
		return em.events[0].payload.(audit.SessionCompactedPayload)
	}

	run50 := makeRun(50)
	run30 := makeRun(30)

	if run50.TokensInSpan != run30.TokensInSpan {
		t.Fatalf("TokensInSpan drift between runs: %d vs %d",
			run50.TokensInSpan, run30.TokensInSpan)
	}
	want50 := 50.0 / float64(run50.TokensInSpan)
	want30 := 30.0 / float64(run30.TokensInSpan)
	if run50.CompressionRatio < want50-1e-6 || run50.CompressionRatio > want50+1e-6 {
		t.Errorf("run50.CompressionRatio = %f, want %f", run50.CompressionRatio, want50)
	}
	if run30.CompressionRatio < want30-1e-6 || run30.CompressionRatio > want30+1e-6 {
		t.Errorf("run30.CompressionRatio = %f, want %f", run30.CompressionRatio, want30)
	}
	// Smaller-output run produces strictly smaller ratio.
	if !(run30.CompressionRatio < run50.CompressionRatio) {
		t.Errorf("expected ratio(30) < ratio(50); got %f >= %f",
			run30.CompressionRatio, run50.CompressionRatio)
	}
}

// TestIntegration_AllFiveTiers_HaveDistinctBehavior asserts the five
// aggressiveness tiers each produce a distinguishable engine action.
// This is the user-visible promise of the dial: moving the tier dial
// produces a different outcome.
//
// The five tiers split across two engine paths (off → no-op,
// conservative/balanced/aggressive → threshold with different
// SummarizePct, maximal → rolling). We pin one concrete engine
// behavior per tier:
//
//   - off: no LLM call, no apply.
//   - conservative: 20% of oldest archived.
//   - balanced: 30% of oldest archived.
//   - aggressive: 40% of oldest archived.
//   - maximal: rolling-summary path produces a different prefix.
func TestIntegration_AllFiveTiers_HaveDistinctBehavior(t *testing.T) {
	// off — bypassed (oldestPct=0 path).
	t.Run("off", func(t *testing.T) {
		store := &fakeStore{messages: buildLargeFixture(50)}
		llm := &fakeLLM{}
		caps := fakeCapabilities{max: 1_000_000, ok: true}
		em := &fakeAudit{}
		eng := newTestEngine(t, store, llm, caps, em, 0)
		// off tier means runner skips the call; engine called with
		// SummarizePct=0 here to model that "no fold" outcome.
		_, err := eng.Compact(context.Background(), "s", ProviderProfileRef{}, compactionpolicy.Tier(compactionpolicy.AggressivenessOff).SummarizePct)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		if len(store.applyCalls) != 0 {
			t.Errorf("off tier produced an apply call; should never archive")
		}
		if len(llm.calls) != 0 {
			t.Errorf("off tier called LLM; should never run a summarize")
		}
	})

	// conservative / balanced / aggressive — three different
	// SummarizePct values produce three different archive-set sizes
	// against the same fixture.
	for _, tc := range []struct {
		name       string
		tier       compactionpolicy.CompactionAggressiveness
		wantArchived int
	}{
		{"conservative", compactionpolicy.AggressivenessConservative, 20},
		{"balanced", compactionpolicy.AggressivenessBalanced, 30},
		{"aggressive", compactionpolicy.AggressivenessAggressive, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{messages: buildLargeFixture(100)}
			llm := &fakeLLM{text: "ok", outputTokens: 5}
			caps := fakeCapabilities{max: 1_000_000, ok: true}
			em := &fakeAudit{}
			eng := newTestEngine(t, store, llm, caps, em, 0)

			summarizePct := compactionpolicy.Tier(tc.tier).SummarizePct
			if _, err := eng.Compact(context.Background(), "s", ProviderProfileRef{}, summarizePct); err != nil {
				t.Fatalf("Compact: %v", err)
			}
			if got := len(store.applyCalls); got != 1 {
				t.Fatalf("ApplyCompaction calls = %d, want 1", got)
			}
			if got := len(store.applyCalls[0].originalIDs); got != tc.wantArchived {
				t.Errorf("%s tier archived %d turns, want %d", tc.name, got, tc.wantArchived)
			}
		})
	}

	// maximal — RollingSummarize path produces the rolling prefix, NOT
	// the threshold-mode "[Earlier conversation summary: " prefix.
	t.Run("maximal", func(t *testing.T) {
		store := &fakeStore{messages: buildPairsFixture(6)}
		llm := &fakeLLM{text: "rolling", outputTokens: 3}
		caps := fakeCapabilities{max: 1_000_000, ok: true}
		em := &fakeAudit{}
		eng := newTestEngine(t, store, llm, caps, em, 0)

		if _, err := eng.RollingSummarize(context.Background(), "s", ProviderProfileRef{}, 4); err != nil {
			t.Fatalf("RollingSummarize: %v", err)
		}
		if got := len(store.applyCalls); got != 1 {
			t.Fatalf("ApplyCompaction calls = %d, want 1", got)
		}
		content := store.applyCalls[0].summary.Content
		if !strings.HasPrefix(content, rollingSummaryContentPrefix) {
			t.Errorf("maximal tier summary missing rolling prefix: %q", content)
		}
		if strings.HasPrefix(content, summaryContentPrefix) {
			t.Errorf("maximal tier produced threshold-mode prefix: %q", content)
		}
	})
}

// TestIntegration_ModelTooSmall_DoesNotPersist pins the data-safety
// invariant: when the cap pre-flight rejects, the originals are never
// touched. The chat runner's UI message ("your compaction model fits
// N but the span needs M") relies on the engine returning the typed
// error WITHOUT any partial state.
func TestIntegration_ModelTooSmall_DoesNotPersist(t *testing.T) {
	store := &fakeStore{messages: buildLargeFixture(100)}
	llm := &fakeLLM{}
	caps := fakeCapabilities{max: 100, ok: true} // tiny cap
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, caps, em, 0)

	_, err := eng.Compact(context.Background(), "sess",
		ProviderProfileRef{ProviderID: "p", ModelID: "tiny"},
		0.30)
	if err == nil {
		t.Fatalf("expected ErrCompactionModelTooSmall, got nil")
	}
	var typed *ErrCompactionModelTooSmall
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *ErrCompactionModelTooSmall", err)
	}

	// Originals untouched.
	if got := len(store.applyCalls); got != 0 {
		t.Errorf("ApplyCompaction calls = %d, want 0 (originals must stay untouched)", got)
	}
	// LLM never called (pre-flight reject).
	if got := len(llm.calls); got != 0 {
		t.Errorf("LLM calls = %d, want 0 (pre-flight should reject before any wire call)", got)
	}
	// Audit records exactly one Failed event.
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
}
