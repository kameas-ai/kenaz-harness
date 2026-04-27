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
	"strings"
	"sync"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
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
//
// ContentBlocks (multimodal-io WP03) carries the canonical post-WP01
// polymorphic content shape when the row was persisted with non-text
// blocks (image / document attachments). When non-nil, buildMessages
// uses ContentBlocks verbatim and ignores Content; legacy text-only
// rows leave ContentBlocks nil so the historical Content path keeps
// working.
type SessionMessage struct {
	Role          string
	Content       string
	ContentBlocks []corellm.ContentBlock
}

// SessionMessageReader is the minimal session-history surface the
// streaming layer needs. The rpc layer wires this to the real
// session.Manager via an adapter; tests substitute fakes.
type SessionMessageReader interface {
	ListMessages(ctx context.Context, sessionID string) ([]SessionMessage, error)
}

// SessionContextReader exposes the session's optional starting context
// (Mission A). buildMessages prepends a system role message when the
// session was configured with kind=system. user_seed sessions surface
// their seed as a normal first user turn — already in the message
// history — so this reader returns an empty content for them.
//
// Deprecated: replaced by AttachmentsResolver.ListResolved in WP03;
// remove next mission post-WP04. The interface is retained for the
// one-release compat buffer so legacy callers keep working.
type SessionContextReader interface {
	SystemPromptFor(ctx context.Context, sessionID string) (content, kind string, err error)
}

// ResolvedAttachment is the slice of attachments.Attachment buildMessages
// consumes. Defined here so the views/llm package does not import
// core/attachments directly.
type ResolvedAttachment struct {
	Content string
	Kind    string
}

// AttachmentsResolver returns the resolved (global+project+session)
// attachment list for a session in declared injection order. nil means
// "attachments not wired" — buildMessages falls back to the legacy
// SessionContextReader probe so Mission A's wire shape keeps working
// during the one-release transition.
type AttachmentsResolver interface {
	ListResolved(ctx context.Context, sessionID string) ([]ResolvedAttachment, error)
}

// SessionMessageWriter persists the assistant turn at stream
// completion so navigating away and back reloads the full
// conversation. Without this, assistant deltas live only in the JS
// currentlyStreaming buffer and disappear on session switch.
//
// The messageID return is used by post-finalize hooks (e.g. the
// artifacts code-block detector) to anchor SourceRef.MessageID to the
// freshly persisted row. An empty id is acceptable when the writer
// has no stable id to surface; downstream hooks treat the field as
// metadata only.
type SessionMessageWriter interface {
	AppendMessage(ctx context.Context, sessionID, role, content string) (messageID string, err error)
}

// ArtifactSink runs the artifacts code-block detector against a
// freshly persisted assistant message. nil-safe — the LLM impl
// short-circuits when no sink is wired so chat-only test paths and
// builds without the artifacts package keep working.
type ArtifactSink interface {
	OnAssistantMessage(ctx context.Context, sessionID, messageID, text string) error
}

// HookMessage mirrors the wire-shape used by the hooks subsystem so
// the views package does not import core/hooks (keeps the boundary
// clean). The rpc layer adapts between core/hooks.HookMessage and
// this minimal projection.
type HookMessage struct {
	Role    string
	Content string
}

// PreSendHookEvent is the slice of hooks.PreSendEvent the LLM impl
// passes to the runner. Defined here so views/llm does not import
// core/hooks directly.
type PreSendHookEvent struct {
	SessionID string
	Messages  []HookMessage
	Model     string
	Kind      string
	UserText  string
}

// PostSendHookEvent mirrors hooks.PostSendEvent.
type PostSendHookEvent struct {
	SessionID     string
	UserTurn      string
	AssistantTurn string
	Model         string
	Kind          string
	FinishReason  string
}

