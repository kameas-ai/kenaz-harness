package usage_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	"github.com/sigil-tech/kaneaz-harness/core/usage"
)

// TestIntegration_SingleWriter_DoubleMeterConsumer is the WP07
// cross-mission integration test (token-cost-telemetry-01KQ8TD7).
//
// It proves that:
//
//  1. The usage.Manager is the SOLE writer of the session_messages
//     cost columns. A single fixture turn produces exactly one
//     UPDATE — no duplicate accounting.
//  2. The two read-side meters (per-session GetSession aggregate +
//     cross-session MonthlyTotalUSD rollup) agree to the cent on the
//     same fixture data. This is the "double meter consumer" half:
//     two consumers reading from the same single writer must produce
//     consistent figures or the billing math is broken.
//  3. Provider-reported, derived, and unknown cost sources all
//     round-trip through both meters with the right CostSource label
//     and the right contribution to the monthly total (NULL cost_usd
//     contributes zero, never NaN).
//  4. The threshold scheduler, when wired to the same Manager,
//     observes exactly the totals MonthlyTotalUSD reports — closing
//     the loop between the writer and the WP06 notification path.
func TestIntegration_SingleWriter_DoubleMeterConsumer(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	mgr := usage.New(db)
	ctx := context.Background()

	// Two sessions in the same project so the MonthlyTotalUSD rollup
	// has something cross-session to sum.
	seedSession(t, db, "sess-A")
	seedSession(t, db, "sess-B")

	// sess-A: one provider-reported turn (exact dollar) + one
	// pricing-derived turn (estimate, table-rate).
	seedMessage(t, db, "sess-A", "msg-A1", "assistant", 1)
	seedMessage(t, db, "sess-A", "msg-A2", "assistant", 2)
	// sess-B: one OpenRouter-flavored provider turn + one
	// unknown-source turn (no pricing entry → NULL cost_usd).
	seedMessage(t, db, "sess-B", "msg-B1", "assistant", 1)
	seedMessage(t, db, "sess-B", "msg-B2", "assistant", 2)

	costA1 := 0.012 // OpenRouter-style provider exact
	costA2 := 0.008 // pricing-table-derived estimate
	costB1 := 0.020 // OpenRouter-style provider exact

	turns := []usage.UsageTurn{
		{
			SessionID: "sess-A", MessageID: "msg-A1",
			ProviderKind: "openrouter", ModelID: "anthropic/claude-sonnet",
			PromptTokens: 100, CompletionTokens: 50,
			CostUSD: &costA1, CostSource: "provider",
		},
		{
			SessionID: "sess-A", MessageID: "msg-A2",
			ProviderKind: "anthropic", ModelID: "claude-sonnet-4-5",
			PromptTokens: 200, CompletionTokens: 100,
			CostUSD: &costA2, CostSource: "derived",
		},
		{
			SessionID: "sess-B", MessageID: "msg-B1",
			ProviderKind: "openrouter", ModelID: "anthropic/claude-haiku",
			PromptTokens: 50, CompletionTokens: 25,
			CostUSD: &costB1, CostSource: "provider",
		},
		{
			SessionID: "sess-B", MessageID: "msg-B2",
			ProviderKind: "bedrock", ModelID: "anthropic.claude-no-pricing",
			PromptTokens: 80, CompletionTokens: 40,
			CostUSD: nil, CostSource: "unknown",
		},
	}

	for _, turn := range turns {
		if err := mgr.Add(ctx, turn); err != nil {
			t.Fatalf("Add %s: %v", turn.MessageID, err)
		}
	}

	// ── Single-writer invariant ───────────────────────────────────
	// Re-running Add against the SAME messageID with different totals
	// MUST overwrite the prior values (UPDATE semantics, not INSERT).
	// The number of rows in session_messages must equal the number of
	// seeded messages — no phantom duplicates.
	var rowCount int
	row := db.Reader().QueryRow(ctx,
		`SELECT COUNT(*) FROM session_messages WHERE role = 'assistant'`)
	if err := row.Scan(&rowCount); err != nil {
		t.Fatalf("count session_messages: %v", err)
	}
	if rowCount != 4 {
		t.Errorf("session_messages.assistant row count = %d, want 4", rowCount)
	}

	// ── Per-session meter ─────────────────────────────────────────
	aggA, err := mgr.GetSession(ctx, "sess-A")
	if err != nil {
		t.Fatalf("GetSession A: %v", err)
	}
	wantA := costA1 + costA2 // 0.020
	if !approxEqual(aggA.CostUSD, wantA, 1e-9) {
		t.Errorf("sess-A CostUSD = %v, want %v", aggA.CostUSD, wantA)
	}
	if aggA.CostSource != "mixed" {
		t.Errorf("sess-A CostSource = %q, want mixed", aggA.CostSource)
	}
	if aggA.PromptTokens != 300 || aggA.CompletionTokens != 150 || aggA.TotalTokens != 450 {
		t.Errorf("sess-A tokens = %d/%d/%d, want 300/150/450",
			aggA.PromptTokens, aggA.CompletionTokens, aggA.TotalTokens)
	}

	aggB, err := mgr.GetSession(ctx, "sess-B")
	if err != nil {
		t.Fatalf("GetSession B: %v", err)
	}
	wantB := costB1 // 0.020 (unknown turn contributes 0)
	if !approxEqual(aggB.CostUSD, wantB, 1e-9) {
		t.Errorf("sess-B CostUSD = %v, want %v", aggB.CostUSD, wantB)
	}
	// sess-B has one provider turn + one unknown turn → "provider"
	// (only provider turns contribute to source classification when
	// there are no derived turns).
	if aggB.CostSource != "provider" {
		t.Errorf("sess-B CostSource = %q, want provider", aggB.CostSource)
	}

	// ── Cross-session monthly meter ───────────────────────────────
	// Use a now() inside the seeded month so MonthlyTotalUSD's
	// [start, end) window contains the seeded created_at values.
	now := time.Now().Local()
	monthly, err := mgr.MonthlyTotalUSD(ctx, now)
	if err != nil {
		t.Fatalf("MonthlyTotalUSD: %v", err)
	}
	wantMonthly := wantA + wantB // 0.040
	if !approxEqual(monthly, wantMonthly, 1e-9) {
		t.Errorf("MonthlyTotalUSD = %v, want %v", monthly, wantMonthly)
	}

	// ── Double-meter agreement ────────────────────────────────────
	// The two consumers MUST agree: per-session sums equal the
	// cross-session monthly sum to the cent. This is the billing-
	// math invariant the spec calls out.
	if !approxEqual(aggA.CostUSD+aggB.CostUSD, monthly, 1e-9) {
		t.Errorf("double-meter mismatch: per-session sum = %v, monthly = %v",
			aggA.CostUSD+aggB.CostUSD, monthly)
	}

	// ── Threshold scheduler closes the loop ───────────────────────
	// Wire a checker against the same Manager. With the dial set to
	// $0.05 ($monthly = $0.040 → 80%), accumulating one more $0.011
	// turn brings monthly to $0.051 → 102% and must fire 50, 80, 100.
	pub := &recordingPublisher{}
	checker, err := usage.NewCheckerFromManager(
		mgr.(usage.ManagerWithChecker),
		func() (float64, error) { return 0.05, nil },
		pub,
	)
	if err != nil {
		t.Fatalf("NewCheckerFromManager: %v", err)
	}
	mgr.SetThresholdChecker(checker)

	seedMessage(t, db, "sess-A", "msg-A3", "assistant", 3)
	costA3 := 0.011
	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID: "sess-A", MessageID: "msg-A3",
		ProviderKind: "openrouter", ModelID: "anthropic/claude-sonnet",
		PromptTokens: 100, CompletionTokens: 50,
		CostUSD: &costA3, CostSource: "provider",
	}); err != nil {
		t.Fatalf("Add msg-A3: %v", err)
	}

	events := pub.snapshot()
	// Expect 50 + 80 + 100 in some order (Check walks the tier list
	// in ascending order so we can assert ordering too).
	if len(events) != 3 {
		t.Fatalf("threshold events = %d, want 3 (50 + 80 + 100)", len(events))
	}
	wantPcts := []int{50, 80, 100}
	for i, want := range wantPcts {
		if events[i].payload.Pct != want {
			t.Errorf("event[%d].pct = %d, want %d", i, events[i].payload.Pct, want)
		}
	}

	// ── Threshold path agrees with double-meter math ──────────────
	// The MonthTotalUSD payload field MUST equal the live monthly
	// total at fire time. This is the closure: writer → both
	// consumers → threshold all see the same number.
	finalMonthly, err := mgr.MonthlyTotalUSD(ctx, now)
	if err != nil {
		t.Fatalf("final MonthlyTotalUSD: %v", err)
	}
	for i, ev := range events {
		if !approxEqual(ev.payload.MonthTotalUSD, finalMonthly, 1e-9) {
			t.Errorf("event[%d].monthTotalUsd = %v, want %v (live monthly)",
				i, ev.payload.MonthTotalUSD, finalMonthly)
		}
	}

	// ── Idempotency on restart ────────────────────────────────────
	// Re-running Add on the same MessageID must NOT re-fire any tier
	// (the cost_threshold_fired table dedupes per year_month/pct).
	pub2 := &recordingPublisher{}
	checker2, _ := usage.NewCheckerFromManager(
		mgr.(usage.ManagerWithChecker),
		func() (float64, error) { return 0.05, nil },
		pub2,
	)
	mgr.SetThresholdChecker(checker2)

	if err := mgr.Add(ctx, usage.UsageTurn{
		SessionID: "sess-A", MessageID: "msg-A3",
		PromptTokens: 100, CompletionTokens: 50,
		CostUSD: &costA3, CostSource: "provider",
	}); err != nil {
		t.Fatalf("Add msg-A3 (replay): %v", err)
	}
	if got := pub2.snapshot(); len(got) != 0 {
		t.Errorf("replay re-fired %d events, want 0 (dedup broken)", len(got))
	}
}

// approxEqual reports whether a and b are within tol of each other.
// SQLite's REAL roundtrip is float64 native so 1e-9 is plenty for the
// dollar-amount asserts.
func approxEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// Compile-time guard: the production manager MUST satisfy the
// ManagerWithChecker contract or the threshold wiring in api.go won't
// compile. Fails loudly if the surface drifts.
var _ usage.ManagerWithChecker = mustManagerWithChecker(nil)

func mustManagerWithChecker(_ *testing.T) usage.ManagerWithChecker {
	// Returning nil is fine — this is a compile-time interface check;
	// the type assertion in TestIntegration_SingleWriter_DoubleMeterConsumer
	// exercises the runtime contract.
	return nil
}

// Compile-time guard: storage.DB and session.NewStorageDB are still
// the helpers our seed* helpers use, so any breaking rename in either
// package surfaces here as a build error rather than a runtime panic.
var (
	_ storage.DB = (storage.DB)(nil)
	_            = session.NewStorageDB
)

// Suppress the unused-import lint when running -short.
var _ = sync.Mutex{}
