package labels

// placeholder_discriminator_test.go — the placeholder-row discriminator
// (review promotion, laya-advisors-01LAYA001 WP07/WP08 review round,
// 2026-09-29): "compact_now/escalate_model rows built from zero-value
// placeholder features are indistinguishable from genuine no-signal
// rows, which would poison a future fine-tune."
//
// This file exercises the FULL path — a real kind's Features type
// (compactnow.Features / escalatemodel.Features), through
// CaptureAdvisor.Recommend's featuresComplete() type assertion, into the
// Row it hands to Store.Insert — not just the bridge's generic fallback
// (capture_test.go's testFeatures, which does not implement
// advice.FeaturesCompleteness at all, already covers that default-true
// path). Importing core/advice/kinds/{compactnow,escalatemodel} here
// does not create a cycle: neither kind package imports
// core/advice/labels.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/advice/kinds/branchnow"
	"github.com/kameas-ai/kenaz-harness/core/advice/kinds/compactnow"
	"github.com/kameas-ai/kenaz-harness/core/advice/kinds/escalatemodel"
)

func TestCaptureAdvisor_CompactNow_PlaceholderFeatures_MarkedIncomplete(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind(compactnow.KindID, advice.SafetySuggestOnly)
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: false, Confidence: 0, Model: compactnow.ModelID, Rung: advice.RungHeuristic})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	// Mirrors advice_hook.go's fireAdvice: an all-zero-value Snapshot
	// (no ChatRunner-visible token-accounting accessor wired yet),
	// FeaturesIncomplete explicitly set true.
	features, err := compactnow.Extract(compactnow.Snapshot{FeaturesIncomplete: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := ca.Recommend(context.Background(), kind, features, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}

	inserts, _ := spy.snapshot()
	if inserts != 1 {
		t.Fatalf("insert calls = %d, want 1", inserts)
	}
	if spy.lastInsertRow.FeaturesComplete {
		t.Error("FeaturesComplete = true for a placeholder Snapshot, want false")
	}
}

func TestCaptureAdvisor_CompactNow_RealFeatures_MarkedComplete(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind(compactnow.KindID, advice.SafetySuggestOnly)
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: true, Confidence: 90, Model: compactnow.ModelID, Rung: advice.RungHeuristic})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	// A real, wired Snapshot (FeaturesIncomplete left at its zero value,
	// false — the field's whole point is that the CALLER opts INTO
	// reporting incompleteness, never the reverse).
	features, err := compactnow.Extract(compactnow.Snapshot{ContextFillFraction: 0.9, TokensInSpan: 4000})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := ca.Recommend(context.Background(), kind, features, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}

	if !spy.lastInsertRow.FeaturesComplete {
		t.Error("FeaturesComplete = false for a real Snapshot, want true")
	}
}

func TestCaptureAdvisor_EscalateModel_PlaceholderFeatures_MarkedIncomplete(t *testing.T) {
	inner := advice.NewFakeAdvisor()
	kind := testKind(escalatemodel.KindID, advice.SafetySuggestOnly)
	inner.ScriptFor(kind.ID, advice.Recommendation{Decision: false, Confidence: 0, Model: escalatemodel.ModelID, Rung: advice.RungHeuristic})
	spy := &spyStore{}
	ca := NewCaptureAdvisor(inner, spy, func() bool { return true })

	features, err := escalatemodel.Extract(escalatemodel.Snapshot{FeaturesIncomplete: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := ca.Recommend(context.Background(), kind, features, advice.SessionContext{SessionID: "s1"}); err != nil {
		t.Fatalf("Recommend: %v", err)
	}

	if spy.lastInsertRow.FeaturesComplete {
		t.Error("FeaturesComplete = true for a placeholder Snapshot, want false")
	}
}

// TestBranchNow_DoesNotImplementFeaturesCompleteness pins the review's
// own reasoning for why branch_now needs none of this: unlike
// compactnow.Features/escalatemodel.Features, branch_now's Features type
// does not implement advice.FeaturesCompleteness at all, so
// featuresComplete()'s default-true fallback applies to it
// unconditionally — its decision-bearing fields (heuristic signal/noise
// counts, branch counts) are real data from a live accessor, never
// placeholders, so it has nothing to report.
func TestBranchNow_DoesNotImplementFeaturesCompleteness(t *testing.T) {
	var f branchnow.Features
	if _, ok := any(f).(advice.FeaturesCompleteness); ok {
		t.Error("branchnow.Features implements advice.FeaturesCompleteness; expected it not to (see comment)")
	}
}

// TestCompactNow_EscalateModel_ImplementFeaturesCompleteness is the
// converse sanity check: both kinds that DO need the discriminator
// actually implement the interface (a typo'd method signature would
// silently fall through to featuresComplete()'s default-true path and
// this whole promotion would be a no-op).
func TestCompactNow_EscalateModel_ImplementFeaturesCompleteness(t *testing.T) {
	var cf compactnow.Features
	if _, ok := any(cf).(advice.FeaturesCompleteness); !ok {
		t.Error("compactnow.Features does not implement advice.FeaturesCompleteness")
	}
	var ef escalatemodel.Features
	if _, ok := any(ef).(advice.FeaturesCompleteness); !ok {
		t.Error("escalatemodel.Features does not implement advice.FeaturesCompleteness")
	}
}