// HookRunner is the surface buildMessages and pump call to fire
// pre/post-send hooks. The rpc layer wires a *hooks.Runner-backed
// adapter; nil means "no hooks configured" (the chat path stays
// untouched, mirroring the pre-hooks behaviour).
type HookRunner interface {
	RunPreSend(ctx context.Context, ev PreSendHookEvent) (PreSendHookEvent, error)
	RunPostSend(ctx context.Context, ev PostSendHookEvent)
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
	hooks     HookRunner
	artifacts ArtifactSink
	// attachments is the WP03 source of truth for resolved starting
	// context. nil falls back to the SessionContextReader probe so
	// Mission A behaviour stays intact during the one-release buffer.
	attachments AttachmentsResolver
	// toolLoop closes the LLM ↔ tool ↔ LLM cycle when the adapter
	// signals FinishReason == "tool_use". nil disables the loop —
	// the chat path stays as it was before WP01.
	toolLoop *toolloop.Loop
	// tools projects the MCP pool's catalog onto each GenerationRequest
	// so the model knows what it may invoke. nil silences discovery
	// and keeps WP00 chat-only behaviour for tests that don't wire an
	// MCP pool.
	tools corellm.ToolDiscoverer
	// confirmGateway brokers WP05 confirm-each modal flow between the
	// toolloop (which blocks on RequestConfirm) and the frontend
	// (which calls ResolveConfirm). nil means "no confirm-each
	// surface wired" — ResolveConfirm returns a not-wired error and
	// the toolloop falls back to its noop gateway.
	confirmGateway *ConfirmGateway

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
	// userTurn / model / kind are captured at StartStream so the post-send
	// hook fired in pump has the original turn metadata without a second
	// session-history read.
	userTurn string
	model    string
	kind     string
	// req is the GenerationRequest used to open the initial stream.
	// pump passes it to the toolloop so the loop can build augmented
	// requests when re-invoking the registry after tool dispatch.
	req corellm.GenerationRequest
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
	// Hooks, when non-nil, fires pre_send before the message list is
	// shipped upstream and post_send after the assistant stream closes.
	// The runner threads any mutated message slice from pre_send hooks
	// back into the GenerationRequest. nil disables the hooks subsystem
	// entirely (test-friendly default; chat surface still works).
	//
	// Memory retrieval used to ship as an inline MemoryRetriever field;
	// it is now a preinstalled pre_send hook (memory.retrieve) that the
	// rpc layer wires into the runner's BuiltinRegistry.
	Hooks HookRunner
	// Attachments, when non-nil, supplies the resolved attachment list
	// for the session being streamed. buildMessages prepends each
	// attachment as a system message in declared order
	// ([global..., project..., session...]). nil keeps the legacy
	// SessionContextReader probe path so Mission A continues to work
	// for the one-release compat window.
	Attachments AttachmentsResolver
	// ToolLoop, when non-nil, runs after the initial stream closes
	// with FinishReason == "tool_use". The loop dispatches each tool
	// call against the configured MCP pool and re-invokes the
	// registry until the model returns a non-tool_use finish. nil
	// preserves WP00 chat-only behavior.
	ToolLoop *toolloop.Loop
	// Tools, when non-nil, is consulted on every StartStream to
	// populate GenerationRequest.Tools. Without it the model is never
	// told about the MCP catalog and the toolloop is dead code from
	// the user's perspective.
	Tools corellm.ToolDiscoverer
	// ConfirmGateway brokers the WP05 confirm-each modal flow. When
	// non-nil ResolveConfirm forwards to its ResolveConfirm method;
	// when nil ResolveConfirm returns a not-wired error.
	ConfirmGateway *ConfirmGateway
	// Artifacts, when non-nil, fires the code-block detector against
	// the freshly persisted assistant message at stream completion
	// (non-tool_use finish only). nil leaves the chat path untouched.
	Artifacts ArtifactSink
}

// New constructs a concrete API.
func New(cfg Config) *API {
	sink := cfg.Sink
	if sink == nil {
		sink = nopSink{}
	}
	return &API{
		reg:            cfg.Registry,
		sink:           sink,
		store:          cfg.Store,
		bundles:        cfg.Bundles,
		keychain:       cfg.Keychain,
		prober:         cfg.Prober,
		history:        cfg.History,
		historyW:       cfg.HistoryWriter,
		hooks:          cfg.Hooks,
		attachments:    cfg.Attachments,
		toolLoop:       cfg.ToolLoop,
		tools:          cfg.Tools,
		confirmGateway: cfg.ConfirmGateway,
		artifacts:      cfg.Artifacts,
		subs:           map[string]*subscription{},
		validated:      map[string]bool{},
	}
}

