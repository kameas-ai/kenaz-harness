// Concrete LLMConnectorAPI implementation. Backs both the streaming
// surface used by chat sessions and the personal-providers Add/Remove/
// Test flow exposed through the providers UI.
//
// Collaborators:
//
//   - Registry — connector registry. ListProviders surfaces its loaded
//     profiles; StartStream opens a stream against it.
//   - personal.Store — JSON-file-backed user-scoped provider store.
//     A nil Store causes AddProvider/RemoveProvider to return
//     ErrPersonalStoreUnavailable.
//   - BundleSource — read-only snapshot of bundle-derived profiles.
//   - KeychainWriter — writes plaintext API keys to the OS keychain
//     and returns the indirect reference persisted in providers.json.
//   - ProviderProber — runs TestProvider's lightweight verification.
//   - StreamSink — fan-out for "llm:stream-chunk" / "llm:stream-closed"
//     payloads. nil falls back to a no-op (test-friendly default).
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// StreamSink is the minimal contract the concrete LLMConnectorAPI uses
// to fan stream chunks out to the frontend. The rpc package's
// StreamBroker satisfies it; tests inject a recording fake.
//
// We define the contract here (in the views package) rather than
// taking a hard dependency on rpc to keep the import graph acyclic:
// rpc imports views/llm, not the other way around.
type StreamSink interface {
	// Emit delivers a payload on a topic. The concrete impl uses the
	// canonical "<view>:<event-kind>" naming (plan §4.2; broker docs).
	Emit(topic string, payload any)
}

type nopSink struct{}

func (nopSink) Emit(string, any) {}

// Registry is the slice of the connector Registry surface this view
// uses. We accept the interface (not the concrete *registry.Registry)
// so tests can inject a fake without dragging in the audit pipeline.
type Registry interface {
	corellm.Registry
}

// AdapterLookup is the optional registry capability the impl uses to
// drive the AddProvider model-picker. The concrete *registry.Registry
// satisfies it; tests substitute a fake.
type AdapterLookup interface {
	Adapter(kind string) corellm.ProviderAdapter
}

// ProviderProber performs the lightweight verification call used by
// TestProvider. Tests replace it with a deterministic fake.
type ProviderProber interface {
	Probe(ctx context.Context, profile corellm.ProviderProfile) ProberResult
}

// ProberResult is the prober's structured response.
type ProberResult struct {
	Success   bool
	LatencyMS int
	Message   string
}

// KeychainWriter writes a plaintext credential to the OS keychain under
// locator. Implementations MUST zeroize the supplied byte slice before
// returning.
type KeychainWriter interface {
	Write(ctx context.Context, locator string, plaintext []byte) error
}

// BundleSource exposes the registry's bundle-derived profiles. A nil
// BundleSource is treated as an empty snapshot.
type BundleSource interface {
	BundleProfiles() []corellm.ProviderProfile
}

// SessionMessage is the slice of session.Message the streaming layer
// reads to build the GenerationRequest. Defined here so the rpc views
// package does not import core/session directly (DIRECTIVE_001 +
// import-cycle hygiene).
type SessionMessage struct {
	Role    string
	Content string
}

// SessionMessageReader is the minimal session-history surface the
// streaming layer needs. The rpc layer wires this to the real
// session.Manager via an adapter; tests substitute fakes.
type SessionMessageReader interface {
	ListMessages(ctx context.Context, sessionID string) ([]SessionMessage, error)
}

// SessionMessageWriter persists the assistant turn at stream
// completion so navigating away and back reloads the full
// conversation. Without this, assistant deltas live only in the JS
// currentlyStreaming buffer and disappear on session switch.
type SessionMessageWriter interface {
	AppendMessage(ctx context.Context, sessionID, role, content string) error
}

// API is the concrete LLMConnectorAPI implementation.
type API struct {
	reg      Registry
	sink     StreamSink
	store    personal.Store
	bundles  BundleSource
	keychain  KeychainWriter
	prober    ProviderProber
	history   SessionMessageReader
	historyW  SessionMessageWriter

	mu             sync.Mutex
	subs           map[string]*subscription
	nextID         uint64
	validated      map[string]bool
	personalLoaded bool
}

