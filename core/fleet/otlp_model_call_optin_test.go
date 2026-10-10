package fleet

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

const optInPrompt = "SECRET-OPTIN-PROMPT-TEXT"

// modelCall drives one real provider call through genaitrace.Instrument
// and returns a func that finishes it (Final), so a test can start a call
// on one side of a toggle and end it on the other.
func modelCall(t *testing.T, genID string) (finish func()) {
	t.Helper()
	call := genaitrace.CallFromRequest(llm.GenerationRequest{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, optInPrompt)},
	}, llm.ProviderProfile{ID: "p-or", Kind: "openrouter", Model: "openai/gpt-4o"})
	s, err := genaitrace.Instrument(context.Background(), call, nil, func(context.Context) (llm.Stream, error) {
		return &doneStream{resp: llm.Response{FinishReason: "stop", Usage: llm.Usage{InputTokens: 7, OutputTokens: 2},
			Meta: &llm.CallMeta{ResponseID: genID}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		for range s.Events() {
		}
		_, _ = s.Final()
	}
}

func optInRig(t *testing.T, general bool) (*FleetOTLPPipeline, *sdktrace.TracerProvider, *fakeOTLPServer) {
	t.Helper()
	srv, fake := newFakeOTLPServer()
	t.Cleanup(srv.Close)
	tp := sdktrace.NewTracerProvider()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp) // genaitrace emits through the global provider
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	p := NewFleetOTLPPipeline(nil)
	if general {
		openEverything(p)
	}
	if err := p.Activate(context.Background(), srv.URL, resource.Empty(), IdentityAttrs{
		UserID: "user-oi", OrgID: "org-oi",
	}, fakeBearerProvider(fakeJWTWith("user-oi", "org-oi", "")), tp); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, tp, fake
}

func flush(t *testing.T, tp *sdktrace.TracerProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
}

// TestOTLPPipeline_ModelCallSharing_OffOnOff pins the WP02 opt-in against
// a fake Fleet receiving /otlp/v1/traces, with general consent fully open:
//   - OFF (the default): no gen_ai span leaves, while an ordinary span does
//     (the lane is open — the filter is what withholds them);
//   - turning it ON exports only calls that START after the toggle — one
//     that started before and ended after is not back-filled;
//   - turning it OFF again stops export at the next batch;
//   - the filter is still in the path (withheld counter), and no content
//     ever reaches the payload.
//
// Not parallel: genaitrace emits through the OTel global provider.
func TestOTLPPipeline_ModelCallSharing_OffOnOff(t *testing.T) {
	p, tp, fake := optInRig(t, true)

	_, plain := tp.Tracer("optin-test").Start(context.Background(), "harness.task.create")
	plain.End()
	modelCall(t, "gen-off-before-0001")()
	straddle := modelCall(t, "gen-straddle-0001") // starts while OFF
	flush(t, tp)
	if body := fake.combinedBody(); !bytes.Contains(body, []byte("harness.task.create")) {
		t.Fatal("the ordinary span never reached the fake Fleet — the lane is closed and the test proves nothing")
	} else if bytes.Contains(body, []byte("gen_ai.")) || bytes.Contains(body, []byte("gen-off-before-0001")) {
		t.Fatal("a model-call span reached Fleet with the setting OFF (default)")
	}
	if p.ModelCallSharing() {
		t.Fatal("sharing must default OFF")
	}

	time.Sleep(2 * time.Millisecond)
	p.SetModelCallSharing(true)
	time.Sleep(2 * time.Millisecond)
	straddle() // ends after the toggle; started before it
	modelCall(t, "gen-after-on-0001")()
	flush(t, tp)
	body := fake.combinedBody()
	if !bytes.Contains(body, []byte("gen-after-on-0001")) || !bytes.Contains(body, []byte(genaitrace.SpanNameChat)) {
		t.Fatal("with the setting ON a model-call span started after the toggle did not reach Fleet")
	}
	if bytes.Contains(body, []byte("gen-straddle-0001")) || bytes.Contains(body, []byte("gen-off-before-0001")) {
		t.Fatal("turning the setting ON back-filled a call that started before it")
	}

	p.SetModelCallSharing(false)
	modelCall(t, "gen-off-again-0001")()
	flush(t, tp)
	body = fake.combinedBody()
	if bytes.Contains(body, []byte("gen-off-again-0001")) {
		t.Fatal("turning the setting OFF did not stop model-call export")
	}
	if bytes.Contains(body, []byte(optInPrompt)) {
		t.Fatal("content reached Fleet")
	}
	st := p.Status()
	if st.ModelCallsShared || st.ModelCallsExported != 1 || st.ModelCallsWithheld != 3 {
		t.Fatalf("status = shared %v exported %d withheld %d, want false/1/3 (filter still in path)",
			st.ModelCallsShared, st.ModelCallsExported, st.ModelCallsWithheld)
	}
}

// TestOTLPPipeline_ModelCallSharing_SeparateFromGeneralConsent: the opt-in
// is its own gate. With general admission closed (no consent, no
// diagnostics class) the opt-in alone carries model-call spans while an
// ordinary span stays home; and a Fleet snapshot that opts the proposed
// ModelCallTelemetryClass OUT narrows it back to nothing.
func TestOTLPPipeline_ModelCallSharing_SeparateFromGeneralConsent(t *testing.T) {
	p, tp, fake := optInRig(t, false)
	p.SetModelCallSharing(true)
	time.Sleep(2 * time.Millisecond)

	_, plain := tp.Tracer("optin-test").Start(context.Background(), "harness.task.create")
	plain.End()
	modelCall(t, "gen-sep-0001")()
	flush(t, tp)
	body := fake.combinedBody()
	if !bytes.Contains(body, []byte("gen-sep-0001")) {
		t.Fatal("the model-call opt-in did not carry the span on its own")
	}
	if bytes.Contains(body, []byte("harness.task.create")) {
		t.Fatal("an ordinary span left without general consent")
	}

	p.SetTelemetryOptIns([]TelemetryOptInItem{{Class: ModelCallTelemetryClass, OptedIn: false}})
	modelCall(t, "gen-denied-0001")()
	flush(t, tp)
	if bytes.Contains(fake.combinedBody(), []byte("gen-denied-0001")) {
		t.Fatalf("Fleet's %s opt-out did not narrow model-call export", ModelCallTelemetryClass)
	}
}