// buildMessages assembles the GenerationRequest message slice.
//
// Pipeline:
//   (1) Load the session's persisted history.
//   (2) Mission A — if the session was configured with a starting
//       context (kind=system, non-empty content), prepend it as a
//       system message. user_seed kind sessions don't need this
//       branch because the seed already lives as the first user
//       turn in `stored`.
//   (3) Mission B — fire the pre_send hook chain. Hooks may mutate
//       the slice to prepend further system messages (memory.retrieve
//       injections, redaction transforms, etc.) or block the send.
//   (4) Map the resulting hookMessages to corellm.Message and return.
//
// Final on-the-wire order: [Mission-A system prompt?,
// hook-injected system messages?, conversation turns…].
//
// model + kind are forwarded to the hook runner so Match filters can
// scope hooks to a particular provider or model.
func (a *API) buildMessages(ctx context.Context, sessionID, model, kind string) ([]corellm.Message, error) {
	if a.history == nil || sessionID == "" {
		return []corellm.Message{
			corellm.NewTextMessage(corellm.RoleUser,
				"Hello from the kaneaz-harness demo. Reply with a one-sentence greeting."),
		}, nil
	}
	stored, err := a.history.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("llm: load session history: %w", err)
	}
	if len(stored) == 0 {
		return nil, errors.New("llm: session has no messages — append a user turn before streaming")
	}
	// WP03 — collect the resolved attachment slice from the
	// AttachmentsResolver when wired. Each "system"-kind attachment is
	// prepended as a system message in declared order
	// ([global..., project..., session...]); "user"-kind attachments
	// already live as a real first user turn in `stored`, so we skip
	// them here.
	//
	// When Attachments is nil we fall through to the legacy
	// SessionContextReader probe so Mission A's behaviour is preserved
	// for the one-release compat buffer.
	var systemAttachments []ResolvedAttachment
	if a.attachments != nil {
		resolved, err := a.attachments.ListResolved(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("llm: load resolved attachments: %w", err)
		}
		systemAttachments = resolved
	} else if ctxReader, ok := a.history.(SessionContextReader); ok {
		content, ctxKind, err := ctxReader.SystemPromptFor(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("llm: load session system prompt: %w", err)
		}
		if ctxKind == "system" && content != "" {
			systemAttachments = []ResolvedAttachment{{Content: content, Kind: "system"}}
		}
	}

	// Convert stored history to the HookMessage projection the runner
	// consumes. Resolved attachments sit at the front so the hook chain
	// sees them as part of the input (memory.retrieve appends its own
	// snippets after); the on-the-wire order is
	// [global..., project..., session..., hook_injections?, conversation…].
	//
	// userText is the most recent user turn — memory.retrieve uses it
	// as the embedding query.
	hookMessages := make([]HookMessage, 0, len(stored)+len(systemAttachments)+1)
	// blockSidecar tracks which hookMessage index originated from a
	// stored message carrying ContentBlocks so the post-hook projection
	// can splice the canonical polymorphic shape back in (multimodal-io
	// WP03). Indexes outside the map fall through to the plain
	// NewTextMessage path. Hooks run on the flattened text projection
	// only — content blocks are out of scope for the hook surface.
	blockSidecar := map[int][]corellm.ContentBlock{}
	for _, att := range systemAttachments {
		if att.Kind != "system" || att.Content == "" {
			continue
		}
		hookMessages = append(hookMessages, HookMessage{
			Role:    "system",
			Content: att.Content,
		})
	}
	var userText string
	for _, m := range stored {
		idx := len(hookMessages)
		flat := m.Content
		if len(m.ContentBlocks) > 0 && flat == "" {
			flat = flattenBlocks(m.ContentBlocks)
		}
		hookMessages = append(hookMessages, HookMessage{Role: m.Role, Content: flat})
		if len(m.ContentBlocks) > 0 {
			blockSidecar[idx] = m.ContentBlocks
		}
		if m.Role == "user" {
			userText = flat
		}
	}
	if a.hooks != nil {
		out, herr := a.hooks.RunPreSend(ctx, PreSendHookEvent{
			SessionID: sessionID,
			Messages:  hookMessages,
			Model:     model,
			Kind:      kind,
			UserText:  userText,
		})
		if herr != nil {
			// Per-hook errors are already swallowed by the runner; this
			// path only fires on a runner-level fault. Log and proceed
			// with the unmodified slice — never block a send on hooks.
			logging.L().Warn("llm.presend.runner_failed",
				"session_id", sessionID, "err", herr.Error())
		} else {
			// Hooks may have prepended / replaced messages. Drop the
			// sidecar when the slice length changed; the original index
			// alignment no longer holds and we'd rather lose the block
			// shape than splice it onto the wrong turn. WP04 lands a
			// hook surface that understands blocks natively.
			if len(out.Messages) != len(hookMessages) {
				blockSidecar = nil
			}
			hookMessages = out.Messages
		}
	}
	outMsgs := make([]corellm.Message, 0, len(hookMessages))
	for i, m := range hookMessages {
		role := corellm.Role(m.Role)
		if role == "" {
			role = corellm.RoleUser
		}
		if blocks, ok := blockSidecar[i]; ok && len(blocks) > 0 {
			outMsgs = append(outMsgs, corellm.Message{Role: role, Content: blocks})
			continue
		}
		outMsgs = append(outMsgs, corellm.NewTextMessage(role, m.Content))
	}
	return outMsgs, nil
}