type subscription struct {
	id        string
	sessionID string
	stream    corellm.Stream
	cancel    context.CancelFunc
	done      chan struct{}
}

// Config bundles construction options.
type Config struct {
	Registry Registry
	Sink     StreamSink
	Store    personal.Store
	Bundles  BundleSource
	Keychain KeychainWriter
	Prober   ProviderProber
	// History provides per-session message threading for StartStream.
	// nil disables history threading; the connector will be called with
	// a single fixed user message (test-friendly default).
	History SessionMessageReader
	// HistoryWriter persists the assistant turn at stream completion
	// so navigating away and back reloads the full conversation.
	HistoryWriter SessionMessageWriter
}

// New constructs a concrete API.
func New(cfg Config) *API {
	sink := cfg.Sink
	if sink == nil {
		sink = nopSink{}
	}
	return &API{
		reg:       cfg.Registry,
		sink:      sink,
		store:     cfg.Store,
		bundles:   cfg.Bundles,
		keychain:  cfg.Keychain,
		prober:    cfg.Prober,
		history:   cfg.History,
		historyW:  cfg.HistoryWriter,
		subs:      map[string]*subscription{},
		validated: map[string]bool{},
	}
}

// buildMessages assembles the GenerationRequest message slice. When a
// SessionMessageReader is configured and sessionID is non-empty, the
// session's persisted history is threaded through; otherwise we fall
// back to a single demo prompt so the chassis still streams in
// test/CI paths.
func (a *API) buildMessages(ctx context.Context, sessionID string) ([]corellm.Message, error) {
	if a.history == nil || sessionID == "" {
		return []corellm.Message{
			{
				Role: corellm.RoleUser,
				Content: []corellm.ContentPart{
					{Type: "text", Text: "Hello from the kaneaz-harness demo. Reply with a one-sentence greeting."},
				},
			},
		}, nil
	}
	stored, err := a.history.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("llm: load session history: %w", err)
	}
	if len(stored) == 0 {
		return nil, errors.New("llm: session has no messages — append a user turn before streaming")
	}
	out := make([]corellm.Message, 0, len(stored))
	for _, m := range stored {
		role := corellm.Role(m.Role)
		if role == "" {
			role = corellm.RoleUser
		}
		out = append(out, corellm.Message{
			Role: role,
			Content: []corellm.ContentPart{
				{Type: "text", Text: m.Content},
			},
		})
	}
	return out, nil
}

// ErrPersonalStoreUnavailable is returned by AddProvider / RemoveProvider
// when the impl was built without a backing store.
var ErrPersonalStoreUnavailable = errors.New("llm: personal provider store unavailable")

// ErrBundleProviderImmutable is returned by RemoveProvider when the
// caller targets a bundle-derived profile.
var ErrBundleProviderImmutable = errors.New("llm: bundle providers are read-only")

// Compile-time assertion: *API satisfies LLMConnectorAPI.
var _ LLMConnectorAPI = (*API)(nil)

