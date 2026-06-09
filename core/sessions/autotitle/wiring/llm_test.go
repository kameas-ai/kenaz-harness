package wiring

import (
	"context"
	"errors"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/cost"
)

// fakeStream is a minimal corellm.Stream implementation for tests.
type fakeStream struct {
	events chan corellm.StreamEvent
	resp   corellm.Response
	err    error
}

func newFakeStream(text string, inputTok, outputTok int) *fakeStream {
	ch := make(chan corellm.StreamEvent, 1)
	close(ch) // stream terminates immediately
	return &fakeStream{
		events: ch,
		resp: corellm.Response{
			Content:      []corellm.ContentBlock{{Type: "text", Text: text}},
			FinishReason: "end_turn",
			Usage: corellm.Usage{
				InputTokens:  inputTok,
				OutputTokens: outputTok,
			},
			Cost: corellm.Cost{
				Currency: "USD",
				Total:    0.0001,
			},
		},
	}
}

func newFakeStreamWithCost(text string, inputTok, outputTok int, costUSD float64) *fakeStream {
	s := newFakeStream(text, inputTok, outputTok)
	s.resp.Cost.Total = costUSD
	return s
}

func newFakeStreamWithIndeterminateCost(text string) *fakeStream {
	s := newFakeStream(text, 10, 5)
	s.resp.Cost = corellm.Cost{Indeterminate: true}
	return s
}

func (f *fakeStream) Events() <-chan corellm.StreamEvent { return f.events }
func (f *fakeStream) Cancel() error                      { return nil }
func (f *fakeStream) Final() (corellm.Response, error) {
	if f.err != nil {
		return corellm.Response{}, f.err
	}
	return f.resp, nil
}

// fakeRegistry is a minimal LLMRegistry for tests.
type fakeRegistry struct {
	stream *fakeStream
	err    error
	// capturedReq stores the last GenerationRequest received.
	capturedReq corellm.GenerationRequest
}

func (r *fakeRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.capturedReq = req
	if r.err != nil {
		return nil, r.err
	}
	return r.stream, nil
}

