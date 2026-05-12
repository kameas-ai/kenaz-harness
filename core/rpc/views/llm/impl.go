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
	"os"
	"runtime"
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

// ModelInfoLookup is implemented by adapters that expose a per-model
// info lookup driven by their own dynamic source (e.g. OpenRouter's
// /api/v1/models endpoint). When a provider's adapter satisfies this
// interface, ListProviders prefers its values over the static
// capabilities catalog so live data (context_window, description) flows
// to the frontend chat-bar without re-issuing HTTP on every call.
//
// The concrete *openrouter.Adapter satisfies this interface; other
// adapters can opt in as their dynamic catalogs land.
type ModelInfoLookup interface {
	LookupModelInfo(modelID string) (corellm.ModelInfo, bool)
}

// AdapterRefresher is implemented by adapters that can refresh their
// dynamic model cache asynchronously. ListProviders kicks a refresh
// when the lookup misses so the next call sees populated data.
type AdapterRefresher interface {
	RefreshModelsAsync(cred []byte)
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

// CapCatalog is the capability-lookup seam used to populate ModelInfos
// (contextWindow, maxOutputTokens) on Provider at ListProviders time and
// to resolve per-provider attachment limits (multimodal-io-01KQ8TDF WP04).
// The concrete *capabilities.Catalog satisfies it; tests may inject a
// fake. nil → ModelInfos fields default to 0 (unknown).
type CapCatalog interface {
	// ContextWindow returns the curated max context length in tokens
	// for (provider, model). Returns 0 when the model is unknown.
	ContextWindow(provider, model string) int
	// MaxOutputTokens returns the curated per-turn output token cap for
	// (provider, model). Returns 0 when the model is unknown
	// (backend-context-window-length-01KQ8TD3 WP01).
	MaxOutputTokens(provider, model string) int
	// AttachmentLimits returns the resolved attachment capability descriptor
	// for (provider, model). Returns a zero-value descriptor when unknown.
	// (multimodal-io-01KQ8TDF WP04 / FR-007)
	AttachmentLimits(provider, model string) AttachmentLimitsResult
}

// AttachmentLimitsResult mirrors capabilities.AttachmentDescriptor across
// the CapCatalog seam without creating a hard import dependency on
// core/llm/capabilities from the view layer.
type AttachmentLimitsResult struct {
	ImageInput              bool
	DocumentInput           bool
	MaxImageBytes           int64
	MaxDocumentBytes        int64
	MaxImageCountPerMessage int
	MaxImagePixels          int64
	MaxDocumentPages        int
	ImageInputMimeTypes     []string
	DocumentInputMimeTypes  []string
}

// CredPeeker resolves a credential reference to a display-safe Redacted
// value (credstore.Peek). Wire via Config.CredPeeker; nil = no
// redaction in ListProviders responses (Redaction field stays zero).
type CredPeeker interface {
	// PeekCred returns the display string, kind, and locator-safe id for
	// ref. Display rules: len>12 → "first4…last4"; else "••••••••".
	// On resolver error the returned Redacted has Display="••••••••"
	// and Kind="unset".
	PeekCred(ctx context.Context, kind, locator string) Redacted
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

// ChatRunner is the kernel-driven entry point that replaces the
// toolloop-based StartStream when wired (chat-migration WP04). The
// LLM impl prefers ChatRunner when both ChatRunner and ToolLoop are
// configured; the toolloop path remains the default until parity
// tests in WP06 turn green.
//
// Defined here as a narrow interface so the impl doesn't import the
// chat package directly (DIRECTIVE_001 — keeps the import direction
// one-way: chat package can import llm view, not the other way around).
type ChatRunner interface {
	StartStream(ctx context.Context, profileID, sessionID, modelOverride, userMessage string) (string, error)
	StopStream(ctx context.Context, subID string) error
	// HasPausedSubFor reports whether a paused turn exists for the given
	// profileID and returns its sub_id token.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	HasPausedSubFor(profileID string) (token string, ok bool)
	// RedriveLastTurn re-issues a kernel run for the paused turn
	// identified by profileID without re-appending the user message.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	RedriveLastTurn(ctx context.Context, profileID string) (newSubID string, err error)
}

// CredentialInvalidator is the secrets-cache seam the rotation pipeline
// calls after a successful keychain write to evict any cached credential
// so the next registry.Stream call resolves fresh bytes.
//
// The concrete *credref.Resolver does NOT expose Invalidate; the secrets
// backend (core/secrets.Resolver) does — the rpc wiring passes a thin
// adapter over secrets.Resolver.Invalidate.
// (provider-keychain-rotation-01KQ8TD9 WP04)
type CredentialInvalidator interface {
	// InvalidateCred evicts the cached credential for (kind, locator).
	InvalidateCred(kind, locator string)
}

// AuditEmitter is the narrow audit surface the rotation pipeline uses.
// The rpc wiring passes a concrete *audit.Emitter (or equivalent) that
// implements this via audit.Emit.
// (provider-keychain-rotation-01KQ8TD9 WP04)
type AuditEmitter interface {
	EmitRotated(ctx context.Context, provider, profileID, source string, rotatedAt time.Time) error
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
	// credPeeker, when non-nil, is called by ListProviders to populate
	// Provider.Redaction for each profile (WP05).
	credPeeker CredPeeker
	// capCatalog, when non-nil, is consulted by ListProviders to populate
	// Provider.ModelInfos with contextWindow data from the curated table.
	capCatalog CapCatalog
	// attachments is the WP03 source of truth for resolved starting
	// context. nil falls back to the SessionContextReader probe so
	// Mission A behaviour stays intact during the one-release buffer.
	attachments AttachmentsResolver
	// chatRunner is the kernel-driven entry point. The chassis-side
	// chat path forwards every StartStream into the chat package's
	// ChatRunner once it's wired; production builds never run with a
	// nil chatRunner. The legacy toolloop pump path was deleted in
	// the agent-kernel-graph-chat-migration cutover.
	chatRunner ChatRunner
	// tools projects the MCP pool's catalog onto each GenerationRequest
	// so the model knows what it may invoke. nil silences discovery
	// and keeps WP00 chat-only behaviour for tests that don't wire an
	// MCP pool.
	tools corellm.ToolDiscoverer

	// credInvalidator, when non-nil, is called after a successful keychain
	// write to evict any cached credential bytes so the next resolve picks
	// up the freshly written key (provider-keychain-rotation-01KQ8TD9 WP04).
	credInvalidator CredentialInvalidator

	// auditRotation, when non-nil, is called to emit KindProviderKeyRotated
	// after a successful rotation (provider-keychain-rotation-01KQ8TD9 WP04).
	auditRotation AuditEmitter

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
	// ChatRunner is the kernel-driven entry point that replaces the
	// toolloop chat path. The chassis wires this on every boot;
	// production never runs with a nil ChatRunner. Tests pass a fake
	// runner when they need to assert StartStream forwarding.
	ChatRunner ChatRunner
	// Tools, when non-nil, is consulted on every StartStream to
	// populate GenerationRequest.Tools. Without it the model is never
	// told about the MCP catalog so the agent loop has nothing to
	// dispatch against.
	Tools corellm.ToolDiscoverer
	// Artifacts, when non-nil, fires the code-block detector against
	// the freshly persisted assistant message at stream completion
	// (non-tool_use finish only). nil leaves the chat path untouched.
	Artifacts ArtifactSink
	// CredPeeker, when non-nil, is called by ListProviders to populate
	// Provider.Redaction for each profile. nil = Redaction field
	// is omitted (zero value) — no breaking change for existing tests.
	CredPeeker CredPeeker
	// CapCatalog, when non-nil, is consulted by ListProviders to populate
	// Provider.ModelInfos with contextWindow data from the curated table.
	// nil = ModelInfos fields default to 0 (unknown) — frontend falls
	// back to MODEL_CONTEXT_FALLBACK.
	CapCatalog CapCatalog
	// CredInvalidator, when non-nil, is called after TestAndRotateKey
	// successfully writes the new key to evict the cached credential.
	// nil → cache entry lives until the TTL fires (next resolve will use
	// the new key regardless — the keychain write is canonical).
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	CredInvalidator CredentialInvalidator
	// AuditRotation, when non-nil, emits KindProviderKeyRotated on
	// successful rotation. nil → audit is silently skipped.
	// (provider-keychain-rotation-01KQ8TD9 WP04)
	AuditRotation AuditEmitter
}

// New constructs a concrete API.
func New(cfg Config) *API {
	sink := cfg.Sink
	if sink == nil {
		sink = nopSink{}
	}
	return &API{
		reg:             cfg.Registry,
		sink:            sink,
		store:           cfg.Store,
		bundles:         cfg.Bundles,
		keychain:        cfg.Keychain,
		prober:          cfg.Prober,
		history:         cfg.History,
		historyW:        cfg.HistoryWriter,
		hooks:           cfg.Hooks,
		attachments:     cfg.Attachments,
		chatRunner:      cfg.ChatRunner,
		tools:           cfg.Tools,
		artifacts:       cfg.Artifacts,
		credPeeker:      cfg.CredPeeker,
		capCatalog:      cfg.CapCatalog,
		credInvalidator: cfg.CredInvalidator,
		auditRotation:   cfg.AuditRotation,
		subs:            map[string]*subscription{},
		validated:       map[string]bool{},
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
func (a *API) ListProviders(ctx context.Context) ([]Provider, error) {
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
		// WP05: populate Redaction via credPeeker if wired.
		if a.credPeeker != nil {
			v.Redaction = a.credPeeker.PeekCred(ctx, v.Cred.Kind, v.Cred.Locator)
		}
		// Populate ModelInfos with the best per-model data available.
		// Resolution order: dynamic adapter lookup (live source of truth
		// for OpenRouter and similar) → curated YAML catalog → 0
		// (frontend falls back to MODEL_CONTEXT_FALLBACK).
		//
		// We prefer dynamic over curated because the adapter's cache
		// reflects the upstream API and avoids stale hand-maintained
		// values drifting behind reality.
		if len(v.Models) > 0 {
			var lookup ModelInfoLookup
			var refresher AdapterRefresher
			if al, ok := a.reg.(AdapterLookup); ok {
				if ad := al.Adapter(v.Kind); ad != nil {
					if ml, ok := ad.(ModelInfoLookup); ok {
						lookup = ml
					}
					if rf, ok := ad.(AdapterRefresher); ok {
						refresher = rf
					}
				}
			}
			infos := make([]ModelInfo, 0, len(v.Models))
			missCount := 0
			for _, modelID := range v.Models {
				info := ModelInfo{ID: modelID, DisplayName: modelID}
				resolved := false
				if lookup != nil {
					if mi, ok := lookup.LookupModelInfo(modelID); ok {
						info.ContextWindow = mi.ContextWindow
						info.MaxOutputTokens = mi.MaxOutputTokens
						if mi.DisplayName != "" {
							info.DisplayName = mi.DisplayName
						}
						info.Description = mi.Description
						resolved = true
						logging.L().Debug("llm.model_info.dynamic_hit",
							"provider_id", v.ID,
							"kind", v.Kind,
							"model_id", modelID,
							"context_window", mi.ContextWindow,
							"max_output_tokens", mi.MaxOutputTokens)
					}
				}
				if !resolved && a.capCatalog != nil {
					cw := a.capCatalog.ContextWindow(v.Kind, modelID)
					mot := a.capCatalog.MaxOutputTokens(v.Kind, modelID)
					info.ContextWindow = cw
					info.MaxOutputTokens = mot
					if cw > 0 {
						resolved = true
						logging.L().Debug("llm.model_info.catalog_hit",
							"provider_id", v.ID,
							"kind", v.Kind,
							"model_id", modelID,
							"context_window", cw,
							"max_output_tokens", mot)
					}
				}
				if !resolved {
					missCount++
					logging.L().Info("llm.model_info.miss",
						"provider_id", v.ID,
						"kind", v.Kind,
						"model_id", modelID,
						"reason", "no dynamic lookup hit and no catalog entry; frontend will use MODEL_CONTEXT_FALLBACK")
				}
				infos = append(infos, info)
			}
			v.ModelInfos = infos
			// On a miss with a refresher available, kick a background
			// refresh so the NEXT ListProviders call sees populated
			// data. The refresh is rate-limited inside the adapter via
			// modelCacheTTL + refreshBackoff so we won't hammer the API.
			if missCount > 0 && refresher != nil {
				logging.L().Info("llm.model_info.refresh_kick",
					"provider_id", v.ID,
					"kind", v.Kind,
					"miss_count", missCount)
				refresher.RefreshModelsAsync(nil)
			}
		}
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
// modelOverride is a per-call selection from the profile's authorised
// models — the chat surface's model-switcher picks one and passes it
// here. Empty => use the profile default.
//
// The chat path is fully kernel-driven: StartStream forwards every
// turn into the wired ChatRunner, which owns provider resolution,
// history loading, kernel run, streaming, and assistant persistence
// (via SessionWriteNode). A nil ChatRunner indicates a chassis-build
// failure — the surface returns "not wired" rather than silently
// falling back to a deleted legacy pump path.
func (a *API) StartStream(ctx context.Context, profileID, sessionID, modelOverride string) (string, error) {
	log := logging.L()
	log.Info("llm.start_stream.requested",
		"profile_id", profileID,
		"session_id", sessionID,
		"model_override", modelOverride,
		"chat_runner_wired", a.chatRunner != nil,
	)
	if a.chatRunner == nil {
		log.Error("llm.start_stream.failed", "reason", "chat runner not wired")
		return "", errors.New("llm: chat runner not wired")
	}
	// Pull the latest user message — the chat surface posts the user
	// turn via Sessions_AppendMessage immediately before calling
	// StartStream, so the trailing user row is the new turn. The
	// runner re-appends it via HistoryWriter so the kernel run sees
	// consistent history.
	var userMessage string
	if a.history != nil && sessionID != "" {
		if stored, herr := a.history.ListMessages(ctx, sessionID); herr == nil {
			for i := len(stored) - 1; i >= 0; i-- {
				if stored[i].Role == "user" {
					userMessage = stored[i].Content
					break
				}
			}
		}
	}
	return a.chatRunner.StartStream(ctx, profileID, sessionID, modelOverride, userMessage)
}

// StopStream terminates the subscription. Forwards to the ChatRunner
// (every sub id is now chat-runner-issued).
func (a *API) StopStream(ctx context.Context, subID string) error {
	if a.chatRunner == nil {
		return errors.New("llm: chat runner not wired")
	}
	return a.chatRunner.StopStream(ctx, subID)
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
			ID:              m.ID,
			DisplayName:     m.DisplayName,
			Description:     m.Description,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	return out, nil
}

// ResolveConfirm is a deprecated stub retained for the LLMConnectorAPI
// surface compatibility (Wails binding contract). The confirm-each
// modal flow was deleted alongside core/toolloop in the chat-migration
// cutover; v1 alpha relies on Cedar policy gates instead. Always
// returns an error so the frontend modal can render a helpful message
// when bound — no dispatch path resolves through this method anymore.
func (a *API) ResolveConfirm(_ context.Context, _, _ string) error {
	return errors.New("llm: confirm-each is retired; use cedar policies to gate tool dispatch")
}

// UpdateProviderCredential writes a new plaintext API key for profileID
// directly to the OS keychain and zeroes the buffer before returning
// (credential-store-01KQ8TDD WP05 / FR-007). This is the ONLY RPC that
// accepts plaintext — it is consumed and destroyed at the binding layer.
//
// Errors:
//   - ErrPersonalStoreUnavailable — no backing store.
//   - ErrBundleProviderImmutable — profile is bundle-derived.
//   - errors.New — keychain not configured, empty plaintext, or write fail.
func (a *API) UpdateProviderCredential(ctx context.Context, profileID, plaintext string) error {
	if a.store == nil {
		return ErrPersonalStoreUnavailable
	}
	if a.bundles != nil {
		for _, p := range a.bundles.BundleProfiles() {
			if p.ID == profileID {
				return fmt.Errorf("%w: %q", ErrBundleProviderImmutable, profileID)
			}
		}
	}
	if plaintext == "" {
		return errors.New("llm: UpdateProviderCredential: empty plaintext")
	}
	profile, err := a.lookupProfile(profileID)
	if err != nil {
		return err
	}
	if profile.Cred.Kind != "keychain" {
		return fmt.Errorf("llm: UpdateProviderCredential: profile %q uses kind=%q; only keychain credentials are writable via this RPC", profileID, profile.Cred.Kind)
	}
	if a.keychain == nil {
		return errors.New("llm: UpdateProviderCredential: no keychain writer configured")
	}
	buf := []byte(plaintext)
	plaintext = "" // zero the Go string-local copy
	err = a.keychain.Write(ctx, profile.Cred.Locator, buf)
	runtime.KeepAlive(buf)
	zeroBytes(buf)
	if err != nil {
		return fmt.Errorf("llm: UpdateProviderCredential keychain write %q: %w", profile.Cred.Locator, err)
	}
	return nil
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

// GetAttachmentLimits returns the resolved attachment capability descriptor
// for the given provider kind + model. Returns a zero-value descriptor
// (everything false/0) when no catalog is wired or the provider/model is
// unknown — the frontend treats zero as "no attachments supported".
// (multimodal-io-01KQ8TDF WP04 / FR-007)
func (a *API) GetAttachmentLimits(_ context.Context, provider, model string) (AttachmentLimitsView, error) {
	if a.capCatalog == nil {
		return AttachmentLimitsView{}, nil
	}
	r := a.capCatalog.AttachmentLimits(provider, model)
	return AttachmentLimitsView{
		ImageInput:              r.ImageInput,
		DocumentInput:           r.DocumentInput,
		MaxImageBytes:           r.MaxImageBytes,
		MaxDocumentBytes:        r.MaxDocumentBytes,
		MaxImageCountPerMessage: r.MaxImageCountPerMessage,
		MaxImagePixels:          r.MaxImagePixels,
		MaxDocumentPages:        r.MaxDocumentPages,
		ImageInputMimeTypes:     r.ImageInputMimeTypes,
		DocumentInputMimeTypes:  r.DocumentInputMimeTypes,
	}, nil
}

// TestAndRotateKey validates plaintextApiKey against the provider's /models
// endpoint and — on success — overwrites the stored keychain entry, evicts
// the cached credential, and emits a KindProviderKeyRotated audit event.
//
// Pipeline (provider-keychain-rotation-01KQ8TD9 WP04 plan §3):
//  1. Resolve the profile and validate it is keychain-backed.
//  2. Resolve the adapter via AdapterLookup.
//  3. Cast to corellm.KeyTester; surface friendly message if unsupported.
//  4. TestKey — 5-second deadline enforced inside the adapter.
//  5. Only on pass: KeychainWriter.Write, then zero the buffer.
//  6. Only on pass: CredentialInvalidator.InvalidateCred.
//  7. Only on pass: AuditEmitter.EmitRotated.
//  8. Only on pass: look up paused chat sub; mint AutoResumeToken if found.
func (a *API) TestAndRotateKey(ctx context.Context, profileID, plaintextApiKey, source string) (RotationResult, error) {
	// Zero the plaintext on return regardless of outcome.
	buf := []byte(plaintextApiKey)
	plaintextApiKey = ""
	defer func() {
		runtime.KeepAlive(buf)
		zeroBytes(buf)
	}()

	t0 := time.Now()

	// Feature-flag guard: if the HARNESS_KEYCHAIN_ROTATION env var is off,
	// refuse the call so the bindings layer can surface a helpful message.
	if !keychainRotationFeatureEnabled() {
		return RotationResult{
			Success:   false,
			Message:   "key rotation is disabled; set HARNESS_KEYCHAIN_ROTATION=on to enable",
			LatencyMS: int(time.Since(t0).Milliseconds()),
			TestedAt:  t0,
		}, nil
	}

	// 1. Profile lookup + keychain-kind gate.
	profile, err := a.lookupProfile(profileID)
	if err != nil {
		return RotationResult{}, err
	}
	if profile.Cred.Kind != "keychain" {
		if profile.Kind == "bedrock" {
			return RotationResult{
				Success:  false,
				Message:  "this Bedrock profile uses AWS credentials — rotate via `aws configure` and try again",
				TestedAt: t0,
			}, nil
		}
		return RotationResult{
			Success:  false,
			Message:  fmt.Sprintf("profile %q uses cred kind %q; only keychain credentials are rotatable via this UI", profileID, profile.Cred.Kind),
			TestedAt: t0,
		}, nil
	}

	// 2. Adapter lookup.
	lookup, ok := a.reg.(AdapterLookup)
	if !ok || lookup == nil {
		return RotationResult{Success: false, Message: "no adapter registry available", TestedAt: t0}, nil
	}
	adapter := lookup.Adapter(profile.Kind)
	if adapter == nil {
		return RotationResult{Success: false, Message: fmt.Sprintf("no adapter registered for kind %q", profile.Kind), TestedAt: t0}, nil
	}

	// 3. KeyTester capability check.
	tester, hasTester := adapter.(corellm.KeyTester)
	if !hasTester {
		return RotationResult{
			Success:   false,
			Message:   "test unavailable for this provider — please verify the key in the provider console",
			LatencyMS: int(time.Since(t0).Milliseconds()),
			TestedAt:  t0,
		}, nil
	}

	// 4. Test the new key.
	testErr := tester.TestKey(ctx, buf)
	latencyMS := int(time.Since(t0).Milliseconds())
	if testErr != nil {
		return RotationResult{
			Success:   false,
			Message:   "provider rejected the new key — try again",
			LatencyMS: latencyMS,
			TestedAt:  t0,
		}, nil
	}

	// 5. Write the new key to the keychain.
	if a.keychain == nil {
		return RotationResult{}, errors.New("llm: TestAndRotateKey: no keychain writer configured")
	}
	if err := a.keychain.Write(ctx, profile.Cred.Locator, buf); err != nil {
		return RotationResult{}, fmt.Errorf("llm: TestAndRotateKey: keychain write %q: %w", profile.Cred.Locator, err)
	}
	// Buffer is zeroed by defer; no further access after this point.

	// 6. Invalidate the cached credential so the next Stream call resolves fresh.
	if a.credInvalidator != nil {
		a.credInvalidator.InvalidateCred(profile.Cred.Kind, profile.Cred.Locator)
	}

	rotatedAt := time.Now()

	// 7. Audit emission.
	if a.auditRotation != nil {
		src := source
		if src == "" {
			src = "inline-toast"
		}
		_ = a.auditRotation.EmitRotated(ctx, profile.Kind, profileID, src, rotatedAt)
	}

	// 8. Auto-resume token: look up any paused chat sub for this profile.
	autoResumeToken := ""
	if a.chatRunner != nil {
		if token, ok := a.chatRunner.HasPausedSubFor(profileID); ok {
			autoResumeToken = token
		}
	}

	return RotationResult{
		Success:         true,
		LatencyMS:       latencyMS,
		TestedAt:        t0,
		AutoResumeToken: autoResumeToken,
	}, nil
}

// keychainRotationFeatureEnabled reads HARNESS_KEYCHAIN_ROTATION at call time.
// Mirrors the chat-runner helper but lives here so the LLM view does not
// import the chat package (DIRECTIVE_001).
func keychainRotationFeatureEnabled() bool {
	v := os.Getenv("HARNESS_KEYCHAIN_ROTATION")
	switch v {
	case "off", "0", "false":
		return false
	default:
		return true
	}
}

// ResumeAfterKeyRotation drives a fresh kernel run for the paused chat
// turn associated with the given profileID (the resumeToken is the
// profile_id from the RotationResult — the sub_id was just a hint for
// the frontend to correlate the toast with the subscription).
//
// If no paused turn exists, returns nil (no-op). This keeps the wire
// shape safe to call without a pre-flight check on the frontend.
// (provider-keychain-rotation-01KQ8TD9 WP04)
func (a *API) ResumeAfterKeyRotation(ctx context.Context, resumeToken string) error {
	if a.chatRunner == nil {
		return nil
	}
	// resumeToken is the profileID; RedriveLastTurn looks up the paused
	// turn by profileID and removes it from the table atomically.
	_, err := a.chatRunner.RedriveLastTurn(ctx, resumeToken)
	if err != nil {
		// A miss is a no-op rather than an error — the user may have
		// already dismissed the toast and manually resubmitted.
		logging.L().Warn("chat.redrive_last_turn.miss",
			"profile_id", resumeToken, "err", err.Error())
		return nil
	}
	return nil
}