// ListProviders returns the merged bundle + personal list. Bundle
// entries win on ID collision; personal-only entries surface the
// user's keychain-backed providers. Personal profiles are loaded into
// the registry on first call so subsequent StartStream calls resolve
// the same IDs.
func (a *API) ListProviders(_ context.Context) ([]Provider, error) {
	if err := a.ensurePersonalLoaded(); err != nil {
		return nil, err
	}
	seen := map[string]Provider{}
	if a.bundles != nil {
		for _, p := range a.bundles.BundleProfiles() {
			seen[p.ID] = profileToProvider(p, "bundle", a.isValidated(p.ID))
		}
	}
	if a.store != nil {
		personalList, err := a.store.List()
		if err != nil {
			return nil, fmt.Errorf("llm: list personal providers: %w", err)
		}
		for _, p := range personalList {
			if _, exists := seen[p.ID]; exists {
				continue
			}
			seen[p.ID] = profileToProvider(p, "personal", a.isValidated(p.ID))
		}
	}
	out := make([]Provider, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sortProviders(out)
	return out, nil
}

// StartStream opens a streaming generation against profileID for the
// supplied session and returns a subscription id. The actual streaming
// runs in a goroutine that pipes each chunk into the StreamSink. The
// emitted StreamChunkPayload carries SessionID so the chat UI can
// route per-token deltas to the correct conversation without
// smuggling state through the subscription id.
//
// modelOverride is a per-call selection from the profile's authorised
// models — the chat surface's model-switcher picks one and passes it
// here. Empty => use the profile default.
func (a *API) StartStream(ctx context.Context, profileID, sessionID, modelOverride string) (string, error) {
	log := logging.L()
	log.Info("llm.start_stream.requested",
		"profile_id", profileID,
		"session_id", sessionID,
		"model_override", modelOverride,
	)
	if a.reg == nil {
		log.Error("llm.start_stream.failed", "reason", "connector not wired")
		return "", errors.New("llm: connector not wired")
	}
	if profileID == "" {
		log.Error("llm.start_stream.failed", "reason", "empty profile id")
		return "", errors.New("llm: profile id required")
	}
	if err := a.ensurePersonalLoaded(); err != nil {
		log.Error("llm.start_stream.failed", "stage", "ensurePersonalLoaded", "err", err.Error())
		return "", err
	}

	messages, err := a.buildMessages(ctx, sessionID)
	if err != nil {
		log.Error("llm.start_stream.failed", "stage", "buildMessages", "err", err.Error())
		return "", err
	}
	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Model:     modelOverride,
		Messages:  messages,
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-streamCtx.Done():
		}
	}()

	stream, err := a.reg.Stream(streamCtx, req)
	if err != nil {
		cancel()
		log.Error("llm.start_stream.failed",
			"stage", "registry.Stream",
			"profile_id", profileID,
			"err", err.Error(),
		)
		return "", err
	}

	a.mu.Lock()
	a.nextID++
	id := fmt.Sprintf("llm-%d", a.nextID)
	sub := &subscription{
		id:        id,
		sessionID: sessionID,
		stream:    stream,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	a.subs[id] = sub
	a.mu.Unlock()

	log.Info("llm.start_stream.opened",
		"sub_id", id,
		"profile_id", profileID,
		"session_id", sessionID,
		"messages", len(messages),
	)
	go a.pump(sub)
	return id, nil
}

// StopStream terminates the subscription. The pump goroutine drains
// remaining chunks, emits llm:stream-closed, and exits.
func (a *API) StopStream(_ context.Context, subID string) error {
	a.mu.Lock()
	sub, ok := a.subs[subID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("llm: subscription %q not found", subID)
	}
	if err := sub.stream.Cancel(); err != nil {
		return err
	}
	sub.cancel()
	<-sub.done
	return nil
}

