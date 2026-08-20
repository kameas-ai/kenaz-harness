package registry

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/events"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// fakeAdapter records inputs and emits a deterministic response.
type fakeAdapter struct {
	kind    string
	calls   int32
	failN   int32 // first N calls return ErrTransient
	final   llm.Response
	chunks  []llm.StreamEvent
	wantCap llm.CapabilityDescriptor
	gotCred []byte
	gotReq  llm.GenerationRequest // last GenerationRequest passed to Stream (post-pipeline)

	// respByCall, when non-nil, selects the Response returned on the Nth
	// call (1-indexed, clamped to the last entry) instead of the fixed
	// `final` field. Used by the WP04 structured-output tests to script a
	// first-attempt-invalid / retry-valid (or retry-still-invalid)
	// sequence.
	respByCall []llm.Response

	mu           sync.Mutex
	gotReqByCall []llm.GenerationRequest // every GenerationRequest passed to Stream, in call order
}

func (a *fakeAdapter) Kind() string { return a.kind }
func (a *fakeAdapter) Capabilities(_ string) llm.CapabilityDescriptor {
	return a.wantCap
}
func (a *fakeAdapter) Stream(_ context.Context, req llm.GenerationRequest, _ llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	n := atomic.AddInt32(&a.calls, 1)
	if int32(n) <= a.failN {
		return nil, &llm.ErrTransient{Status: 503, Message: "blip"}
	}
	a.gotCred = append([]byte(nil), cred...)
	a.gotReq = req
	a.mu.Lock()
	a.gotReqByCall = append(a.gotReqByCall, req)
	a.mu.Unlock()

	final := a.final
	if a.respByCall != nil {
		idx := int(n) - 1
		if idx >= len(a.respByCall) {
			idx = len(a.respByCall) - 1
		}
		final = a.respByCall[idx]
	}
	return &fakeStream{chunks: a.chunks, final: final}, nil
}

func (a *fakeAdapter) callReqs() []llm.GenerationRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]llm.GenerationRequest, len(a.gotReqByCall))
	copy(out, a.gotReqByCall)
	return out
}

type fakeStream struct {
	chunks   []llm.StreamEvent
	final    llm.Response
	out      chan llm.StreamEvent
	once     bool
	finalErr error
	cancelled bool
}

func (s *fakeStream) Events() <-chan llm.StreamEvent {
	if s.once {
		return s.out
	}
	s.once = true
	s.out = make(chan llm.StreamEvent, len(s.chunks))
	for _, c := range s.chunks {
		s.out <- c
	}
	close(s.out)
	return s.out
}
func (s *fakeStream) Cancel() error { s.cancelled = true; return nil }
func (s *fakeStream) Final() (llm.Response, error) {
	if s.cancelled {
		return llm.Response{}, &llm.ErrCancelled{}
	}
	return s.final, s.finalErr
}

func newReg(t *testing.T) (*Registry, *events.MemorySink) {
	t.Helper()
	sink := &events.MemorySink{}
	emit := events.New(sink)
	emit.SetClock(func() time.Time { return time.Unix(0, 0) })
	r, err := New(Options{Emitter: emit})
	if err != nil {
		t.Fatal(err)
	}
	return r, sink
}

