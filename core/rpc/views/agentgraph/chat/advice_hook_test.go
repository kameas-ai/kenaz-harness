package chat

// advice_hook_test.go drives laya-advisors-01LAYA001 WP07's post-turn
// advice hook (fireAdvice/checkModelSwitch) directly against a bare
// *ChatRunner (no kernel, no HTTP, no sqlite) — this package DOES import
// core/advice/kinds/branchnow (advice_hook.go's own imports), so the
// REAL "branch_now" kind is registered via that package's init() for
// every test in this binary, exactly as core/rpc/api.go's production
// wiring sees it. This is the complementary end-to-end half of AC-04's
// mutation proof (core/advice/ac04_mutation_test.go proves the GATE
// function refuses a mutated registration; this proves the CALL SITE
// actually consults the gate before auto-acting, and that a suggest-only
// kind's evaluator never reaches an auto-act executor at all).

import (
	"context"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// fakeBroker records every Emit call, race-safe per CLAUDE.md's
// mutex+snapshot pattern.
type fakeAdviceBroker struct {
	mu     sync.Mutex
	events []struct {
		topic   string
		payload any
	}
}

func (b *fakeAdviceBroker) Emit(topic string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, struct {
		topic   string
		payload any
	}{topic, payload})
}

func (b *fakeAdviceBroker) snapshotTopics() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.events))
	for i, e := range b.events {
		out[i] = e.topic
	}
	return out
}

// fakeAuditEmitter records every Emit call.
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []contextaudit.Event
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e contextaudit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeAuditEmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// TestFireAdvice_AutonomousTier_BranchNow_AutoActsAndAudits is the
// end-to-end half of AC-04: at the Autonomous tier, with the REAL
// registered branch_now kind (SafetyReversible) scripted to decide
// "yes" at high confidence, fireAdvice must call AutoActBranchNow, emit
// the auto-act audit event, and NEVER publish a passive chip for
// branch_now (the auto-act path returns before the chip-publish branch).
func TestFireAdvice_AutonomousTier_BranchNow_AutoActsAndAudits(t *testing.T) {
	fake := advice.NewFakeAdvisor()
	fake.ScriptFor("branch_now", advice.Recommendation{Decision: true, Confidence: 95, Model: "heuristic/branch-regex-v1", Rung: advice.RungHeuristic})
	// compact_now / escalate_model: script "no" so only branch_now is in play.
	fake.ScriptFor("compact_now", advice.Recommendation{Decision: false, Confidence: 0})
	fake.ScriptFor("escalate_model", advice.Recommendation{Decision: false, Confidence: 0})

	var autoActCalls int
	var autoActMu sync.Mutex
	audit := &fakeAuditEmitter{}
	broker := &fakeAdviceBroker{}

	r := &ChatRunner{
		cfg: Config{
			Advisor: fake,
			Broker:  broker,
			AdviceDeps: &AdviceDeps{
				BranchCount: func(context.Context, string) (int, error) { return 0, nil },
				AutoActBranchNow: func(context.Context, string) (string, error) {
					autoActMu.Lock()
					autoActCalls++
					autoActMu.Unlock()
					return "child-session-1", nil
				},
				AutoActAudit: audit,
			},
		},
	}

	r.fireAdvice("sess-1", "profile-1", "model-1", "help me with this side task", autonomy.TierAutonomous)

	autoActMu.Lock()
	got := autoActCalls
	autoActMu.Unlock()
	if got != 1 {
		t.Fatalf("AutoActBranchNow call count = %d, want 1", got)
	}
	if audit.count() != 1 {
		t.Errorf("audit emit count = %d, want 1 (KindAdviceAutoActed)", audit.count())
	}
	for _, topic := range broker.snapshotTopics() {
		if topic == TopicAdviceRecommendation {
			t.Errorf("branch_now auto-acted but ALSO published a passive chip on %s — auto-act must short-circuit the chip", TopicAdviceRecommendation)
		}
	}
}

