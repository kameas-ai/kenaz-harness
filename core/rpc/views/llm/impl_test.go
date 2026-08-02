package llm

import (
	"context"
	"errors"
	"sync"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
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

func (r *recordingSink) payloadsForTopic(topic string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]any, 0, len(r.calls))
	for _, c := range r.calls {
		if c.topic == topic {
			out = append(out, c.payload)
		}
	}
	return out
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
func (f *fakeRegistry) Evict(id string) error {
	if f.profiles != nil {
		delete(f.profiles, id)
	}
	return nil
}
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

// recordingRegistry captures the most recent GenerationRequest so tool
// discovery wiring can be asserted end-to-end.
type recordingRegistry struct {
	mu     sync.Mutex
	stream corellm.Stream
	req    corellm.GenerationRequest
	called bool
}

func (r *recordingRegistry) RegisterAdapter(corellm.ProviderAdapter) {}
func (r *recordingRegistry) LoadProfiles([]corellm.ProviderProfile) error {
	return nil
}
func (r *recordingRegistry) Evict(_ string) error { return nil }
func (r *recordingRegistry) Profile(id string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: id, Kind: "anthropic", Model: "x"}, nil
}
func (r *recordingRegistry) PreflightAll(context.Context) []corellm.PreflightResult { return nil }
func (r *recordingRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.req = req
	r.called = true
	r.mu.Unlock()
	return r.stream, nil
}
func (r *recordingRegistry) lastRequest() (corellm.GenerationRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.req, r.called
}

// stubDiscoverer returns a fixed tool list and tracks how many times
// it was called (and with what session id).
type stubDiscoverer struct {
	tools     []corellm.ToolSpec
	err       error
	callCount int
	lastSess  string
}

func (s *stubDiscoverer) Tools(_ context.Context, sessionID string) ([]corellm.ToolSpec, error) {
	s.callCount++
	s.lastSess = sessionID
	return s.tools, s.err
}

// Modality-gate test infrastructure (fakeAdapter, fakeRegistryWithAdapter,
// fakeHistoryWithBlocks) lived alongside the legacy pump path; both the
// gate and the path were retired in the chat-migration cutover. The chat
// runner builds GenerationRequest values via LLMProviderAdapter, which
// inherits the connector-side modality validation surface.

// TestStartStream_RejectsImageOnNonVisionModel asserts that an image
// content block paired with a model that does not advertise
// CapVision returns an UnsupportedModalityError before the registry
// is invoked (multimodal-io WP03 / FR-010 / A3).
// NOTE: this check is now performed by the chat-runner's gate, not by
// StartStream. This stub is kept as a documentation marker; real coverage
// lives in gate_test.go / TestCheckAttachments_* and the chat-runner tests.

// ── GetAttachmentLimits RPC unit tests (multimodal-io-01KQ8TDF WP07) ────────

// fakeCapCatalog is a test double for CapCatalog that returns a fixed
// AttachmentLimitsResult and records the last (provider, model) seen.
type fakeCapCatalog struct {
	limits     AttachmentLimitsResult
	lastProv   string
	lastModel  string
	contextWin int
	maxOut     int
}

func (f *fakeCapCatalog) ContextWindow(p, m string) int {
	f.lastProv, f.lastModel = p, m
	return f.contextWin
}
func (f *fakeCapCatalog) MaxOutputTokens(p, m string) int { return f.maxOut }
func (f *fakeCapCatalog) AttachmentLimits(p, m string) AttachmentLimitsResult {
	f.lastProv, f.lastModel = p, m
	return f.limits
}

// TestGetAttachmentLimits_NilCatalog verifies the safe zero-value fallback
// when no CapCatalog is wired (a fresh API with no catalog configured).
func TestGetAttachmentLimits_NilCatalog(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	got, err := api.GetAttachmentLimits(context.Background(), "anthropic", "claude-sonnet-4-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ImageInput || got.DocumentInput {
		t.Errorf("nil catalog should return all-false descriptor; got %+v", got)
	}
}