func (a *API) pump(sub *subscription) {
	log := logging.L()
	defer func() {
		a.mu.Lock()
		delete(a.subs, sub.id)
		a.mu.Unlock()
		close(sub.done)
	}()

	chunkCount := 0
	var assistantText []byte
	for ev := range sub.stream.Events() {
		chunkCount++
		if ev.Kind == corellm.StreamText {
			assistantText = append(assistantText, ev.Text...)
		}
		a.sink.Emit("llm:stream-chunk", StreamChunkPayload{
			SubID:     sub.id,
			SessionID: sub.sessionID,
			Chunk:     ev,
		})
	}
	resp, err := sub.stream.Final()
	closed := StreamClosedPayload{SubID: sub.id, SessionID: sub.sessionID}
	switch {
	case err != nil:
		var ce *corellm.ErrCancelled
		if errors.As(err, &ce) {
			closed.Reason = "stop-called"
		} else {
			closed.Reason = "backend-error"
			closed.Message = err.Error()
		}
	default:
		closed.Reason = "completed"
		closed.FinishReason = resp.FinishReason
	}

	// Persist the assistant turn so navigating away and back reloads
	// the full conversation. We persist on completed AND on
	// stop-called (partial turn is still useful context); skip on
	// backend-error since the response is unreliable.
	if a.historyW != nil &&
		sub.sessionID != "" &&
		len(assistantText) > 0 &&
		closed.Reason != "backend-error" {
		if err := a.historyW.AppendMessage(
			context.Background(),
			sub.sessionID,
			"assistant",
			string(assistantText),
		); err != nil {
			log.Warn("llm.stream.persist_assistant_failed",
				"sub_id", sub.id,
				"session_id", sub.sessionID,
				"err", err.Error(),
			)
		}
	}

	log.Info("llm.stream.closed",
		"sub_id", sub.id,
		"session_id", sub.sessionID,
		"reason", closed.Reason,
		"finish_reason", closed.FinishReason,
		"chunks", chunkCount,
		"text_bytes", len(assistantText),
		"err_message", closed.Message,
	)
	a.sink.Emit("llm:stream-closed", closed)
}

// AddProvider validates the input, writes the plaintext API key (if any)
// to the keychain under the supplied locator, then persists the
// CredentialReference to the personal store. The new profile is also
// loaded into the registry so StartStream can resolve it without
// requiring a process restart.
func (a *API) AddProvider(ctx context.Context, in AddProviderInput) error {
	log := logging.L()
	log.Info("llm.add_provider.requested",
		"id", in.ID, "kind", in.Kind, "model", in.Model,
		"models", in.Models, "region", in.Region,
		"cred_kind", in.Cred.Kind, "cred_locator", in.Cred.Locator,
		"has_plaintext_key", in.PlaintextAPIKey != "",
	)
	if a.store == nil {
		log.Error("llm.add_provider.failed", "id", in.ID, "reason", "no store")
		return ErrPersonalStoreUnavailable
	}
	switch in.Cred.Kind {
	case "keychain", "aws_profile", "env", "file":
		// indirect references — accepted
	default:
		return fmt.Errorf("llm: AddProvider requires an indirect credential kind (keychain|aws_profile|env|file), got %q", in.Cred.Kind)
	}
	// Only keychain creds need a plaintext write — aws_profile points at
	// ~/.aws/credentials which the AWS SDK reads directly, env / file
	// resolve via their respective backends with no upfront staging.
	if in.PlaintextAPIKey != "" {
		if in.Cred.Kind != "keychain" {
			return fmt.Errorf("llm: PlaintextAPIKey is only used with kind=keychain, got %q", in.Cred.Kind)
		}
		if a.keychain == nil {
			return errors.New("llm: no keychain writer configured; cannot store plaintext key")
		}
		buf := []byte(in.PlaintextAPIKey)
		in.PlaintextAPIKey = ""
		err := a.keychain.Write(ctx, in.Cred.Locator, buf)
		zeroBytes(buf)
		if err != nil {
			return fmt.Errorf("llm: keychain write %q: %w", in.Cred.Locator, err)
		}
	}
	profile := corellm.ProviderProfile{
		ID:     in.ID,
		Kind:   in.Kind,
		Model:  in.Model,
		Models: in.Models,
		Region: in.Region,
		Cred: corellm.CredentialReference{
			Kind:    in.Cred.Kind,
			Locator: in.Cred.Locator,
		},
	}
	if err := a.store.Add(profile); err != nil {
		log.Error("llm.add_provider.failed", "id", in.ID, "stage", "store.Add", "err", err.Error())
		return err
	}
	if a.reg != nil {
		// Best-effort registry sync. Failure here means StartStream will
		// not see the new profile until the next ensurePersonalLoaded
		// pass; the store write is durable so the row still renders.
		_ = a.reg.LoadProfiles([]corellm.ProviderProfile{profile})
	}
	a.mu.Lock()
	delete(a.validated, in.ID)
	a.mu.Unlock()
	log.Info("llm.add_provider.persisted", "id", in.ID, "kind", in.Kind, "models", profile.AvailableModels())
	return nil
}