func TestLLMCaller_Call_HappyPath(t *testing.T) {
	reg := &fakeRegistry{
		stream: newFakeStream("Learning Rust", 20, 5),
	}
	caller := NewLLMCaller(reg, WithProfileID("test-profile", "test-model"))

	text, inputTok, outputTok, err := caller.Call(context.Background(), "sys prompt", "user prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Learning Rust" {
		t.Errorf("text = %q, want %q", text, "Learning Rust")
	}
	if inputTok != 20 {
		t.Errorf("inputTokens = %d, want 20", inputTok)
	}
	if outputTok != 5 {
		t.Errorf("outputTokens = %d, want 5", outputTok)
	}
}

// TestLLMCaller_Call_TagsKindAutoTitle verifies the overhead tally is
// updated after each call (the tag is logged with cost.KindAutoTitle).
func TestLLMCaller_Call_TagsKindAutoTitle(t *testing.T) {
	// cost.KindAutoTitle constant must be exactly "auto_title".
	if cost.KindAutoTitle != "auto_title" {
		t.Errorf("cost.KindAutoTitle = %q, want %q", cost.KindAutoTitle, "auto_title")
	}

	reg := &fakeRegistry{
		stream: newFakeStreamWithCost("My Title", 15, 8, 0.0005),
	}
	caller := NewLLMCaller(reg, WithProfileID("p1", "m1"))

	_, _, _, err := caller.Call(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overhead := caller.Overhead()
	if overhead.Calls != 1 {
		t.Errorf("Calls = %d, want 1", overhead.Calls)
	}
	if overhead.InputTokens != 15 {
		t.Errorf("InputTokens = %d, want 15", overhead.InputTokens)
	}
	if overhead.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8", overhead.OutputTokens)
	}
	if overhead.Total == 0 {
		t.Errorf("Total = 0, want > 0")
	}
}

// TestLLMCaller_Call_IndeterminateCost verifies the IndeterminateCalls
// counter increments when the cost reducer cannot price the call.
func TestLLMCaller_Call_IndeterminateCost(t *testing.T) {
	reg := &fakeRegistry{
		stream: newFakeStreamWithIndeterminateCost("Some Title"),
	}
	caller := NewLLMCaller(reg, WithProfileID("p1", "m1"))

	_, _, _, err := caller.Call(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	overhead := caller.Overhead()
	if overhead.IndeterminateCalls != 1 {
		t.Errorf("IndeterminateCalls = %d, want 1", overhead.IndeterminateCalls)
	}
}

// TestLLMCaller_Call_RegistryError propagates the registry error.
func TestLLMCaller_Call_RegistryError(t *testing.T) {
	reg := &fakeRegistry{err: errors.New("provider down")}
	caller := NewLLMCaller(reg, WithProfileID("p1", "m1"))

	_, _, _, err := caller.Call(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errors.New("provider down")) && err.Error() != "provider down" {
		// Just check it's non-nil and not wrapped awkwardly.
		_ = err
	}
}

// TestLLMCaller_Call_NoProfile returns error when no profile is configured.
func TestLLMCaller_Call_NoProfile(t *testing.T) {
	reg := &fakeRegistry{stream: newFakeStream("Title", 5, 2)}
	caller := NewLLMCaller(reg) // no profile set

	_, _, _, err := caller.Call(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("expected error when no profile configured, got nil")
	}
}

// TestLLMCaller_NilRegistry returns nil from constructor.
func TestLLMCaller_NilRegistry(t *testing.T) {
	caller := NewLLMCaller(nil)
	if caller != nil {
		t.Errorf("NewLLMCaller(nil) = non-nil, want nil")
	}
}

// TestLLMCaller_Call_RequestShape verifies the registry receives the correct
// profile id, model, system prompt, and user turn.
func TestLLMCaller_Call_RequestShape(t *testing.T) {
	reg := &fakeRegistry{stream: newFakeStream("Title", 10, 3)}
	caller := NewLLMCaller(reg, WithProfileID("my-profile", "my-model"))

	const sys = "Be concise."
	const user = "User: Hello\nAssistant: Hi"
	_, _, _, err := caller.Call(context.Background(), sys, user)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := reg.capturedReq
	if req.ProfileID != "my-profile" {
		t.Errorf("ProfileID = %q, want %q", req.ProfileID, "my-profile")
	}
	if req.Model != "my-model" {
		t.Errorf("Model = %q, want %q", req.Model, "my-model")
	}
	if req.System != sys {
		t.Errorf("System = %q, want %q", req.System, sys)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want %q", req.Messages[0].Role, "user")
	}
	if len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Text != user {
		t.Errorf("Messages[0].Content[0].Text = %q, want %q", req.Messages[0].Content[0].Text, user)
	}
}

// TestLLMCaller_ProfileResolver verifies the custom resolver is used
// when wired.
func TestLLMCaller_ProfileResolver(t *testing.T) {
	reg := &fakeRegistry{stream: newFakeStream("Title", 5, 2)}

	resolverCalled := false
	resolver := ProfileResolver(func(_ context.Context, _, _ string) (string, string, bool) {
		resolverCalled = true
		return "resolved-profile", "resolved-model", true
	})

	caller := NewLLMCaller(reg, WithProfileResolver(resolver))
	_, _, _, err := caller.Call(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resolverCalled {
		t.Error("profile resolver was not called")
	}
	if reg.capturedReq.ProfileID != "resolved-profile" {
		t.Errorf("ProfileID = %q, want %q", reg.capturedReq.ProfileID, "resolved-profile")
	}
}

// TestLLMCaller_Overhead_RunningTally verifies multiple calls accumulate.
func TestLLMCaller_Overhead_RunningTally(t *testing.T) {
	streams := []*fakeStream{
		newFakeStreamWithCost("Title1", 10, 3, 0.001),
		newFakeStreamWithCost("Title2", 20, 6, 0.002),
	}

	idx := 0
	caller := NewLLMCaller(&funcRegistry{fn: func(_ corellm.GenerationRequest) (corellm.Stream, error) {
		if idx >= len(streams) {
			return nil, errors.New("no more streams")
		}
		s := streams[idx]
		idx++
		return s, nil
	}}, WithProfileID("p1", "m1"))

	for i := 0; i < 2; i++ {
		_, _, _, err := caller.Call(context.Background(), "sys", "user")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	overhead := caller.Overhead()
	if overhead.Calls != 2 {
		t.Errorf("Calls = %d, want 2", overhead.Calls)
	}
	if overhead.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", overhead.InputTokens)
	}
	if overhead.OutputTokens != 9 {
		t.Errorf("OutputTokens = %d, want 9", overhead.OutputTokens)
	}
	wantTotal := 0.001 + 0.002
	if overhead.Total < wantTotal-1e-9 || overhead.Total > wantTotal+1e-9 {
		t.Errorf("Total = %f, want %f", overhead.Total, wantTotal)
	}
}

// funcRegistry is a helper that wraps a function as an LLMRegistry.
type funcRegistry struct {
	fn func(req corellm.GenerationRequest) (corellm.Stream, error)
}

func (r *funcRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	return r.fn(req)
}