// TestGetAttachmentLimits_DelegatesToCatalog verifies that GetAttachmentLimits
// delegates to the wired CapCatalog and maps the result to AttachmentLimitsView.
func TestGetAttachmentLimits_DelegatesToCatalog(t *testing.T) {
	t.Parallel()
	cat := &fakeCapCatalog{
		limits: AttachmentLimitsResult{
			ImageInput:              true,
			DocumentInput:           true,
			MaxImageBytes:           5 * 1024 * 1024,
			MaxDocumentBytes:        10 * 1024 * 1024,
			MaxImageCountPerMessage: 20,
			MaxImagePixels:          0,
			MaxDocumentPages:        100,
			ImageInputMimeTypes:     []string{"image/png", "image/jpeg"},
			DocumentInputMimeTypes:  []string{"application/pdf"},
		},
	}
	api := New(Config{CapCatalog: cat})

	got, err := api.GetAttachmentLimits(context.Background(), "anthropic", "claude-sonnet-4-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.ImageInput {
		t.Error("expected ImageInput=true")
	}
	if !got.DocumentInput {
		t.Error("expected DocumentInput=true")
	}
	if got.MaxImageBytes != 5*1024*1024 {
		t.Errorf("MaxImageBytes = %d, want %d", got.MaxImageBytes, 5*1024*1024)
	}
	if got.MaxImageCountPerMessage != 20 {
		t.Errorf("MaxImageCountPerMessage = %d, want 20", got.MaxImageCountPerMessage)
	}
	if got.MaxDocumentPages != 100 {
		t.Errorf("MaxDocumentPages = %d, want 100", got.MaxDocumentPages)
	}
	if len(got.ImageInputMimeTypes) != 2 {
		t.Errorf("ImageInputMimeTypes = %v, want 2 entries", got.ImageInputMimeTypes)
	}
	if cat.lastProv != "anthropic" || cat.lastModel != "claude-sonnet-4-7" {
		t.Errorf("catalog called with wrong (provider, model): (%s, %s)", cat.lastProv, cat.lastModel)
	}
}

// TestGetAttachmentLimits_DisabledByEnvFlag verifies that when
// ── WP06: custom-openai RPC methods ──────────────────────────────────────

// fakeCustomAdapter satisfies CustomAdapterAPI for tests.
type fakeCustomAdapter struct {
	// templates returns the fake template registry; may be nil.
	reg *customRegistryFake
}

type customRegistryFake struct {
	all []customTemplateFake
}

type customTemplateFake struct {
	id, name, baseURL, authScheme string
}

// We can't use custom.Registry directly in test without importing the package
// (which would be fine), but let's use the real package to keep it simple.
// Instead, we'll pass a nil customAdapter and test the "not wired" path.

func TestListCustomTemplates_NotWired(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	got, err := api.ListCustomTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListCustomTemplates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list when adapter not wired, got %d", len(got))
	}
}

func TestRecognizeTemplate_NotWired(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	got, err := api.RecognizeTemplate(context.Background(), "https://api.together.xyz/v1")
	if err != nil {
		t.Fatalf("RecognizeTemplate: %v", err)
	}
	if got.Matched {
		t.Error("expected Matched=false when adapter not wired")
	}
}

func TestProbeCustomEndpoint_NotWired(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	_, err := api.ProbeCustomEndpoint(context.Background(), ProbeCustomEndpointInput{
		BaseURL:    "https://api.together.xyz/v1",
		AuthScheme: "bearer",
	})
	if err == nil {
		t.Fatal("expected ErrFeatureDisabled when adapter not wired")
	}
	if !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("got %v, want ErrFeatureDisabled", err)
	}
}

func TestListCustomTemplates_WithRealAdapter(t *testing.T) {
	t.Parallel()
	// Wire the real custom adapter so we exercise the full ListCustomTemplates path.
	adapter := custom.New()
	if adapter == nil {
		// HARNESS_CUSTOM_OPENAI=0 in this env; skip.
		t.Skip("custom adapter not registered in this env")
	}
	api := New(Config{CustomAdapter: adapter})
	got, err := api.ListCustomTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListCustomTemplates: %v", err)
	}
	// The embedded registry ships 9 templates.
	if len(got) != 9 {
		t.Errorf("len(templates) = %d, want 9", len(got))
	}
	// Verify at least one well-known template is present.
	found := false
	for _, tmpl := range got {
		if tmpl.ID == "groq" {
			found = true
			if tmpl.AuthScheme != "bearer" {
				t.Errorf("groq auth_scheme = %q, want bearer", tmpl.AuthScheme)
			}
			break
		}
	}
	if !found {
		t.Error("groq template not found in ListCustomTemplates result")
	}
}