func TestRegistry_RegisterAndProfileLookup(t *testing.T) {
	r, _ := newReg(t)
	prof := llm.ProviderProfile{
		ID: "p1", Kind: "anthropic", Model: "claude-sonnet-4-7",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Profile("p1")
	if err != nil || got.ID != "p1" {
		t.Fatalf("profile lookup: %+v %v", got, err)
	}
	if _, err := r.Profile("missing"); err == nil {
		t.Fatal("expected miss on unknown id")
	}
}

func TestRegistry_DuplicateProfileIDRejected(t *testing.T) {
	r, _ := newReg(t)
	prof := llm.ProviderProfile{
		ID: "dup", Kind: "anthropic", Model: "x",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof, prof}); err == nil {
		t.Fatal("expected dup error in single batch")
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err == nil {
		t.Fatal("expected dup error across batches")
	}
}

func TestRegistry_StreamCapabilityRejection(t *testing.T) {
	r, sink := newReg(t)
	// Ollama defaults reject vision.
	r.RegisterAdapter(&fakeAdapter{kind: "ollama"})
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "llama3.1",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	req := llm.GenerationRequest{
		ProfileID: "p", Attachments: []llm.Attachment{{Kind: "image"}},
	}
	_, err := r.Stream(context.Background(), req)
	var ce *llm.ErrCapabilityUnsupported
	if !errors.As(err, &ce) {
		t.Fatalf("expected capability rejection, got %v", err)
	}
	// The audit log must record capability_rejected and NOT
	// request_submitted (US2.2 — rejected before any wire call).
	var sawRej, sawReq bool
	for _, k := range sink.Kinds() {
		if k == events.KindCapabilityRejected {
			sawRej = true
		}
		if k == events.KindRequestSubmitted {
			sawReq = true
		}
	}
	if !sawRej {
		t.Fatal("expected capability_rejected event")
	}
	if sawReq {
		t.Fatal("must not emit request_submitted on capability rejection")
	}
}

// TestRegistry_StreamCapabilityRejection_ResponseFormat is
// structured-output-is-reachable-01PMZE14 WP02's AC-003: a request that
// opts into ResponseFormat{Mode:"json_schema"} against a model whose
// capability row does not advertise structured_output must be refused
// by the gate — attributably, naming provider/model/capability — before
// any wire call, exactly like the pre-existing vision-attachment
// rejection above. The gate mechanism itself is not new (spec §2:
// "Capability gate for CapStructuredOutput / CapGrammar — WIRED"); this
// pins that the new agentgraph->chat seam's ResponseFormat assignment
// (WP02) reaches it correctly for the mode this mission adds.
func TestRegistry_StreamCapabilityRejection_ResponseFormat(t *testing.T) {
	fsys := fstest.MapFS{
		"data/nostruct.yaml": &fstest.MapFile{Data: []byte(`
provider: nostruct
models:
  - match: "no-struct-model*"
    streaming: true
    structured_output: false
`)},
	}
	cat, err := capabilities.Load(fsys, "data")
	if err != nil {
		t.Fatalf("capabilities.Load: %v", err)
	}
	sink := &events.MemorySink{}
	emit := events.New(sink)
	emit.SetClock(func() time.Time { return time.Unix(0, 0) })
	r, err := New(Options{Emitter: emit, Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	r.RegisterAdapter(&fakeAdapter{kind: "nostruct"})
	prof := llm.ProviderProfile{ID: "p", Kind: "nostruct", Model: "no-struct-model-1",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(`{"type":"object"}`)},
	}
	_, err = r.Stream(context.Background(), req)
	var ce *llm.ErrCapabilityUnsupported
	if !errors.As(err, &ce) {
		t.Fatalf("expected capability rejection, got %v", err)
	}
	if ce.Provider != "nostruct" || ce.Model != "no-struct-model-1" {
		t.Errorf("error must name provider/model: %#v", ce)
	}
	found := false
	for _, c := range ce.Capabilities {
		if c == llm.CapStructuredOutput {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CapStructuredOutput in missing capabilities, got %#v", ce.Capabilities)
	}
	var sawRej, sawReq bool
	for _, k := range sink.Kinds() {
		if k == events.KindCapabilityRejected {
			sawRej = true
		}
		if k == events.KindRequestSubmitted {
			sawReq = true
		}
	}
	if !sawRej {
		t.Fatal("expected capability_rejected event")
	}
	if sawReq {
		t.Fatal("must not emit request_submitted on capability rejection — refused before any wire call")
	}
}

// TestRegistry_StreamRejectsEmptyToolCallID verifies the adapter-agnostic
// pre-send guard: a request whose tool_use carries an empty id is rejected
// before any wire call, so a provider never sees an empty tool_call_id (which
// it would bounce with a cryptic 400 mid-conversation).
func TestRegistry_StreamRejectsEmptyToolCallID(t *testing.T) {
	r, _ := newReg(t)
	adapter := &fakeAdapter{kind: "ollama"}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{ID: "p", Kind: "ollama", Model: "llama3.1",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	req := llm.GenerationRequest{
		ProfileID: "p",
		Messages: []llm.Message{
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "tool_use", ToolUse: &llm.ToolUse{ID: "", Name: "bash"}},
			}},
		},
	}
	_, err := r.Stream(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for empty tool_use id, got nil")
	}
	if !strings.Contains(err.Error(), "tool_use") {
		t.Fatalf("error should name the offending tool_use, got %v", err)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 0 {
		t.Fatalf("adapter must not be dispatched on validation failure, calls=%d", n)
	}
}

// Pipeline ordering test: profile → CapabilityGate → PolicyGuard →
// CredentialResolver → AuditEmitter → RetryMiddleware → adapter.
func TestRegistry_PipelineOrdering(t *testing.T) {
	const key = "TEST_REG_API_KEY"
	os.Setenv(key, "secret-bytes")
	defer os.Unsetenv(key)

	r, sink := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	adapter := &fakeAdapter{
		kind: "anthropic",
		chunks: []llm.StreamEvent{
			{Kind: llm.StreamText, Text: "hello"},
			{Kind: llm.StreamFinish, Finish: "stop"},
		},
		final: llm.Response{
			Content:      []llm.ContentBlock{{Type: "text", Text: "hello"}},
			FinishReason: "stop",
			Usage:        llm.Usage{InputTokens: 5, OutputTokens: 1},
		},
	}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "p1", Kind: "anthropic", Model: "claude-sonnet-4-7",
		Cred: llm.CredentialReference{Kind: "env", Locator: key},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	stream, err := r.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p1", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	// Drain the events channel.
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Attempts != 1 {
		t.Fatalf("expected 1 attempt (first-try), got %d", resp.Attempts)
	}
	if string(adapter.gotCred) != "secret-bytes" {
		t.Fatalf("adapter did not receive resolved cred bytes: %q", adapter.gotCred)
	}

	// Audit ordering: request_submitted → stream_chunk × N → response_final.
	kinds := sink.Kinds()
	if len(kinds) < 3 {
		t.Fatalf("not enough events: %v", kinds)
	}
	if kinds[0] != events.KindRequestSubmitted {
		t.Fatalf("expected request_submitted first, got %s", kinds[0])
	}
	if kinds[len(kinds)-1] != events.KindResponseFinal {
		t.Fatalf("expected response_final last, got %s", kinds[len(kinds)-1])
	}
	for i := 1; i < len(kinds)-1; i++ {
		if kinds[i] != events.KindStreamChunk {
			t.Fatalf("expected stream_chunk at idx %d, got %s", i, kinds[i])
		}
	}
}

func TestRegistry_RetryOnTransientFailure(t *testing.T) {
	const key = "TEST_REG_API_KEY_RETRY"
	os.Setenv(key, "x")
	defer os.Unsetenv(key)

	r, sink := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	adapter := &fakeAdapter{
		kind:  "openai",
		failN: 1,
		chunks: []llm.StreamEvent{
			{Kind: llm.StreamFinish, Finish: "stop"},
		},
		final: llm.Response{FinishReason: "stop"},
	}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "openai", Model: "gpt-4o-mini",
		Cred:  llm.CredentialReference{Kind: "env", Locator: key},
		Retry: &llm.RetryPolicy{MaxAttempts: 3, BaseMS: 1, MaxMS: 1, Jitter: "full"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	stream, err := r.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Attempts < 2 {
		t.Fatalf("expected ≥ 2 attempts (retry), got %d", resp.Attempts)
	}
	var sawRetry bool
	for _, k := range sink.Kinds() {
		if k == events.KindRetryAttempted {
			sawRetry = true
			break
		}
	}
	if !sawRetry {
		t.Fatal("expected llm/retry_attempted event")
	}
}

func TestRegistry_PolicyDeniedBeforeWire(t *testing.T) {
	r, sink := newReg(t)
	r.policy = denyAllGuard{}
	r.RegisterAdapter(&fakeAdapter{kind: "anthropic"})
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"}}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}
	_, err := r.Stream(context.Background(), llm.GenerationRequest{ProfileID: "p"})
	var pd *llm.ErrPolicyDenied
	if !errors.As(err, &pd) {
		t.Fatalf("expected policy-denied, got %v", err)
	}
	var sawDenied, sawSubmitted bool
	for _, k := range sink.Kinds() {
		if k == events.KindPolicyDenied {
			sawDenied = true
		}
		if k == events.KindRequestSubmitted {
			sawSubmitted = true
		}
	}
	if !sawDenied {
		t.Fatal("expected policy_denied event")
	}
	if sawSubmitted {
		t.Fatal("must not submit request after policy denial")
	}
}

type denyAllGuard struct{}

func (denyAllGuard) Allow(_ context.Context, _ llm.GenerationRequest, _ llm.ProviderProfile) error {
	return &llm.ErrPolicyDenied{Reason: "test deny"}
}

func TestRegistry_PreflightAll_BedrockMissingRegion(t *testing.T) {
	r, sink := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	prof := llm.ProviderProfile{
		ID: "b", Kind: "bedrock", Model: "anthropic.claude-3-sonnet",
		Cred: llm.CredentialReference{Kind: "aws_profile", Locator: "default"},
		// Region intentionally empty.
	}
	// Bypass the validator by inserting directly — we want PreflightAll
	// to also catch the violation independently of LoadProfiles.
	r.profiles[prof.ID] = prof
	results := r.PreflightAll(context.Background())
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Resolved {
		t.Fatal("bedrock without region must not be marked resolved")
	}
	var sawFail bool
	for _, k := range sink.Kinds() {
		if k == events.KindPreflightFailed {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatal("expected preflight_failed event")
	}
}

// Confirm Go's type identity: registry.Registry implements llm.Registry.
func TestRegistry_ImplementsLLMRegistry(t *testing.T) {
	var _ llm.Registry = (*Registry)(nil)
}

func TestRegistry_MergePersonalProfilesAppends(t *testing.T) {
	r, _ := newReg(t)
	bundle := llm.ProviderProfile{
		ID: "bundle-prof", Kind: "anthropic", Model: "claude-sonnet",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{bundle}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	personal := llm.ProviderProfile{
		ID: "personal-prof", Kind: "openai", Model: "gpt-4o-mini",
		Cred: llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/personal-prof"},
	}
	if err := r.MergePersonalProfiles([]llm.ProviderProfile{personal}); err != nil {
		t.Fatalf("MergePersonalProfiles: %v", err)
	}
	got, err := r.Profile("personal-prof")
	if err != nil || got.Kind != "openai" {
		t.Fatalf("expected personal profile loaded, got %+v err=%v", got, err)
	}
}

func TestRegistry_MergePersonalProfilesBundleWinsOnCollision(t *testing.T) {
	r, _ := newReg(t)
	bundle := llm.ProviderProfile{
		ID: "shared-id", Kind: "anthropic", Model: "claude-sonnet",
		Cred: llm.CredentialReference{Kind: "env", Locator: "K"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{bundle}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	personal := llm.ProviderProfile{
		ID: "shared-id", Kind: "openai", Model: "gpt-4o-mini",
		Cred: llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/shared-id"},
	}
	if err := r.MergePersonalProfiles([]llm.ProviderProfile{personal}); err != nil {
		t.Fatalf("MergePersonalProfiles: %v", err)
	}
	got, err := r.Profile("shared-id")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if got.Kind != "anthropic" {
		t.Fatalf("expected bundle profile to win, got %+v", got)
	}
}

func TestRegistry_MergePersonalProfilesValidatesBatch(t *testing.T) {
	r, _ := newReg(t)
	bad := llm.ProviderProfile{ID: "", Kind: "anthropic", Model: "x",
		Cred: llm.CredentialReference{Kind: "keychain", Locator: "k"}}
	if err := r.MergePersonalProfiles([]llm.ProviderProfile{bad}); err == nil {
		t.Fatal("expected validation error on empty ID")
	}
	if len(r.Profiles()) != 0 {
		t.Fatalf("expected registry to be unchanged after invalid merge")
	}
}

// authFailAdapter returns *ErrAuth on every Stream call.
type authFailAdapter struct {
	kind   string
	status int
	msg    string
}

func (a *authFailAdapter) Kind() string { return a.kind }
func (a *authFailAdapter) Capabilities(_ string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{Provider: a.kind}
}
func (a *authFailAdapter) Stream(_ context.Context, _ llm.GenerationRequest, _ llm.ProviderProfile, _ []byte) (llm.Stream, error) {
	return nil, &llm.ErrAuth{Status: a.status, Message: a.msg}
}

// TestRegistry_AuthFailureDecoratedAsErrProviderAuthFailed asserts WP01:
//   - An adapter returning *ErrAuth causes Stream to return an error
//     matchable by errors.As(err, &*ErrProviderAuthFailed) AND by
//     errors.As(err, &*ErrAuth) via the Unwrap chain.
//   - ErrProviderAuthFailed.Reason is populated from the wrapped ErrAuth.Message.
func TestRegistry_AuthFailureDecoratedAsErrProviderAuthFailed(t *testing.T) {
	const key = "TEST_REG_AUTH_FAIL_KEY"
	os.Setenv(key, "dummy")
	defer os.Unsetenv(key)

	r, _ := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	r.RegisterAdapter(&authFailAdapter{kind: "anthropic", status: 401, msg: "invalid api_key"})
	prof := llm.ProviderProfile{
		ID: "auth-fail", Kind: "anthropic", Model: "claude-sonnet-4-7",
		Cred: llm.CredentialReference{Kind: "env", Locator: key},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatal(err)
	}

	_, err := r.Stream(context.Background(), llm.GenerationRequest{ProfileID: "auth-fail"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Primary assertion: registry-level decorated type.
	var authFailed *llm.ErrProviderAuthFailed
	if !errors.As(err, &authFailed) {
		t.Fatalf("expected *ErrProviderAuthFailed in chain, got %T: %v", err, err)
	}
	if authFailed.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", authFailed.Provider, "anthropic")
	}
	if authFailed.ProfileID != "auth-fail" {
		t.Errorf("ProfileID = %q, want %q", authFailed.ProfileID, "auth-fail")
	}
	if authFailed.Reason != "invalid api_key" {
		t.Errorf("Reason = %q, want %q", authFailed.Reason, "invalid api_key")
	}

	// Secondary assertion: raw *ErrAuth still reachable via Unwrap.
	var authBare *llm.ErrAuth
	if !errors.As(err, &authBare) {
		t.Fatalf("expected *ErrAuth via Unwrap chain, got %T: %v", err, err)
	}
	if authBare.Status != 401 {
		t.Errorf("ErrAuth.Status = %d, want 401", authBare.Status)
	}
}

// TestRegistry_GeminiRegisteredWhenFlagOn verifies gemini adapter is registered
// when HARNESS_GOOGLE_GEMINI is unset (default) or "on".
func TestRegistry_GeminiRegisteredWhenFlagOn(t *testing.T) {
	// Clear any existing flag value for this test.
	old := os.Getenv("HARNESS_GOOGLE_GEMINI")
	_ = os.Unsetenv("HARNESS_GOOGLE_GEMINI")
	defer func() {
		if old != "" {
			_ = os.Setenv("HARNESS_GOOGLE_GEMINI", old)
		}
	}()

	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := r.Adapter("gemini")
	if a == nil {
		t.Error("expected gemini adapter to be registered when HARNESS_GOOGLE_GEMINI is unset")
	}
}

// TestRegistry_Evict_ThenReload verifies that Evict removes a profile so
// that LoadProfiles can replace it (the UpdateProvider seam).
func TestRegistry_Evict_ThenReload(t *testing.T) {
	t.Parallel()
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	profV1 := llm.ProviderProfile{
		ID:    "p-evict-test",
		Kind:  "openai",
		Model: "gpt-4o",
		Cred:  llm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/evict-test"},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{profV1}); err != nil {
		t.Fatalf("LoadProfiles v1: %v", err)
	}

	// Evict, then reload with new model.
	if err := r.Evict(profV1.ID); err != nil {
		t.Fatalf("Evict: %v", err)
	}

	profV2 := profV1
	profV2.Model = "gpt-4o-mini"
	if err := r.LoadProfiles([]llm.ProviderProfile{profV2}); err != nil {
		t.Fatalf("LoadProfiles v2: %v", err)
	}

	got, err := r.Profile(profV1.ID)
	if err != nil {
		t.Fatalf("Profile after reload: %v", err)
	}
	if got.Model != "gpt-4o-mini" {
		t.Errorf("expected model gpt-4o-mini after reload, got %q", got.Model)
	}
}

// TestRegistry_Evict_Missing is a no-op that must not return an error.
func TestRegistry_Evict_Missing(t *testing.T) {
	t.Parallel()
	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Evict("does-not-exist"); err != nil {
		t.Errorf("Evict of unknown id returned error: %v", err)
	}
}

// TestRegistry_GeminiNotRegisteredWhenFlagOff verifies gemini adapter is absent
// when HARNESS_GOOGLE_GEMINI=off.
func TestRegistry_GeminiNotRegisteredWhenFlagOff(t *testing.T) {
	old := os.Getenv("HARNESS_GOOGLE_GEMINI")
	_ = os.Setenv("HARNESS_GOOGLE_GEMINI", "off")
	defer func() {
		if old != "" {
			_ = os.Setenv("HARNESS_GOOGLE_GEMINI", old)
		} else {
			_ = os.Unsetenv("HARNESS_GOOGLE_GEMINI")
		}
	}()

	r, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := r.Adapter("gemini")
	if a != nil {
		t.Error("expected gemini adapter NOT to be registered when HARNESS_GOOGLE_GEMINI=off")
	}
}

// ─── WP03: KnobPolicy.Apply wired into Stream ──────────────────────────────
// (model-request-path-live-01PMDL01). These tests use a synthetic Catalog
// (rather than the embedded default) so ProviderCapabilities knob fields
// are controlled precisely and the test isn't coupled to the shipped
// capability YAML's per-model coverage of the newer knob fields.
//
// NB: as discovered while writing these tests, none of the shipped
// core/llm/capabilities/data/*.yaml model entries currently set the
// seed/top_k/top_p/frequency_penalty/presence_penalty/parallel_tool_calls
// fields — and Catalog.DescribeRich's applyRichEntry unconditionally
// overwrites (not merges) those fields from the matched model entry, so
// every *known* model currently resolves all of these to false regardless
// of the provider-level `defaults:` block. That's a pre-existing data/loader
// gap in core/llm/capabilities (not touched by this WP) worth a follow-up;
// seeded here in this test's doc comment rather than silently worked around.

func testKnobProfile() llm.ProviderProfile {
	return llm.ProviderProfile{
		ID: "p", Kind: "testknob", Model: "test-knob-model-1",
		Cred: llm.CredentialReference{Kind: "env", Locator: "TEST_REG_KNOB_KEY"},
	}
}

// testKnobCatalog returns a Catalog for a synthetic "testknob" provider with
// one model ("test-knob-model*") that: does not support seed, does support
// top_k, and supports reasoning via token_budget style only.
func testKnobCatalog(t *testing.T) *capabilities.Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"data/testknob.yaml": &fstest.MapFile{Data: []byte(`
provider: testknob
models:
  - match: "test-knob-model*"
    streaming: true
    tool_calling: true
    seed: false
    top_k: true
    reasoning: true
    reasoning_style: "token_budget"
`)},
	}
	cat, err := capabilities.Load(fsys, "data")
	if err != nil {
		t.Fatalf("testKnobCatalog: %v", err)
	}
	return cat
}

func newRegWithResolvedCred(t *testing.T) (*Registry, *fakeAdapter) {
	t.Helper()
	os.Setenv("TEST_REG_KNOB_KEY", "secret-bytes")
	t.Cleanup(func() { os.Unsetenv("TEST_REG_KNOB_KEY") })

	sink := &events.MemorySink{}
	emit := events.New(sink)
	emit.SetClock(func() time.Time { return time.Unix(0, 0) })
	r, err := New(Options{Emitter: emit, Catalog: testKnobCatalog(t)})
	if err != nil {
		t.Fatal(err)
	}
	r.resolver = credref.New(secrets.NewMemoryBackend())
	adapter := &fakeAdapter{
		kind: "testknob",
		chunks: []llm.StreamEvent{
			{Kind: llm.StreamFinish, Finish: "stop"},
		},
		final: llm.Response{FinishReason: "stop"},
	}
	r.RegisterAdapter(adapter)
	if err := r.LoadProfiles([]llm.ProviderProfile{testKnobProfile()}); err != nil {
		t.Fatal(err)
	}
	return r, adapter
}

// TestRegistry_StreamKnobPolicy_DropsUnsupportedKnob verifies an unsupported
// knob (seed, unsupported per testKnobCatalog) is stripped
// from the request before it reaches the adapter, and the stream still
// succeeds (silent drop, not a refusal).
func TestRegistry_StreamKnobPolicy_DropsUnsupportedKnob(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	seed := 42
	req := llm.GenerationRequest{
		ProfileID: "p",
		Knobs:     &llm.RequestKnobs{Seed: &seed},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	if adapter.gotReq.Knobs == nil {
		t.Fatal("adapter did not receive knobs at all")
	}
	if adapter.gotReq.Knobs.Seed != nil {
		t.Errorf("want seed dropped before reaching adapter, got %v", adapter.gotReq.Knobs.Seed)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 1 {
		t.Fatalf("expected exactly 1 adapter call, got %d", n)
	}
}

// TestRegistry_StreamKnobPolicy_PassesSupportedKnob verifies a knob the
// profile's model DOES support (top_k, per testKnobCatalog) reaches the
// adapter unmodified.
func TestRegistry_StreamKnobPolicy_PassesSupportedKnob(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	topK := 40
	req := llm.GenerationRequest{
		ProfileID: "p",
		Knobs:     &llm.RequestKnobs{TopK: &topK},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	if adapter.gotReq.Knobs == nil || adapter.gotReq.Knobs.TopK == nil {
		t.Fatal("want top_k retained on the request reaching the adapter")
	}
	if *adapter.gotReq.Knobs.TopK != 40 {
		t.Errorf("want top_k=40 unmodified, got %d", *adapter.gotReq.Knobs.TopK)
	}
}

// TestRegistry_StreamKnobPolicy_RefusesIrreconcilableReasoning verifies the
// registry refuses (does not dispatch to the adapter) when the request's
// reasoning knob is irreconcilable with the model's reasoning style — here
// an OpenAI-style effort string against an Anthropic token-budget model.
func TestRegistry_StreamKnobPolicy_RefusesIrreconcilableReasoning(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	req := llm.GenerationRequest{
		ProfileID: "p",
		Knobs: &llm.RequestKnobs{
			Reasoning: &llm.ReasoningConfig{OpenAIEffort: "high"},
		},
	}
	_, err := r.Stream(context.Background(), req)
	if err == nil {
		t.Fatal("expected ErrUnsupportedFeature refusal, got nil")
	}
	if !llm.IsUnsupportedFeature(err) {
		t.Errorf("want ErrUnsupportedFeature, got %T: %v", err, err)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 0 {
		t.Fatalf("adapter must not be dispatched on knob-policy refusal, calls=%d", n)
	}
}

// ─── Registry-seam bugfix: KnobPolicy over the req.Params channel ─────────
// (PR #257 review blocker). No production caller ever populates req.Knobs
// directly — every real caller (the chat seam's llm_provider_adapter.go,
// plus every adapter's own body-builder) wires per-node sampling knobs
// onto the untyped req.Params map instead. These tests start where
// production actually starts: a request carrying knobs in Params, not
// Knobs. Assertions are against the captured adapter request's Params
// (and, where relevant, Knobs), never against a caller-constructed Knobs
// value the production path never sets.

// TestRegistry_StreamKnobPolicy_ParamsChannel_DropsUnsupportedKnob verifies
// an unsupported knob (seed, per testKnobCatalog) arriving via req.Params —
// the channel azure/adapter.go and the chat seam actually use — is removed
// from Params before the adapter is invoked, and req.Knobs is left nil
// (the request never used that channel).
func TestRegistry_StreamKnobPolicy_ParamsChannel_DropsUnsupportedKnob(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	req := llm.GenerationRequest{
		ProfileID: "p",
		Params:    map[string]any{"seed": 42},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	if _, ok := adapter.gotReq.Params["seed"]; ok {
		t.Errorf("want seed removed from Params before reaching adapter, got %v", adapter.gotReq.Params["seed"])
	}
	if adapter.gotReq.Knobs != nil {
		t.Errorf("want Knobs left nil (request only used Params), got %+v", adapter.gotReq.Knobs)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 1 {
		t.Fatalf("expected exactly 1 adapter call, got %d", n)
	}
}

// TestRegistry_StreamKnobPolicy_ParamsChannel_PassesSupportedKnob verifies a
// knob the model DOES support (top_k, per testKnobCatalog) reaches the
// adapter via Params unmodified.
func TestRegistry_StreamKnobPolicy_ParamsChannel_PassesSupportedKnob(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	req := llm.GenerationRequest{
		ProfileID: "p",
		Params:    map[string]any{"top_k": 40},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	got, ok := adapter.gotReq.Params["top_k"]
	if !ok {
		t.Fatal("want top_k retained in Params on the request reaching the adapter")
	}
	if got != 40 {
		t.Errorf("want top_k=40 unmodified, got %v", got)
	}
}

// TestRegistry_StreamKnobPolicy_ParamsChannel_PreservesUnrelatedKeys
// verifies Params keys KnobPolicy doesn't govern (max_tokens, temperature)
// survive sanitisation untouched, alongside a dropped unsupported knob in
// the same map — Params is a general-purpose channel and this seam must
// not wipe entries it has no opinion about.
func TestRegistry_StreamKnobPolicy_ParamsChannel_PreservesUnrelatedKeys(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	req := llm.GenerationRequest{
		ProfileID: "p",
		Params: map[string]any{
			"seed":        42, // unsupported: dropped
			"max_tokens":  1024,
			"temperature": 0.7,
		},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	if _, ok := adapter.gotReq.Params["seed"]; ok {
		t.Error("want seed dropped")
	}
	if v, ok := adapter.gotReq.Params["max_tokens"]; !ok || v != 1024 {
		t.Errorf("want max_tokens=1024 preserved, got %v (present=%v)", v, ok)
	}
	if v, ok := adapter.gotReq.Params["temperature"]; !ok || v != 0.7 {
		t.Errorf("want temperature=0.7 preserved, got %v (present=%v)", v, ok)
	}
}

// TestRegistry_StreamKnobPolicy_ParamsChannel_RefusalStillFires verifies
// the irreconcilable-mismatch refusal path (reasoning style mismatch, via
// the existing req.Knobs surface) still fires and the adapter is never
// dispatched even when the same request also carries an unrelated,
// supported knob on the Params channel — i.e. merging the two channels
// doesn't paper over a refusal.
func TestRegistry_StreamKnobPolicy_ParamsChannel_RefusalStillFires(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	req := llm.GenerationRequest{
		ProfileID: "p",
		Params:    map[string]any{"top_k": 40},
		Knobs: &llm.RequestKnobs{
			Reasoning: &llm.ReasoningConfig{OpenAIEffort: "high"},
		},
	}
	_, err := r.Stream(context.Background(), req)
	if err == nil {
		t.Fatal("expected ErrUnsupportedFeature refusal, got nil")
	}
	if !llm.IsUnsupportedFeature(err) {
		t.Errorf("want ErrUnsupportedFeature, got %T: %v", err, err)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 0 {
		t.Fatalf("adapter must not be dispatched on knob-policy refusal, calls=%d", n)
	}
}

// TestRegistry_StreamKnobPolicy_KnobsTakesPrecedenceOverParams verifies the
// documented precedence: when the same knob is set on both channels with
// different values, the explicit req.Knobs value wins, and the winning
// value is written back into Params too (so adapters that only read Params
// see the value that actually governs, not a stale Params-derived one).
func TestRegistry_StreamKnobPolicy_KnobsTakesPrecedenceOverParams(t *testing.T) {
	r, adapter := newRegWithResolvedCred(t)
	knobsTopK := 99
	req := llm.GenerationRequest{
		ProfileID: "p",
		Params:    map[string]any{"top_k": 40},
		Knobs:     &llm.RequestKnobs{TopK: &knobsTopK},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, _ = stream.Final()
	if adapter.gotReq.Knobs == nil || adapter.gotReq.Knobs.TopK == nil || *adapter.gotReq.Knobs.TopK != 99 {
		t.Fatalf("want Knobs.TopK=99 to win, got %+v", adapter.gotReq.Knobs)
	}
	if got, ok := adapter.gotReq.Params["top_k"]; !ok || got != 99 {
		t.Errorf("want Params[top_k] reconciled to the winning value 99, got %v (present=%v)", got, ok)
	}
}

// --- WP04: structured-output validate + repair-once
// (model-request-path-live-01PMDL01) ---

// nameRequiredSchema requires a top-level string "name" field.
const nameRequiredSchema = `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`

func newRegForStructured(t *testing.T) (*Registry, *fakeAdapter) {
	t.Helper()
	const key = "TEST_REG_STRUCTURED_KEY"
	os.Setenv(key, "secret-bytes")
	t.Cleanup(func() { os.Unsetenv(key) })

	r, _ := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	adapter := &fakeAdapter{kind: "anthropic"}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "anthropic", Model: "claude-sonnet-4-7",
		Cred: llm.CredentialReference{Kind: "env", Locator: key},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	return r, adapter
}

func jsonResp(text string) llm.Response {
	return llm.Response{
		Content:      []llm.ContentBlock{{Type: "text", Text: text}},
		FinishReason: "stop",
	}
}

// TestRegistry_StructuredOutput_PassesValidFirstTry verifies a response that
// validates against the schema on the first attempt passes through
// unmodified and triggers no retry.
func TestRegistry_StructuredOutput_PassesValidFirstTry(t *testing.T) {
	r, adapter := newRegForStructured(t)
	adapter.respByCall = []llm.Response{jsonResp(`{"name":"ok"}`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}
	if got := (llm.Message{Content: resp.Content}).Text(); got != `{"name":"ok"}` {
		t.Errorf("unexpected content: %q", got)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 1 {
		t.Fatalf("expected exactly 1 adapter call (no retry needed), got %d", n)
	}
}

// TestRegistry_StructuredOutput_RetriesOnceAndRepairs verifies a first
// response that fails schema validation triggers exactly one corrective
// retry naming the specific field violation, and the repaired response is
// what the caller ultimately observes.
func TestRegistry_StructuredOutput_RetriesOnceAndRepairs(t *testing.T) {
	r, adapter := newRegForStructured(t)
	adapter.respByCall = []llm.Response{
		jsonResp(`{}`),               // missing required "name" -> invalid
		jsonResp(`{"name":"fixed"}`), // corrective retry -> valid
	}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}
	if got := (llm.Message{Content: resp.Content}).Text(); got != `{"name":"fixed"}` {
		t.Errorf("want repaired response surfaced to caller, got %q", got)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 2 {
		t.Fatalf("expected exactly 2 adapter calls (1 retry), got %d", n)
	}

	// The corrective retry's request must name the specific field
	// violation (".name") so the model knows what to fix.
	reqs := adapter.callReqs()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 recorded requests, got %d", len(reqs))
	}
	last := reqs[1]
	if len(last.Messages) == 0 {
		t.Fatal("expected retry request to carry appended messages")
	}
	hintMsg := last.Messages[len(last.Messages)-1]
	if hintMsg.Role != llm.RoleUser {
		t.Errorf("want corrective hint as a user message, got role %q", hintMsg.Role)
	}
	if !strings.Contains(hintMsg.Text(), "name") {
		t.Errorf("want corrective hint to name the offending field, got %q", hintMsg.Text())
	}
}

// TestRegistry_StructuredOutput_TypedErrorAfterRepairFails verifies that
// when both the first attempt and the single corrective retry fail schema
// validation, the caller sees the typed llm.ErrResponseValidationFailed.
func TestRegistry_StructuredOutput_TypedErrorAfterRepairFails(t *testing.T) {
	r, adapter := newRegForStructured(t)
	adapter.respByCall = []llm.Response{
		jsonResp(`{}`), // missing "name" -> invalid
		jsonResp(`{}`), // still missing "name" -> invalid
	}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, ferr := stream.Final()
	if ferr == nil {
		t.Fatal("expected ErrResponseValidationFailed, got nil")
	}
	var verr *llm.ErrResponseValidationFailed
	if !errors.As(ferr, &verr) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T: %v", ferr, ferr)
	}
	if !strings.Contains(verr.SchemaError, "name") {
		t.Errorf("want SchemaError to name the offending field, got %q", verr.SchemaError)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 2 {
		t.Fatalf("expected exactly 2 adapter calls (1 retry, no more), got %d", n)
	}
}

// TestRegistry_StructuredOutput_StrictValidationSkipsRetry verifies
// StrictValidation:true fails immediately on the first violation with no
// corrective retry.
func TestRegistry_StructuredOutput_StrictValidationSkipsRetry(t *testing.T) {
	r, adapter := newRegForStructured(t)
	adapter.respByCall = []llm.Response{jsonResp(`{}`)}

	req := llm.GenerationRequest{
		ProfileID: "p",
		ResponseFormat: &llm.ResponseFormat{
			Mode: "json_schema", Schema: []byte(nameRequiredSchema), StrictValidation: true,
		},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	_, ferr := stream.Final()
	var verr *llm.ErrResponseValidationFailed
	if !errors.As(ferr, &verr) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T: %v", ferr, ferr)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 1 {
		t.Fatalf("strict mode must not retry, expected 1 call, got %d", n)
	}
}

// TestRegistry_StructuredOutput_GrammarModeSkipsValidation verifies
// Mode=="grammar" responses are passed through with no JSON-schema
// re-validation (grammar constraints are enforced by the runtime's
// token sampler, not this post-hoc validator).
func TestRegistry_StructuredOutput_GrammarModeSkipsValidation(t *testing.T) {
	// Grammar mode requests CapGrammar (llm.go GenerationRequest.RequiredCapabilities);
	// Anthropic doesn't advertise it, so use Ollama (llama.cpp backend, native
	// GBNF support per capabilities/data/ollama.yaml) to reach the structured
	// stream at all.
	const key = "TEST_REG_STRUCTURED_GRAMMAR_KEY"
	os.Setenv(key, "secret-bytes")
	t.Cleanup(func() { os.Unsetenv(key) })
	r, _ := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	adapter := &fakeAdapter{kind: "ollama"}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "ollama", Model: "llama3.1",
		Cred: llm.CredentialReference{Kind: "env", Locator: key},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	adapter.respByCall = []llm.Response{jsonResp(`not json at all`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "grammar"},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: unexpected error for grammar mode, got %v", ferr)
	}
	if got := (llm.Message{Content: resp.Content}).Text(); got != "not json at all" {
		t.Errorf("want passthrough content, got %q", got)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 1 {
		t.Fatalf("grammar mode must not retry, expected 1 call, got %d", n)
	}
}
