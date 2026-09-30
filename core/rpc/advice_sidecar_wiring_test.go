package rpc

// advice_sidecar_wiring_test.go — laya-advisors-01LAYA001 WP15's wiring
// proof, through the UNTOUCHED production construction path
// (newLLMStack): chatAdvisor is CaptureAdvisor -> SidecarAdvisor, and
// with the dated-nil sidecarProbe (WP13 wires the real Manager) a
// recommendation is served by the local heuristic fallback — the stack
// the user actually runs, not a fake. Mutation: revert newLLMStack to
// wrap heuristicAdvisor directly and the type assertion below fails.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/advice"
	advicecompactnow "github.com/kameas-ai/kenaz-harness/core/advice/kinds/compactnow"
	advicelabels "github.com/kameas-ai/kenaz-harness/core/advice/labels"
)

func TestNewLLMStack_ChatAdvisor_IsCaptureOverSidecarOverHeuristic(t *testing.T) {
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	cedarEngine := buildCedarEngineOrNil(dataDir, nil)
	stack := newLLMStack(c, NewStreamBroker(NewMultiEmitter()), newPersonalStore(c),
		nil, nil, func() bool { return false }, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, cedarEngine, nil, nil, nil, nil)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	if stack.chatAdvisor == nil {
		t.Fatal("newLLMStack produced no chatAdvisor")
	}

	capture, ok := stack.chatAdvisor.(*advicelabels.CaptureAdvisor)
	if !ok {
		t.Fatalf("chatAdvisor is %T, want *labels.CaptureAdvisor outermost (capture semantics preserved)", stack.chatAdvisor)
	}
	if _, ok := capture.Inner().(*advice.SidecarAdvisor); !ok {
		t.Fatalf("CaptureAdvisor wraps %T, want *advice.SidecarAdvisor (Capture -> Sidecar -> Heuristic)", capture.Inner())
	}

	// End to end through the real stack: the sidecar probe is the dated nil,
	// so the Sidecar layer falls through and the HeuristicAdvisor answers —
	// honestly labeled as the heuristic it is.
	kind, ok := advice.Get(advicecompactnow.KindID)
	if !ok {
		t.Fatalf("kind %q not registered", advicecompactnow.KindID)
	}
	rec, err := stack.chatAdvisor.Recommend(context.Background(), kind,
		advicecompactnow.Features{ContextFillFraction: 0.9, FeaturesIncomplete: true},
		advice.SessionContext{SessionID: "wp15-wiring-sess"})
	if err != nil {
		t.Fatalf("Recommend through the production stack: %v", err)
	}
	if rec.Rung != advice.RungHeuristic || rec.Model != advicecompactnow.ModelID {
		t.Errorf("recommendation = %+v, want the local heuristic's (Rung=%s Model=%s)", rec, advice.RungHeuristic, advicecompactnow.ModelID)
	}
}