// flattenBlocks joins every text block in declaration order with a
// blank line separator. Mirrors corellm.Message.Text but works
// directly on a []ContentBlock slice so callers that haven't built a
// Message yet can compute the legacy text projection.
func flattenBlocks(blocks []corellm.ContentBlock) string {
	out := ""
	for _, b := range blocks {
		if b.Type != "" && b.Type != "text" {
			continue
		}
		if b.Text == "" {
			continue
		}
		if out != "" {
			out += "\n\n"
		}
		out += b.Text
	}
	return out
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

	// Resolve provider kind + effective model so the pre/post hook
	// runners can apply Match filters without a second registry pass.
	kind, effectiveModel := a.profileKindAndModel(profileID, modelOverride)

	messages, err := a.buildMessages(ctx, sessionID, effectiveModel, kind)
	if err != nil {
		log.Error("llm.start_stream.failed", "stage", "buildMessages", "err", err.Error())
		return "", err
	}
	req := corellm.GenerationRequest{
		ProfileID: profileID,
		Model:     modelOverride,
		Messages:  messages,
	}
	// Populate the tool catalog so the model knows what it may invoke.
	// Discovery failures are non-fatal: degrade to a no-tools request
	// rather than block the chat surface on a flaky pool.
	if a.tools != nil {
		discovered, derr := a.tools.Tools(ctx, sessionID)
		if derr != nil {
			log.Warn("llm.tool_discovery.failed",
				"session_id", sessionID, "err", derr.Error())
		} else {
			req.Tools = discovered
			toolNames := make([]string, 0, len(discovered))
			for _, t := range discovered {
				toolNames = append(toolNames, t.Name)
			}
			log.Info("llm.tool_discovery.ok",
				"session_id", sessionID,
				"count", len(discovered),
				"names", toolNames,
			)
		}
	} else {
		log.Info("llm.tool_discovery.no_discoverer", "session_id", sessionID)
	}

	// multimodal-io WP03 — modality gate. Walk every Message's
	// ContentBlocks for image / document blocks and reject the request
	// before opening a stream when the active model lacks the matching
	// capability. The error is surfaced via the existing chat-error
	// path (StartStream return value); the chat surface renders
	// UnsupportedModalityError.Friendly() in place of the raw text.
	if err := a.validateModalities(req, kind, effectiveModel); err != nil {
		log.Error("llm.start_stream.failed",
			"stage", "validateModalities",
			"profile_id", profileID,
			"err", err.Error(),
		)
		return "", err
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

	// Capture the latest user turn from the stored history so the
	// post_send hook fired in pump can include it without a second
	// session-history read.
	var userTurn string
	if a.history != nil && sessionID != "" {
		if stored, herr := a.history.ListMessages(ctx, sessionID); herr == nil {
			for i := len(stored) - 1; i >= 0; i-- {
				if stored[i].Role == "user" {
					userTurn = stored[i].Content
					break
				}
			}
		}
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
		userTurn:  userTurn,
		model:     effectiveModel,
		kind:      kind,
		req:       req,
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

	// Stream-chunk batching (token-stream coalescing fix).
	//
	// Wails v2's runtime.EventsEmit on macOS uses evaluateJavaScript
	// under the hood; rapid-fire calls landing within a single webview
	// message-pump cycle can be elided — middle events get dropped
	// while first+last survive. Confirmed by per-delta logging in
	// core/llm/openrouter (see openrouter.delta records in
	// ~/.kenaz/harness.log) showing 781 bytes accumulated server-side
	// vs. ~half that landing in the chat surface.
	//
	// Fix: accumulate text deltas in a per-pump builder; flush at most
	// once per ~16ms (one frame). Non-text events (finish / error /
	// usage / tool / reasoning) flush pending text first, then emit
	// immediately so ordering is preserved. End-of-stream flushes any
	// remaining pending text before returning.
	chunkCount := 0
	var assistantText []byte
	var pendingText strings.Builder

	flushPending := func() {
		if pendingText.Len() == 0 {
			return
		}
		text := pendingText.String()
		pendingText.Reset()
		a.sink.Emit("llm:stream-chunk", StreamChunkPayload{
			SubID:     sub.id,
			SessionID: sub.sessionID,
			Chunk: corellm.StreamEvent{
				Kind: corellm.StreamText,
				Text: text,
			},
		})
	}

	const flushEvery = 16 * time.Millisecond
	flushTicker := time.NewTicker(flushEvery)
	defer flushTicker.Stop()

	events := sub.stream.Events()
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			chunkCount++
			if ev.Kind == corellm.StreamText {
				assistantText = append(assistantText, ev.Text...)
				pendingText.WriteString(ev.Text)
				continue
			}
			// Non-text event: drain pending text first so ordering is
			// preserved (a finish or error must arrive AFTER the text
			// that preceded it).
			flushPending()
			a.sink.Emit("llm:stream-chunk", StreamChunkPayload{
				SubID:     sub.id,
				SessionID: sub.sessionID,
				Chunk:     ev,
			})
		case <-flushTicker.C:
			flushPending()
		}
	}
	// Final flush of anything buffered after the events channel closed.
	flushPending()
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
	//
	// One exception: if the response carries tool_use calls AND a
	// toolloop is wired, the loop owns assistant persistence (it
	// records the full tool_use envelope plus every subsequent turn).
	// Letting the pump also write would duplicate the assistant turn
	// and partially obscure the tool calls.
	deferAssistantToLoop := a.toolLoop != nil &&
		err == nil &&
		resp.FinishReason == "tool_use" &&
		len(resp.ToolCalls) > 0
	var persistedMessageID string
	persistedAssistant := false
	if a.historyW != nil &&
		sub.sessionID != "" &&
		len(assistantText) > 0 &&
		closed.Reason != "backend-error" &&
		!deferAssistantToLoop {
		mid, perr := a.historyW.AppendMessage(
			context.Background(),
			sub.sessionID,
			"assistant",
			string(assistantText),
		)
		if perr != nil {
			log.Warn("llm.stream.persist_assistant_failed",
				"sub_id", sub.id,
				"session_id", sub.sessionID,
				"err", perr.Error(),
			)
		} else {
			persistedMessageID = mid
			persistedAssistant = true
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

	// Fire post_send hooks (memory.persist, user-defined hooks). The
	// runner does not return mutations on this path — hooks are
	// side-effect only. We pass the assistant text + finish reason so
	// memory.persist can decide whether to embed and store the chunk.
	if a.hooks != nil && sub.sessionID != "" {
		a.hooks.RunPostSend(context.Background(), PostSendHookEvent{
			SessionID:     sub.sessionID,
			UserTurn:      sub.userTurn,
			AssistantTurn: string(assistantText),
			Model:         sub.model,
			Kind:          sub.kind,
			FinishReason:  closed.Reason,
		})
	}

	// Artifacts code-block detector — runs only on a clean
	// assistant-message finalize (non-tool_use finish, message
	// successfully persisted). Errors log at warn but never fail the
	// chat path; the artifact tab simply stays empty for that turn.
	if a.artifacts != nil &&
		persistedAssistant &&
		!deferAssistantToLoop &&
		sub.sessionID != "" &&
		len(assistantText) > 0 {
		if aerr := a.artifacts.OnAssistantMessage(
			context.Background(),
			sub.sessionID,
			persistedMessageID,
			string(assistantText),
		); aerr != nil {
			log.Warn("llm.stream.artifacts_capture_failed",
				"sub_id", sub.id,
				"session_id", sub.sessionID,
				"err", aerr.Error(),
			)
		}
	}

	// Hand off to the toolloop when the model asked for tools and one
	// is wired. We deliberately run *after* the stream-closed event so
	// the chat surface gets the canonical close signal for the initial
	// turn before the orchestrator loops; subsequent turns produced
	// inside the loop don't surface as deltas in WP01 (streaming
	// feedback during the loop lands in WP04).
	if deferAssistantToLoop {
		if loopErr := a.toolLoop.Run(
			context.Background(),
			sub.sessionID,
			sub.id,
			&resp,
			sub.req,
		); loopErr != nil {
			log.Warn("llm.stream.toolloop_failed",
				"sub_id", sub.id,
				"session_id", sub.sessionID,
				"err", loopErr.Error(),
			)
		}
	}
}

// validateModalities walks req.Messages[*].Content[*] for image /
// document blocks and rejects the request when the active model lacks
// the matching capability. Returns a *corellm.UnsupportedModalityError
// keyed to the first violating modality. Empty kind / model (no
// adapter resolvable) is treated as "no gate" so a misrouted profile
// does not silently swallow modality information.
func (a *API) validateModalities(req corellm.GenerationRequest, kind, model string) error {
	hasImage := false
	hasDocument := false
	for _, m := range req.Messages {
		for _, blk := range m.Content {
			switch blk.Type {
			case "image":
				hasImage = true
			case "document":
				hasDocument = true
			}
		}
	}
	if !hasImage && !hasDocument {
		return nil
	}
	desc, ok := a.lookupCapabilities(kind, model)
	if !ok {
		return nil
	}
	if hasImage && !desc.Has(corellm.CapVision) {
		return &corellm.UnsupportedModalityError{Modality: "image", Model: model}
	}
	if hasDocument && !desc.Has(corellm.CapDocuments) {
		return &corellm.UnsupportedModalityError{Modality: "document", Model: model}
	}
	return nil
}

// lookupCapabilities resolves the descriptor for (kind, model) by
// asking the adapter registered under kind. Returns (zero, false) when
// the registry lacks an AdapterLookup or the adapter is unregistered;
// callers treat that as "skip the gate" rather than failing closed so a
// future provider kind without a YAML entry still chats.
func (a *API) lookupCapabilities(kind, model string) (corellm.CapabilityDescriptor, bool) {
	if a == nil || a.reg == nil || kind == "" {
		return corellm.CapabilityDescriptor{}, false
	}
	lookup, ok := a.reg.(AdapterLookup)
	if !ok || lookup == nil {
		return corellm.CapabilityDescriptor{}, false
	}
	adapter := lookup.Adapter(kind)
	if adapter == nil {
		return corellm.CapabilityDescriptor{}, false
	}
	return adapter.Capabilities(model), true
}

// profileKindAndModel resolves the provider kind + effective model
// for the supplied profile and override. Falls back to ("", model)
// when the registry lookup fails so a missing profile id does not
// break hook Match filters.
func (a *API) profileKindAndModel(profileID, modelOverride string) (kind, model string) {
	if a.reg == nil {
		return "", modelOverride
	}
	prof, err := a.reg.Profile(profileID)
	if err != nil {
		return "", modelOverride
	}
	if modelOverride != "" {
		return prof.Kind, modelOverride
	}
	return prof.Kind, prof.Model
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

// ResolveConfirm forwards the user's confirm-each decision to the
// gateway. The gateway looks up the pending request by id and unblocks
// the toolloop goroutine waiting in RequestConfirm. Returns an error
// when the gateway is unwired (default in tests / pre-WP05 builds) or
// when the request id / decision is invalid.
func (a *API) ResolveConfirm(_ context.Context, requestID, decision string) error {
	if a == nil || a.confirmGateway == nil {
		return errors.New("llm: confirm gateway not wired")
	}
	return a.confirmGateway.ResolveConfirm(requestID, toolloop.ConfirmDecision(decision))
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
