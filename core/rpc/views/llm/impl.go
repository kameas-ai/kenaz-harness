package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
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

// nopSink is the safe default when the API is constructed without a
// broker — the connector still returns subscription ids and pumps
// chunks, but no Wails event is emitted.
type nopSink struct{}

func (nopSink) Emit(string, any) {}

// Registry is the slice of the connector Registry surface this view
// uses. We accept the interface (not the concrete *registry.Registry)
// so tests can inject a fake without dragging in the audit pipeline.
type Registry interface {
	corellm.Registry
}

// API is the concrete LLMConnectorAPI implementation.
//
// Lifecycle:
//
//	api := llmview.New(llmview.Config{
//	    Registry: reg,
//	    Sink:     broker,           // *rpc.StreamBroker.WithEmit() shim
//	    DataDir:  dataDir,          // for providers.json fallback
//	})
//
// StartStream(profileID) opens a registry stream, allocates a
// subscription id, and runs a goroutine that pumps each StreamEvent
// into the sink as a payload of shape:
//
//	{ "sub_id": "<id>", "chunk": <StreamEvent> }
//
// The frontend's useEventStream subscribes to "llm:stream-chunk" and
// filters by sub_id. StopStream(subID) cancels the underlying call
// and the pump emits "llm:stream-closed" with reason "stop-called".
type API struct {
	reg     Registry
	sink    StreamSink
	dataDir string

	mu              sync.Mutex
	subs            map[string]*subscription
	nextID          uint64
	providersLoaded bool
	profileDisplay  map[string]displayMeta
}

type subscription struct {
	id     string
	stream corellm.Stream
	cancel context.CancelFunc
	done   chan struct{}
}

// displayMeta caches the human-friendly Name/Tier we read from
// providers.json so ListProviders does not re-parse the file on every
// call.
type displayMeta struct {
	name string
	tier string
}

// Config bundles construction options.
type Config struct {
	// Registry is required; ListProviders / StartStream call into it.
	Registry Registry
	// Sink receives "llm:stream-chunk" / "llm:stream-closed" emissions.
	// Nil falls through to a no-op (test-friendly default).
	Sink StreamSink
	// DataDir is the base for the personal providers.json file. When
	// blank we fall back to $XDG_CONFIG_HOME / Library/Application
	// Support / %APPDATA% (os.UserConfigDir).
	DataDir string
}

// New constructs a concrete API. A nil Registry yields an API that
// returns errNotWired-style errors from every method, matching the
// chassis stub behaviour so a partially-wired harness still boots.
func New(cfg Config) *API {
	sink := cfg.Sink
	if sink == nil {
		sink = nopSink{}
	}
	return &API{
		reg:     cfg.Registry,
		sink:    sink,
		dataDir: cfg.DataDir,
		subs:    map[string]*subscription{},
	}
}

// Compile-time assertion: *API satisfies LLMConnectorAPI.
var _ LLMConnectorAPI = (*API)(nil)

// providerProfileFile is the personal-providers escape-hatch shape.
// One JSON file under $USER_CONFIG_DIR/kaneaz-harness/providers.json
// lets a developer wire a real anthropic profile without authoring a
// full bundle (US plan §6 v1 demo). The file shape mirrors
// llm.ProviderProfile so the Registry can ingest it directly.
type providerProfileFile struct {
	Profiles []providerProfileEntry `json:"profiles"`
}

type providerProfileEntry struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Tier     string                 `json:"tier"`
	Kind     string                 `json:"kind"`
	Model    string                 `json:"model"`
	Auth     credentialEntry        `json:"auth"`
	Region   string                 `json:"region,omitempty"`
	Endpoint string                 `json:"endpoint,omitempty"`
	Defaults map[string]interface{} `json:"defaults,omitempty"`
}

type credentialEntry struct {
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}

// ListProviders returns the loaded profiles' user-facing summaries.
//
// The first call after process start lazily loads providers.json into
// the Registry. Subsequent calls return the cached snapshot. Profiles
// already loaded into the Registry by other code paths (tests, future
// bundle wiring) are also surfaced here.
func (a *API) ListProviders(_ context.Context) ([]Provider, error) {
	if a.reg == nil {
		return nil, errors.New("llm: connector not wired")
	}
	if err := a.ensureProvidersLoaded(); err != nil {
		return nil, err
	}
	// The Registry interface (core/llm.Registry) does not expose a
	// list method, but the concrete *registry.Registry does. We
	// retrieve via the optional method below; tests pass a fake that
	// implements it directly.
	type lister interface {
		Profiles() []corellm.ProviderProfile
	}
	var profs []corellm.ProviderProfile
	if l, ok := a.reg.(lister); ok {
		profs = l.Profiles()
	}
	out := make([]Provider, 0, len(profs))
	for _, p := range profs {
		name := p.ID
		// If we loaded from providers.json, attach the Name/Tier we
		// stashed there. Look up by ID in our cache.
		a.mu.Lock()
		if disp, ok := a.profileDisplay[p.ID]; ok {
			if disp.name != "" {
				name = disp.name
			}
		}
		a.mu.Unlock()
		out = append(out, Provider{
			ID:    p.ID,
			Name:  name,
			Tier:  a.tierFor(p.ID),
			Model: p.Model,
		})
	}
	return out, nil
}