// UpdateProvider replaces an existing personal provider profile.
// PlaintextAPIKey is optional — when empty, the keychain entry is left
// untouched so the user can edit model/region without re-entering
// the credential. When supplied, it is written to the keychain under
// the (existing) locator and zeroed before any further processing.
func (a *API) UpdateProvider(ctx context.Context, in AddProviderInput) error {
	if a.store == nil {
		return ErrPersonalStoreUnavailable
	}
	switch in.Cred.Kind {
	case "keychain", "aws_profile", "env", "file":
	default:
		return fmt.Errorf("llm: UpdateProvider requires an indirect credential kind (keychain|aws_profile|env|file), got %q", in.Cred.Kind)
	}
	if a.bundles != nil {
		for _, p := range a.bundles.BundleProfiles() {
			if p.ID == in.ID {
				return fmt.Errorf("%w: %q", ErrBundleProviderImmutable, in.ID)
			}
		}
	}
	if in.PlaintextAPIKey != "" {
		if in.Cred.Kind != "keychain" {
			return fmt.Errorf("llm: PlaintextAPIKey is only used with kind=keychain, got %q", in.Cred.Kind)
		}
		if a.keychain == nil {
			return errors.New("llm: no keychain writer configured; cannot store plaintext key")
		}
		buf := []byte(in.PlaintextAPIKey)
		in.PlaintextAPIKey = ""
		err := a.keychain.Write(ctx, in.Cred.Locator, buf)
		zeroBytes(buf)
		if err != nil {
			return fmt.Errorf("llm: keychain write %q: %w", in.Cred.Locator, err)
		}
	}
	profile := corellm.ProviderProfile{
		ID:     in.ID,
		Kind:   in.Kind,
		Model:  in.Model,
		Models: in.Models,
		Region: in.Region,
		Cred: corellm.CredentialReference{
			Kind:    in.Cred.Kind,
			Locator: in.Cred.Locator,
		},
	}
	if err := a.store.Update(profile); err != nil {
		return err
	}
	if a.reg != nil {
		// Replace any in-memory copy. The simplest path is to push the
		// new profile via LoadProfiles; since the registry rejects
		// duplicate IDs, we have to remove first via a future
		// RegistryEvict seam. For now, the AddProvider flow already
		// has best-effort LoadProfiles semantics so we mirror it.
		_ = a.reg.LoadProfiles([]corellm.ProviderProfile{profile})
	}
	a.mu.Lock()
	delete(a.validated, in.ID)
	a.mu.Unlock()
	return nil
}

// RemoveProvider deletes a personal provider by ID. Bundle-derived
// profiles are rejected so the UI cannot mutate them through this seam.
func (a *API) RemoveProvider(_ context.Context, id string) error {
	if a.store == nil {
		return ErrPersonalStoreUnavailable
	}
	if a.bundles != nil {
		for _, p := range a.bundles.BundleProfiles() {
			if p.ID == id {
				return fmt.Errorf("%w: %q", ErrBundleProviderImmutable, id)
			}
		}
	}
	if err := a.store.Remove(id); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.validated, id)
	a.mu.Unlock()
	return nil
}

// ListModels asks the adapter for the supplied kind to enumerate the
// models the supplied API key is authorized to call. Returns an empty
// slice (not an error) when the kind has no adapter registered or
// when the adapter does not implement ModelLister — the UI then
// falls back to manual model entry. The plaintext key is zeroed
// before this method returns.
func (a *API) ListModels(ctx context.Context, kind, plaintextApiKey string) ([]ModelInfo, error) {
	if kind == "" {
		return nil, errors.New("llm: kind required")
	}
	lookup, ok := a.reg.(AdapterLookup)
	if !ok || lookup == nil {
		return []ModelInfo{}, nil
	}
	adapter := lookup.Adapter(kind)
	if adapter == nil {
		return []ModelInfo{}, nil
	}
	lister, ok := adapter.(corellm.ModelLister)
	if !ok {
		return []ModelInfo{}, nil
	}
	buf := []byte(plaintextApiKey)
	defer zeroBytes(buf)
	models, err := lister.ListModels(ctx, buf)
	if err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, ModelInfo{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			Description: m.Description,
		})
	}
	return out, nil
}

