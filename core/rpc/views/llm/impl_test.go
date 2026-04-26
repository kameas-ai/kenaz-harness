package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// recordingSink captures every Emit call for assertion.
type recordingSink struct {
	mu    sync.Mutex
	calls []sinkCall
}

type sinkCall struct {
	topic   string
	payload any
}

func (r *recordingSink) Emit(topic string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sinkCall{topic: topic, payload: payload})
}

func (r *recordingSink) topicCount(topic string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.topic == topic {
			n++
		}
	}
	return n
}

func (r *recordingSink) lastClosed() (StreamClosedPayload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.calls) - 1; i >= 0; i-- {
		if p, ok := r.calls[i].payload.(StreamClosedPayload); ok {
			return p, true
		}
	}
	return StreamClosedPayload{}, false
}

// fakeRegistry is a minimal corellm.Registry suitable for tests.
type fakeRegistry struct {
	profiles map[string]corellm.ProviderProfile
	stream   corellm.Stream
	streamErr error
	loaded   []corellm.ProviderProfile
}

func (f *fakeRegistry) RegisterAdapter(_ corellm.ProviderAdapter) {}
func (f *fakeRegistry) LoadProfiles(profs []corellm.ProviderProfile) error {
	if f.profiles == nil {
		f.profiles = map[string]corellm.ProviderProfile{}
	}
	for _, p := range profs {
		f.profiles[p.ID] = p
	}
	f.loaded = append(f.loaded, profs...)
	return nil
}
func (f *fakeRegistry) Profile(id string) (corellm.ProviderProfile, error) {
	p, ok := f.profiles[id]
	if !ok {
		return corellm.ProviderProfile{}, errors.New("not found")
	}
	return p, nil
}
func (f *fakeRegistry) Profiles() []corellm.ProviderProfile {
	out := make([]corellm.ProviderProfile, 0, len(f.profiles))
	for _, p := range f.profiles {
		out = append(out, p)
	}
	return out
}
func (f *fakeRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult {
	return nil
}
func (f *fakeRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return f.stream, nil
}

// fakeStream is a deterministic Stream impl driven by tests.
type fakeStream struct {
	chunks   []corellm.StreamEvent
	final    corellm.Response
	finalErr error
	mu       sync.Mutex
	out      chan corellm.StreamEvent
	once     sync.Once
	cancelled bool
}

func (s *fakeStream) Events() <-chan corellm.StreamEvent {
	s.once.Do(func() {
		s.out = make(chan corellm.StreamEvent, len(s.chunks))
		for _, c := range s.chunks {
			s.out <- c
		}
		close(s.out)
	})
	return s.out
}
func (s *fakeStream) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
	return nil
}
func (s *fakeStream) Final() (corellm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled {
		return corellm.Response{}, &corellm.ErrCancelled{Reason: "test"}
	}
	return s.final, s.finalErr
}

func TestAPI_StartStream_PumpsChunksAndCloses(t *testing.T) {
	stream := &fakeStream{
		chunks: []corellm.StreamEvent{
			{Kind: corellm.StreamText, Text: "hello"},
			{Kind: corellm.StreamText, Text: " world"},
			{Kind: corellm.StreamFinish, Finish: "end_turn"},
		},
		final: corellm.Response{FinishReason: "end_turn"},
	}
	reg := &fakeRegistry{
		profiles: map[string]corellm.ProviderProfile{
			"p": {ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-5",
				Cred: corellm.CredentialReference{Kind: "env", Locator: "K"}},
		},
		stream: stream,
	}
	sink := &recordingSink{}
	api := New(Config{Registry: reg, Sink: sink})

	subID, err := api.StartStream(context.Background(), "p", "", "")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("empty sub id")
	}

	// Wait for the pump to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sink.topicCount("llm:stream-closed") > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := sink.topicCount("llm:stream-chunk"); got != 3 {
		t.Fatalf("expected 3 stream-chunk emissions, got %d", got)
	}
	closed, ok := sink.lastClosed()
	if !ok {
		t.Fatal("expected llm:stream-closed payload")
	}
	if closed.Reason != "completed" {
		t.Fatalf("reason = %q, want completed", closed.Reason)
	}
	if closed.SubID != subID {
		t.Fatalf("sub id mismatch: %q vs %q", closed.SubID, subID)
	}
	if closed.FinishReason != "end_turn" {
		t.Fatalf("finish_reason = %q", closed.FinishReason)
	}
}

func TestAPI_StopStreamCancels(t *testing.T) {
	// A stream that never closes its events channel until Cancel is
	// called.
	out := make(chan corellm.StreamEvent)
	stream := &cancelOnlyStream{out: out}
	reg := &fakeRegistry{
		profiles: map[string]corellm.ProviderProfile{
			"p": {ID: "p", Kind: "anthropic", Model: "x",
				Cred: corellm.CredentialReference{Kind: "env", Locator: "K"}},
		},
		stream: stream,
	}
	sink := &recordingSink{}
	api := New(Config{Registry: reg, Sink: sink})

	subID, err := api.StartStream(context.Background(), "p", "", "")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	if err := api.StopStream(context.Background(), subID); err != nil {
		t.Fatalf("StopStream: %v", err)
	}

	closed, ok := sink.lastClosed()
	if !ok {
		t.Fatal("expected llm:stream-closed after stop")
	}
	if closed.Reason != "stop-called" {
		t.Fatalf("reason = %q, want stop-called", closed.Reason)
	}
}