// StartStream opens a streaming generation against profileID and
// returns a subscription id. The actual streaming runs in a goroutine
// that pipes each chunk into the StreamSink.
func (a *API) StartStream(ctx context.Context, profileID string) (string, error) {
	if a.reg == nil {
		return "", errors.New("llm: connector not wired")
	}
	if err := a.ensureProvidersLoaded(); err != nil {
		return "", err
	}
	if profileID == "" {
		return "", errors.New("llm: profile id required")
	}

	// Build a default GenerationRequest. The v1 demo uses a single
	// fixed user message; chat UI work will replace this with a real
	// message-history threading from the session state.
	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Messages: []corellm.Message{
			{
				Role: corellm.RoleUser,
				Content: []corellm.ContentPart{
					{Type: "text", Text: "Hello from the kaneaz-harness demo. Reply with a one-sentence greeting."},
				},
			},
		},
	}

	// Per-call ctx so StopStream / ctx-cancel both terminate the run.
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
		return "", err
	}

	a.mu.Lock()
	a.nextID++
	id := fmt.Sprintf("llm-%d", a.nextID)
	sub := &subscription{
		id:     id,
		stream: stream,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	a.subs[id] = sub
	a.mu.Unlock()

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

// pump reads from the underlying corellm.Stream, emits one
// "llm:stream-chunk" payload per StreamEvent, and emits a final
// "llm:stream-closed" payload when the stream terminates.
func (a *API) pump(sub *subscription) {
	defer func() {
		a.mu.Lock()
		delete(a.subs, sub.id)
		a.mu.Unlock()
		close(sub.done)
	}()

	for ev := range sub.stream.Events() {
		a.sink.Emit("llm:stream-chunk", StreamChunkPayload{
			SubID: sub.id,
			Chunk: ev,
		})
	}
	resp, err := sub.stream.Final()
	closed := StreamClosedPayload{SubID: sub.id}
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
	a.sink.Emit("llm:stream-closed", closed)
}

// StreamChunkPayload is the typed payload shipped on llm:stream-chunk.
//
// Privacy: Chunk is a connector StreamEvent. The connector never puts
// credential bytes on a StreamEvent (per the audit-emitter contract);
// the redaction pipeline upstream gives a second line of defense.
type StreamChunkPayload struct {
	SubID string                 `json:"sub_id"`
	Chunk corellm.StreamEvent    `json:"chunk"`
}

// StreamClosedPayload is the typed payload shipped on llm:stream-closed.
type StreamClosedPayload struct {
	SubID        string `json:"sub_id"`
	Reason       string `json:"reason"`           // completed | stop-called | backend-error
	Message      string `json:"message,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// tierFor returns the human-friendly tier label cached for the given
// profile id. Defaults to "Cloud" when no providers.json entry was
// loaded for the profile.
func (a *API) tierFor(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d, ok := a.profileDisplay[id]; ok && d.tier != "" {
		return d.tier
	}
	return "Cloud"
}

// ensureProvidersLoaded loads $USER_CONFIG_DIR/kaneaz-harness/providers.json
// (or $DataDir/providers.json when DataDir is set) and registers each
// entry with the Registry. Idempotent — second call is a no-op.
func (a *API) ensureProvidersLoaded() error {
	a.mu.Lock()
	if a.providersLoaded {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()

	path, err := a.providersPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// Missing file is not an error — the Registry may have been
		// pre-populated by another code path. We mark "loaded" so we
		// don't keep retrying every call.
		a.mu.Lock()
		a.providersLoaded = true
		a.mu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("llm: read providers file: %w", err)
	}
	var doc providerProfileFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("llm: parse providers file: %w", err)
	}
	profs := make([]corellm.ProviderProfile, 0, len(doc.Profiles))
	display := make(map[string]displayMeta, len(doc.Profiles))
	for _, e := range doc.Profiles {
		profs = append(profs, corellm.ProviderProfile{
			ID:       e.ID,
			Kind:     e.Kind,
			Model:    e.Model,
			Cred:     corellm.CredentialReference{Kind: e.Auth.Kind, Locator: e.Auth.Locator},
			Region:   e.Region,
			Endpoint: e.Endpoint,
			Defaults: e.Defaults,
		})
		display[e.ID] = displayMeta{name: e.Name, tier: e.Tier}
	}
	if len(profs) > 0 {
		if err := a.reg.LoadProfiles(profs); err != nil {
			return fmt.Errorf("llm: load profiles: %w", err)
		}
	}
	a.mu.Lock()
	a.providersLoaded = true
	a.profileDisplay = display
	a.mu.Unlock()
	return nil
}

// providersPath resolves the providers.json location. DataDir wins
// when set (test harnesses, embedding apps); otherwise we use
// os.UserConfigDir.
func (a *API) providersPath() (string, error) {
	if a.dataDir != "" {
		return filepath.Join(a.dataDir, "providers.json"), nil
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("llm: user config dir: %w", err)
	}
	return filepath.Join(cfg, "kaneaz-harness", "providers.json"), nil
}