// TestProvider runs the configured prober against the named profile and
// returns a TestResult.
func (a *API) TestProvider(ctx context.Context, id string) (TestResult, error) {
	profile, err := a.lookupProfile(id)
	if err != nil {
		return TestResult{}, err
	}
	if a.prober == nil {
		return TestResult{
			Success: false,
			Message: "no provider prober configured",
		}, nil
	}
	t0 := time.Now()
	res := a.prober.Probe(ctx, profile)
	if res.LatencyMS == 0 {
		res.LatencyMS = int(time.Since(t0).Milliseconds())
	}
	a.mu.Lock()
	a.validated[id] = res.Success
	a.mu.Unlock()
	return TestResult{
		Success:   res.Success,
		LatencyMS: res.LatencyMS,
		Message:   res.Message,
	}, nil
}

// ensurePersonalLoaded loads personal-store profiles into the registry
// once per process. Idempotent.
func (a *API) ensurePersonalLoaded() error {
	a.mu.Lock()
	if a.personalLoaded {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	if a.store == nil || a.reg == nil {
		a.mu.Lock()
		a.personalLoaded = true
		a.mu.Unlock()
		return nil
	}
	list, err := a.store.List()
	if err != nil {
		return fmt.Errorf("llm: list personal providers: %w", err)
	}
	if len(list) > 0 {
		if err := a.reg.LoadProfiles(list); err != nil {
			return fmt.Errorf("llm: load personal profiles: %w", err)
		}
	}
	a.mu.Lock()
	a.personalLoaded = true
	a.mu.Unlock()
	return nil
}

func (a *API) lookupProfile(id string) (corellm.ProviderProfile, error) {
	if a.bundles != nil {
		for _, p := range a.bundles.BundleProfiles() {
			if p.ID == id {
				return p, nil
			}
		}
	}
	if a.store != nil {
		p, err := a.store.Get(id)
		if err == nil {
			return p, nil
		}
	}
	return corellm.ProviderProfile{}, fmt.Errorf("llm: provider %q not found", id)
}

func (a *API) isValidated(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.validated[id]
}

// StreamChunkPayload is the typed payload shipped on llm:stream-chunk.
//
// SessionID lets the chat UI route deltas to the conversation that
// requested the completion without the frontend having to maintain a
// SubID→SessionID mapping.
//
// Privacy: Chunk is a connector StreamEvent. The connector never puts
// credential bytes on a StreamEvent (per the audit-emitter contract);
// the redaction pipeline upstream gives a second line of defense.
type StreamChunkPayload struct {
	SubID     string              `json:"sub_id"`
	SessionID string              `json:"session_id,omitempty"`
	Chunk     corellm.StreamEvent `json:"chunk"`
}

// StreamClosedPayload is the typed payload shipped on llm:stream-closed.
type StreamClosedPayload struct {
	SubID        string `json:"sub_id"`
	SessionID    string `json:"session_id,omitempty"`
	Reason       string `json:"reason"`
	Message      string `json:"message,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

func profileToProvider(p corellm.ProviderProfile, source string, validated bool) Provider {
	return Provider{
		ID:     p.ID,
		Name:   p.ID,
		Tier:   source,
		Kind:   p.Kind,
		Model:  p.Model,
		Models: p.AvailableModels(),
		Region: p.Region,
		Cred: CredentialReference{
			Kind:    p.Cred.Kind,
			Locator: p.Cred.Locator,
		},
		Source:    source,
		Validated: validated,
	}
}

func sortProviders(ps []Provider) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && lessProvider(ps[j], ps[j-1]); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

func lessProvider(a, b Provider) bool {
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.ID < b.ID
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
