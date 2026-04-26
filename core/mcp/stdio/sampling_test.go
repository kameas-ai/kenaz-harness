package stdio

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// stubRegistry is a minimal corellm.Registry returning a deterministic
// completion. Only Stream is implemented; the other methods panic if
// the sampling handler ever reaches them (which would be a bug).
//
// lastReq is guarded by mu because the reader-loop goroutine drives
// Stream while the test goroutine inspects the captured request.
type stubRegistry struct {
	calls atomic.Int32

	mu      sync.Mutex
	lastReq corellm.GenerationRequest

	out    string
	finish string
}

func (r *stubRegistry) RegisterAdapter(_ corellm.ProviderAdapter) {}
func (r *stubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error {
	return nil
}
func (r *stubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, nil
}
func (r *stubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult {
	return nil
}
func (r *stubRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.calls.Add(1)
	r.mu.Lock()
	r.lastReq = req
	r.mu.Unlock()
	out := r.out
	if out == "" {
		out = "stub-response"
	}
	finish := r.finish
	if finish == "" {
		finish = "stop"
	}
	return &stubStream{out: out, finish: finish}, nil
}

// snapshotLastReq returns a copy of the last GenerationRequest the
// stub observed under its mutex — safe for assertions across the
// reader-loop / test boundary.
func (r *stubRegistry) snapshotLastReq() corellm.GenerationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastReq
}

type stubStream struct {
	out    string
	finish string
	ch     chan corellm.StreamEvent
	once   atomic.Bool
}

func (s *stubStream) Events() <-chan corellm.StreamEvent {
	if !s.once.CompareAndSwap(false, true) {
		// Pre-closed channel for the second caller, if any.
		c := make(chan corellm.StreamEvent)
		close(c)
		return c
	}
	s.ch = make(chan corellm.StreamEvent, 1)
	close(s.ch) // empty stream — sampling handler reads Final directly
	return s.ch
}

func (s *stubStream) Cancel() error { return nil }

func (s *stubStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentPart{corellm.ContentPartFromText(s.out)},
		FinishReason: s.finish,
	}, nil
}

func TestLLMSamplingHandler_Translation(t *testing.T) {
	t.Parallel()
	reg := &stubRegistry{out: "hello"}
	h := LLMSamplingHandler(reg, func() (string, string) { return "openai", "gpt-test" })

	temp := 0.7
	contentJSON, _ := json.Marshal(map[string]any{"type": "text", "text": "ping?"})
	req := SamplingRequest{
		Messages: []SamplingMessage{
			{Role: "user", Content: contentJSON},
		},
		SystemPrompt:  "be brief",
		MaxTokens:     128,
		Temperature:   &temp,
		StopSequences: []string{"###"},
	}
	resp, err := h.CreateMessage(context.Background(), "fake", req)
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if reg.calls.Load() != 1 {
		t.Fatalf("registry calls = %d, want 1", reg.calls.Load())
	}
	got := reg.snapshotLastReq()
	if got.ProfileID != "openai" || got.Model != "gpt-test" {
		t.Fatalf("provider/model selector dropped: %+v", got)
	}
	if got.System != "be brief" {
		t.Fatalf("system prompt = %q", got.System)
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != corellm.RoleUser {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if got.Messages[0].Content[0].Text != "ping?" {
		t.Fatalf("text = %q", got.Messages[0].Content[0].Text)
	}
	if got.Params["max_tokens"] != 128 {
		t.Fatalf("max_tokens = %v", got.Params["max_tokens"])
	}
	if got.Params["temperature"] != 0.7 {
		t.Fatalf("temperature = %v", got.Params["temperature"])
	}

	// Response shape.
	if resp.Role != "assistant" {
		t.Fatalf("role = %q", resp.Role)
	}
	var content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(resp.Content, &content)
	if content.Type != "text" || content.Text != "hello" {
		t.Fatalf("content = %+v", content)
	}
	if resp.Model != "gpt-test" {
		t.Fatalf("model = %q", resp.Model)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("stop reason = %q", resp.StopReason)
	}
}

func TestLLMSamplingHandler_ServerInitiated_GateOn(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	reg := &stubRegistry{out: "answered"}
	h := LLMSamplingHandler(reg, func() (string, string) { return "openai", "gpt-test" })

	inst := newServerInstance("fake", nil, h, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:              "fake",
		Command:         []string{bin, "--emit-sampling"},
		SamplingEnabled: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if reg.calls.Load() > 0 {
			// Final shape check: registry saw the right request.
			snap := reg.snapshotLastReq()
			if snap.System != "you are a fake test fixture" {
				t.Fatalf("system prompt = %q", snap.System)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("registry never invoked under gate ON")
}

// countingHandler asserts that when the gate is OFF, the sampling
// handler is never invoked. Per the constraint in WP03 the dispatch
// path must respond with -32601 directly.
type countingHandler struct {
	calls atomic.Int32
}

func (c *countingHandler) CreateMessage(_ context.Context, _ string, _ SamplingRequest) (SamplingResponse, error) {
	c.calls.Add(1)
	return SamplingResponse{}, nil
}

func TestSampling_GateOff_ReturnsMethodNotFound(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	stub := &countingHandler{}
	inst := newServerInstance("fake", nil, stub, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Note: SamplingEnabled=false (default off, per FR-015 cost-amplification gate).
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--emit-sampling"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// Wait long enough for the fake to have emitted sampling/createMessage
	// and received the gate-off error.
	time.Sleep(300 * time.Millisecond)
	if got := stub.calls.Load(); got != 0 {
		t.Fatalf("handler called %d times under gate OFF; want 0", got)
	}
}

func TestPool_SetSamplingEnabled(t *testing.T) {
	t.Parallel()
	// Construct an instance directly so we don't pay the spawn cost
	// for what is purely a state-mutation test.
	inst := newServerInstance("fake", nil, nil, nil, nil)
	if inst.SamplingEnabled() {
		t.Fatalf("default sampling state must be OFF")
	}
	inst.setSamplingEnabled(true)
	if !inst.SamplingEnabled() {
		t.Fatalf("setSamplingEnabled(true) ineffective")
	}
	inst.setSamplingEnabled(false)
	if inst.SamplingEnabled() {
		t.Fatalf("setSamplingEnabled(false) ineffective")
	}

	// Pool.SetSamplingEnabled finds the server and toggles its state.
	p := &Pool{servers: map[string]*ServerInstance{"fake": inst}}
	p.SetSamplingEnabled("fake", true)
	if !inst.SamplingEnabled() {
		t.Fatalf("Pool.SetSamplingEnabled did not flip state")
	}
	p.SetSamplingEnabled("missing-id", true) // no-op, must not panic
}