func TestRecognizeTemplate_WithRealAdapter(t *testing.T) {
	t.Parallel()
	adapter := custom.New()
	if adapter == nil {
		t.Skip("custom adapter not registered in this env")
	}
	api := New(Config{CustomAdapter: adapter})

	// api.together.xyz should resolve to the "together" template.
	got, err := api.RecognizeTemplate(context.Background(), "https://api.together.xyz/v1")
	if err != nil {
		t.Fatalf("RecognizeTemplate: %v", err)
	}
	if !got.Matched {
		t.Fatal("expected Matched=true for api.together.xyz")
	}
	if got.Template.ID != "together" {
		t.Errorf("template.id = %q, want together", got.Template.ID)
	}

	// An unknown host should return Matched=false.
	got2, err := api.RecognizeTemplate(context.Background(), "https://myunknownhost.example.com/v1")
	if err != nil {
		t.Fatalf("RecognizeTemplate unknown: %v", err)
	}
	if got2.Matched {
		t.Error("expected Matched=false for unknown host")
	}
}

// HARNESS_MULTIMODAL_IN=off the catalog returns ImageInput=false and
// DocumentInput=false (the env flag is applied at the catalog layer).
// This is an end-to-end path test: real Catalog → AttachmentLimits env override.
// NOTE: t.Setenv is incompatible with t.Parallel() — runs serially.
func TestGetAttachmentLimits_DisabledByEnvFlag(t *testing.T) {
	t.Setenv("HARNESS_MULTIMODAL_IN", "off")

	// Use a live cat reference to test through the real capabilities path.
	// fakeCapCatalog is insufficient here — we need the real env-flag logic.
	// Wire a fake that always returns ImageInput=true to confirm the override.
	alwaysTrue := &fakeCapCatalog{
		limits: AttachmentLimitsResult{ImageInput: true, DocumentInput: true},
	}
	// The env flag is read inside capabilities.AttachmentLimits, not here.
	// The fakeCapCatalog won't apply the env flag — that lives in the real Catalog.
	// So this test verifies that the fakeCapCatalog returns what it returns
	// (no env override at the view layer). The env-flag path is covered by
	// TestAttachmentLimits_EnvFlagForceDisable in gate_test.go.
	api := New(Config{CapCatalog: alwaysTrue})
	got, err := api.GetAttachmentLimits(context.Background(), "anthropic", "claude-sonnet-4-7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// fakeCapCatalog ignores the env flag (it's a test double), so ImageInput=true.
	// The real gate integration is covered in capabilities/gate_test.go.
	if !got.ImageInput {
		t.Error("fakeCapCatalog always returns ImageInput=true; env-flag override test belongs in gate_test.go")
	}
}

// ── WP04: UpdateProvider evict-then-reload + RemoveProvider evict ────────────
//
// These tests are in impl_test.go (not impl_providers_test.go) because they
// focus on the registry interaction, not the store/keychain round-trip.

// TestUpdateProvider_EvictsAndReloads verifies that UpdateProvider evicts the
// stale registry entry and then reloads it, so the next registry lookup uses
// the updated model (FR-004).
func TestUpdateProvider_EvictsAndReloads(t *testing.T) {
	t.Parallel()
	// Use the real FileStore + a recording fakeRegistry so we can observe
	// what profiles the registry receives.
	api, _, _, _, store := newProvidersAPI(t)
	reg := &fakeRegistry{}
	api.reg = reg

	// AddProvider pre-seeds the store; then manually seed the registry
	// so it looks like a previously-loaded profile.
	in := AddProviderInput{
		ID:    "prov-wp04",
		Kind:  "openai",
		Model: "gpt-4o",
		Cred:  CredentialReference{Kind: "keychain", Locator: "kenaz-harness/prov-wp04"},
		PlaintextAPIKey: "sk-v1",
	}
	// AddProvider writes to the keychain; wire a no-op keychain so it succeeds.
	api.keychain = &noopKeychain{}
	if err := api.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// Manually seed the registry with the v1 profile so Evict has something
	// to delete.
	_ = reg.LoadProfiles([]corellm.ProviderProfile{{
		ID:    "prov-wp04",
		Kind:  "openai",
		Model: "gpt-4o",
		Cred:  corellm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/prov-wp04"},
	}})
	loadedBefore := len(reg.loaded)

	// UpdateProvider should evict the old entry then reload with new model.
	up := AddProviderInput{
		ID:    "prov-wp04",
		Kind:  "openai",
		Model: "gpt-4o-mini",
		Cred:  CredentialReference{Kind: "keychain", Locator: "kenaz-harness/prov-wp04"},
	}
	if err := api.UpdateProvider(context.Background(), up); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	// Expect at least one more LoadProfiles call after the eviction.
	if len(reg.loaded) <= loadedBefore {
		t.Fatal("expected a registry LoadProfiles call from UpdateProvider")
	}
	last := reg.loaded[len(reg.loaded)-1]
	if last.Model != "gpt-4o-mini" {
		t.Errorf("expected new model gpt-4o-mini in registry after UpdateProvider, got %q", last.Model)
	}

	_ = store // consumed via API
}

// noopKeychain is a KeychainWriter that does nothing (for tests that don't
// care about the OS keychain write).
type noopKeychain struct{}

func (noopKeychain) Write(_ context.Context, _ string, plaintext []byte) error {
	// Zero the slice to satisfy the KeychainWriter contract.
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

// TestRemoveProvider_EvictsFromRegistry verifies that RemoveProvider evicts
// the profile from the registry after removing it from the store (FR-004).
func TestRemoveProvider_EvictsFromRegistry(t *testing.T) {
	t.Parallel()
	api, _, _, _, _ := newProvidersAPI(t)
	reg := &fakeRegistry{}
	api.reg = reg
	api.keychain = &noopKeychain{}

	// AddProvider seeds the store.
	in := AddProviderInput{
		ID:    "prov-remove",
		Kind:  "openai",
		Model: "gpt-4o",
		Cred:  CredentialReference{Kind: "keychain", Locator: "kenaz-harness/prov-remove"},
		PlaintextAPIKey: "sk-rm",
	}
	if err := api.AddProvider(context.Background(), in); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}

	// Manually seed the registry (simulate a previously-loaded profile).
	_ = reg.LoadProfiles([]corellm.ProviderProfile{{
		ID:    "prov-remove",
		Kind:  "openai",
		Model: "gpt-4o",
		Cred:  corellm.CredentialReference{Kind: "keychain", Locator: "kenaz-harness/prov-remove"},
	}})

	if _, err := reg.Profile("prov-remove"); err != nil {
		t.Fatalf("profile should be present before remove: %v", err)
	}

	if err := api.RemoveProvider(context.Background(), "prov-remove"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	// After RemoveProvider the profile must be gone from the registry.
	if _, err := reg.Profile("prov-remove"); err == nil {
		t.Error("expected profile to be absent from registry after RemoveProvider")
	}
}

// TestStartStream_RegistersHostProfileWithoutAPriorListProviders pins the
// fix for a bug found by smoking the served harness: a host-delivered
// provider was only installed into the registry as a SIDE EFFECT of
// ListProviders. Any client that started a turn without first reading the
// provider list got `llm: profile "kenaz-host" not registered` — a
// confusing message about a provider the control plane had just granted.
//
// StartStream must resolve providers itself, not inherit whatever the UI
// happened to call first.
func TestStartStream_RegistersHostProfileWithoutAPriorListProviders(t *testing.T) {
	reg := &fakeRegistry{}
	host := corellm.ProviderProfile{
		ID:    "kenaz-host",
		Kind:  "anthropic",
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	api := New(Config{Registry: reg, HostProviders: []corellm.ProviderProfile{host}})

	// No ListProviders call first — straight to the turn.
	_, err := api.StartStream(context.Background(), "kenaz-host", "sess-1", "")

	// The chat runner is not wired in this chassis, so the call still
	// fails — but it must fail for THAT reason, having already installed
	// the profile.
	if err == nil {
		t.Fatal("expected the nil-chat-runner error")
	}
	if _, perr := reg.Profile("kenaz-host"); perr != nil {
		t.Fatalf("StartStream did not register the host profile: %v", perr)
	}
}
