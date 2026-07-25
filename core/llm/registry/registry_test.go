package registry

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
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
}

func (a *fakeAdapter) Kind() string { return a.kind }
func (a *fakeAdapter) Capabilities(_ string) llm.CapabilityDescriptor {
	return a.wantCap
}
func (a *fakeAdapter) Stream(_ context.Context, _ llm.GenerationRequest, _ llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	n := atomic.AddInt32(&a.calls, 1)
	if int32(n) <= a.failN {
		return nil, &llm.ErrTransient{Status: 503, Message: "blip"}
	}
	a.gotCred = append([]byte(nil), cred...)
	return &fakeStream{chunks: a.chunks, final: a.final}, nil
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
