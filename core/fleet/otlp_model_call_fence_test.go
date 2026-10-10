package fleet

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

// TestOTLPPipeline_ModelCallSpansStayLocal pins the 2026-10-09 owner
// ruling for llm-call-telemetry-01LLMTEL01: gen_ai.chat / kenaz.chat.turn
// spans reach Fleet only under an explicit "Share model-call telemetry
// with Fleet" opt-in (default OFF, WP02), never under the general
// telemetry consent. With that setting at its default (OFF) they do not
// leave the machine at all; otlp_model_call_optin_test.go covers ON.
//
// The pipeline is put in its most permissive reachable state short of the
// model-call opt-in (full consent, the span class opted in) and a fake
// Fleet receives
// /otlp/v1/traces: an ordinary harness span arrives, proving the lane is
// open; the turn span and the provider-call span — real ones, through
// genaitrace.StartTurn / Instrument — do not, and no gen_ai.* / kenaz.*
// key or the generation id appears anywhere in the payload.
func TestOTLPPipeline_ModelCallSpansStayLocal(t *testing.T) {
	srv, fake := newFakeOTLPServer()
	defer srv.Close()

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp) // genaitrace emits through the global provider
	defer otel.SetTracerProvider(prev)

	pipeline := NewFleetOTLPPipeline(nil)
	openEverything(pipeline)
	ctx := context.Background()
	if err := pipeline.Activate(ctx, srv.URL, resource.Empty(), IdentityAttrs{
		UserID: "user-mc", OrgID: "org-mc",
	}, fakeBearerProvider(fakeJWTWith("user-mc", "org-mc", "")), tp); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// An ordinary span: proves the lane exports at all.
	_, plain := tp.Tracer("model-call-fence-test").Start(ctx, "harness.task.create")
	plain.SetAttributes(attribute.String("task.kind", "shell"))
	plain.End()

	// A real turn + provider call.
	turnCtx, turn := genaitrace.StartTurn(ctx, "session-mc")
	call := genaitrace.Call{ProviderKind: "openrouter", RequestModel: "openai/gpt-4o", SessionID: "session-mc"}
	s, err := genaitrace.Instrument(turnCtx, call, nil, func(context.Context) (llm.Stream, error) {
		return &doneStream{resp: llm.Response{FinishReason: "stop", Usage: llm.Usage{InputTokens: 11},
			Meta: &llm.CallMeta{ResponseID: "gen-fence-0001"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	_, _ = s.Final()
	turn.End()

	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	_ = pipeline.Shutdown(flushCtx)

	body := fake.combinedBody()
	if !bytes.Contains(body, []byte("harness.task.create")) {
		t.Fatalf("the ordinary span never reached the fake Fleet — the test proves nothing (payload %d bytes)", len(body))
	}
	for _, leak := range []string{genaitrace.SpanNameChat, genaitrace.SpanNameTurn, "gen_ai.", "kenaz.", "gen-fence-0001", genaitrace.ScopeName} {
		if bytes.Contains(body, []byte(leak)) {
			t.Errorf("model-call telemetry %q reached Fleet without the model-call opt-in", leak)
		}
	}
}

// doneStream is an already-finished llm.Stream.
type doneStream struct{ resp llm.Response }

func (d *doneStream) Events() <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}
func (d *doneStream) Cancel() error                { return nil }
func (d *doneStream) Final() (llm.Response, error) { return d.resp, nil }