func TestAPI_StartStream_RegistryError(t *testing.T) {
	reg := &fakeRegistry{streamErr: errors.New("rate limited")}
	api := New(Config{Registry: reg})
	_, err := api.StartStream(context.Background(), "missing", "", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAPI_NotWired(t *testing.T) {
	api := New(Config{})
	// ListProviders without store/bundles returns an empty list — the
	// chassis still boots, just nothing to render.
	provs, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(provs) != 0 {
		t.Fatalf("expected empty provider list, got %d", len(provs))
	}
	// StartStream without a registry must reject so the UI surfaces the
	// "connector not wired" path.
	if _, err := api.StartStream(context.Background(), "x", "", ""); err == nil {
		t.Fatal("expected error when registry not wired")
	}
}

// fakeHistory satisfies SessionMessageReader for buildMessages tests.
type fakeHistory struct {
	stored []SessionMessage
}

func (f *fakeHistory) ListMessages(_ context.Context, _ string) ([]SessionMessage, error) {
	return f.stored, nil
}

// fakeContextReader satisfies SessionMessageReader + SessionContextReader
// so buildMessages exercises the legacy Mission-A code path.
type fakeContextReader struct {
	stored []SessionMessage
	prompt string
	kind   string
}

func (f *fakeContextReader) ListMessages(_ context.Context, _ string) ([]SessionMessage, error) {
	return f.stored, nil
}
func (f *fakeContextReader) SystemPromptFor(_ context.Context, _ string) (string, string, error) {
	return f.prompt, f.kind, nil
}

// fakeResolver supplies ResolvedAttachments to buildMessages.
type fakeResolver struct {
	out []ResolvedAttachment
	err error
}

func (f *fakeResolver) ListResolved(_ context.Context, _ string) ([]ResolvedAttachment, error) {
	return f.out, f.err
}

// TestBuildMessages_AttachmentsPrependedInOrder asserts the
// AttachmentsResolver path takes precedence and emits system messages
// in the declared [global..., project..., session...] order, ahead of
// the conversation history.
func TestBuildMessages_AttachmentsPrependedInOrder(t *testing.T) {
	t.Parallel()
	api := New(Config{
		History: &fakeHistory{stored: []SessionMessage{
			{Role: "user", Content: "hello"},
		}},
		Attachments: &fakeResolver{out: []ResolvedAttachment{
			{Content: "global pre", Kind: "system"},
			{Content: "project pre", Kind: "system"},
			{Content: "session pre", Kind: "system"},
		}},
	})
	msgs, err := api.buildMessages(context.Background(), "s1", "model-x", "anthropic")
	if err != nil {
		t.Fatalf("buildMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4 (3 system + 1 user)", len(msgs))
	}
	wantOrder := []struct {
		role string
		text string
	}{
		{"system", "global pre"},
		{"system", "project pre"},
		{"system", "session pre"},
		{"user", "hello"},
	}
	for i, w := range wantOrder {
		if string(msgs[i].Role) != w.role {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, w.role)
		}
		if len(msgs[i].Content) == 0 || msgs[i].Content[0].Text != w.text {
			t.Errorf("msgs[%d].Content = %+v, want %q", i, msgs[i].Content, w.text)
		}
	}
}

// TestBuildMessages_AttachmentsSkipsUserKind verifies that "user"-kind
// resolved attachments are NOT prepended as system messages — the
// caller is responsible for surfacing them as user turns in `stored`.
func TestBuildMessages_AttachmentsSkipsUserKind(t *testing.T) {
	t.Parallel()
	api := New(Config{
		History: &fakeHistory{stored: []SessionMessage{
			{Role: "user", Content: "hi"},
		}},
		Attachments: &fakeResolver{out: []ResolvedAttachment{
			{Content: "user-seed visible", Kind: "user"},
			{Content: "system invisible", Kind: "system"},
		}},
	})
	msgs, err := api.buildMessages(context.Background(), "s1", "", "")
	if err != nil {
		t.Fatalf("buildMessages: %v", err)
	}
	// Only the system attachment is prepended; user kind is skipped.
	if len(msgs) != 2 {
		t.Fatalf("msgs = %d, want 2", len(msgs))
	}
	if string(msgs[0].Role) != "system" || msgs[0].Content[0].Text != "system invisible" {
		t.Errorf("first = %+v", msgs[0])
	}
	if string(msgs[1].Role) != "user" || msgs[1].Content[0].Text != "hi" {
		t.Errorf("second = %+v", msgs[1])
	}
}

// TestBuildMessages_LegacyContextReaderFallback asserts that when no
// AttachmentsResolver is wired, buildMessages still honours Mission A's
// SessionContextReader probe — the one-release compat buffer.
func TestBuildMessages_LegacyContextReaderFallback(t *testing.T) {
	t.Parallel()
	api := New(Config{
		History: &fakeContextReader{
			stored: []SessionMessage{{Role: "user", Content: "hey"}},
			prompt: "be brief",
			kind:   "system",
		},
	})
	msgs, err := api.buildMessages(context.Background(), "s1", "", "")
	if err != nil {
		t.Fatalf("buildMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d", len(msgs))
	}
	if msgs[0].Content[0].Text != "be brief" {
		t.Errorf("first = %+v", msgs[0])
	}
}

// cancelOnlyStream blocks Events() until Cancel is called, simulating
// a long-running upstream call.
type cancelOnlyStream struct {
	out       chan corellm.StreamEvent
	mu        sync.Mutex
	cancelled bool
}

func (s *cancelOnlyStream) Events() <-chan corellm.StreamEvent { return s.out }
func (s *cancelOnlyStream) Cancel() error {
	s.mu.Lock()
	if !s.cancelled {
		s.cancelled = true
		close(s.out)
	}
	s.mu.Unlock()
	return nil
}
func (s *cancelOnlyStream) Final() (corellm.Response, error) {
	return corellm.Response{}, &corellm.ErrCancelled{Reason: "stop"}
}