// TestFireAdvice_BelowAutonomousTier_BranchNow_PublishesChipNotAutoAct
// pins the tier boundary: the SAME high-confidence "yes" recommendation
// at TierBold (below Autonomous) must render a passive chip and must
// NOT auto-act.
func TestFireAdvice_BelowAutonomousTier_BranchNow_PublishesChipNotAutoAct(t *testing.T) {
	fake := advice.NewFakeAdvisor()
	fake.ScriptFor("branch_now", advice.Recommendation{Decision: true, Confidence: 95, Model: "heuristic/branch-regex-v1", Rung: advice.RungHeuristic})
	fake.ScriptFor("compact_now", advice.Recommendation{Decision: false, Confidence: 0})
	fake.ScriptFor("escalate_model", advice.Recommendation{Decision: false, Confidence: 0})

	var autoActCalls int
	broker := &fakeAdviceBroker{}
	r := &ChatRunner{
		cfg: Config{
			Advisor: fake,
			Broker:  broker,
			AdviceDeps: &AdviceDeps{
				BranchCount:      func(context.Context, string) (int, error) { return 0, nil },
				AutoActBranchNow: func(context.Context, string) (string, error) { autoActCalls++; return "x", nil },
			},
		},
	}

	r.fireAdvice("sess-1", "profile-1", "model-1", "help me with this side task", autonomy.TierBold)

	if autoActCalls != 0 {
		t.Errorf("AutoActBranchNow call count at TierBold = %d, want 0", autoActCalls)
	}
	var sawChip bool
	for _, topic := range broker.snapshotTopics() {
		if topic == TopicAdviceRecommendation {
			sawChip = true
		}
	}
	if !sawChip {
		t.Errorf("no %s chip published at TierBold for a decision=true, confidence=95 recommendation", TopicAdviceRecommendation)
	}
}

// TestFireAdvice_SuggestOnlyKinds_NeverAutoAct pins the structural half
// of AC-04 at the CALL SITE: compact_now and escalate_model are
// SafetySuggestOnly (registered by their own packages), so even at the
// Autonomous tier with both scripted to decide "yes" at high confidence,
// evaluateCompactNow/evaluateEscalateModel never call RequireCanAutoAct
// or an auto-act executor — there is no auto-act code path for them to
// take at all (unlike branch_now, which takes the path and is then
// refused by the gate under mutation).
func TestFireAdvice_SuggestOnlyKinds_NeverAutoAct(t *testing.T) {
	fake := advice.NewFakeAdvisor()
	fake.ScriptFor("branch_now", advice.Recommendation{Decision: false, Confidence: 0})
	fake.ScriptFor("compact_now", advice.Recommendation{Decision: true, Confidence: 99, Model: "heuristic/compact-fill-threshold-v1", Rung: advice.RungHeuristic})
	fake.ScriptFor("escalate_model", advice.Recommendation{Decision: true, Confidence: 99, Model: "heuristic/escalate-failure-streak-v1", Rung: advice.RungHeuristic})

	var autoActCalls int
	broker := &fakeAdviceBroker{}
	r := &ChatRunner{
		cfg: Config{
			Advisor: fake,
			Broker:  broker,
			AdviceDeps: &AdviceDeps{
				BranchCount:      func(context.Context, string) (int, error) { return 0, nil },
				AutoActBranchNow: func(context.Context, string) (string, error) { autoActCalls++; return "x", nil },
			},
		},
	}

	r.fireAdvice("sess-1", "profile-1", "model-1", "context is getting full", autonomy.TierAutonomous)

	if autoActCalls != 0 {
		t.Errorf("AutoActBranchNow call count for suggest-only kinds at Autonomous tier = %d, want 0", autoActCalls)
	}
	topics := broker.snapshotTopics()
	var chipCount int
	for _, topic := range topics {
		if topic == TopicAdviceRecommendation {
			chipCount++
		}
	}
	if chipCount != 2 {
		t.Errorf("chip publish count = %d, want 2 (compact_now + escalate_model, both suggest-only, both shown)", chipCount)
	}
}

// TestCheckModelSwitch_EmitsOnlyOnActualChange pins the model.switched
// behavioral-label source: the first StartStream for a session never
// fires (no prior model to switch away from), a repeat of the SAME
// (profile, model) pair never fires, and an actual change fires exactly
// once.
func TestCheckModelSwitch_EmitsOnlyOnActualChange(t *testing.T) {
	audit := &fakeAuditEmitter{}
	r := &ChatRunner{cfg: Config{AdviceDeps: &AdviceDeps{ModelSwitchAudit: audit}}}
	ctx := context.Background()

	r.checkModelSwitch(ctx, "sess-1", "profile-a", "model-a")
	if audit.count() != 0 {
		t.Fatalf("first call: audit count = %d, want 0 (no prior model)", audit.count())
	}

	r.checkModelSwitch(ctx, "sess-1", "profile-a", "model-a")
	if audit.count() != 0 {
		t.Fatalf("repeat of the same pair: audit count = %d, want 0", audit.count())
	}

	r.checkModelSwitch(ctx, "sess-1", "profile-b", "model-b")
	if audit.count() != 1 {
		t.Fatalf("after an actual switch: audit count = %d, want 1", audit.count())
	}

	// A second session's first call must not be affected by session-1's
	// history (per-session state, not global).
	r.checkModelSwitch(ctx, "sess-2", "profile-a", "model-a")
	if audit.count() != 1 {
		t.Fatalf("a different session's first call: audit count = %d, want 1 (unchanged)", audit.count())
	}
}
