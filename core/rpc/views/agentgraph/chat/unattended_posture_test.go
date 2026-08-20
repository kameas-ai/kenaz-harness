package chat

// unattended_posture_test.go — mission model-scheduled-jobs-01PMSJ01
// WP05, H-2: "the ask bus is pre-seeded with exactly one answer and
// pauses on the next ask_user fire." A scheduled run must resolve to
// AskNever rather than inherit whatever the interactive session's
// autonomy dial happens to be — see chat_runner.go's StartStream, the
// `unattended := runposture.IsUnattended(ctx)` block right after
// resolvedKnobs is computed.
//
// The graph here is deliberately minimal: a single AskNode whose id is
// NOT chatAskNodeID ("ask_user"), so StartStream's pre-seed
// (askBus.Answer(subID, chatAskNodeID, userMessage)) does not touch it —
// this is exactly the "re-entered" / follow-up ask shape H-2 describes,
// without needing a full agent_loop. No model call happens at all: the
// run either pauses on this node (interactive) or auto-resolves via
// applyAskOnAmbiguityDial's DefaultAnswer stamp (unattended) and
// terminates.

import (
	"context"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
)

// unseededAskID deliberately differs from chatAskNodeID so StartStream's
// pre-seed of the FIRST ask never reaches this node.
const unseededAskID = "ask_user_followup"

func unseededAskGraph() coreag.Graph {
	return coreag.Graph{
		ID:          "test_unseeded_ask",
		Entrypoints: []string{unseededAskID},
		Nodes: []coreag.Node{
			{
				ID:    unseededAskID,
				Kind:  coreag.NodeKindAsk,
				Attrs: coreag.AskAttrs{Question: "anything else?"},
			},
		},
	}
}

func buildUnseededAskRunner(t *testing.T) (*ChatRunner, *recordingBroker) {
	t.Helper()
	broker := &recordingBroker{}
	graph := unseededAskGraph()
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker
}

// TestUnattendedRun_UnseededAskCompletesInsteadOfPausing is H-2: a
// scheduled (unattended) run whose graph re-enters ask_user — i.e. fires
// an AskNode with no seeded answer — must auto-resolve via
// applyAskOnAmbiguityDial's DefaultAnswer and terminate, never pause on
// a human who is not there to answer.
//
// Mutation: delete the `if unattended { resolvedKnobs.AskOnAmbiguity =
// autonomy.AskNever }` block in StartStream. This test must fail (the
// run pauses, matching TestInteractiveRun_UnseededAskStillPauses below)
// against that mutant.
func TestUnattendedRun_UnseededAskCompletesInsteadOfPausing(t *testing.T) {
	t.Parallel()
	runner, broker := buildUnseededAskRunner(t)

	ctx := runposture.Unattended(context.Background())
	subID, err := runner.StartStream(ctx, "profile-1", "session-1", "", "")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("expected non-empty sub id")
	}

	closed := waitForClosed(t, broker)
	if closed.FinishReason == "paused" {
		t.Fatalf("unattended run paused on an unseeded ask node: %+v", closed)
	}
	if closed.Reason != "completed" {
		t.Errorf("Reason = %q, want completed", closed.Reason)
	}
}

// TestInteractiveRun_UnseededAskStillPauses is FR-008's companion for
// H-2: the SAME graph, run WITHOUT the unattended marker, still pauses —
// proving the posture is a per-run override, not a change to the
// default interactive behaviour applyAskOnAmbiguityDial already had.
func TestInteractiveRun_UnseededAskStillPauses(t *testing.T) {
	t.Parallel()
	runner, broker := buildUnseededAskRunner(t)

	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("expected non-empty sub id")
	}

	deadline := time.Now().Add(2 * time.Second)
	var sawPause bool
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			if e.topic != "llm:stream-closed" {
				continue
			}
			payload := e.payload.(StreamClosedPayload)
			if payload.FinishReason == "paused" {
				sawPause = true
			}
		}
		if sawPause {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sawPause {
		t.Fatalf("interactive run over the SAME graph did not pause on the unseeded ask node; events = %+v", broker.snapshot())
	}
}
