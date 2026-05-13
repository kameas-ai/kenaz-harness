package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/compaction"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/tokenizer"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// envCompactionDisabled is the env-var sentinel value the harness
// honors to skip compaction at the chat-runner pre-send hook AND at
// the scheduler boundary (mission compaction-strategy-ui-01KQ8TDI WP08
// / plan §4 rollout). Setting HARNESS_COMPACTION=off short-circuits
// every compaction-driven branch so users can A/B test the feature
// without restarting the harness for the chat hook (the scheduler
// reads the env once at boot and stays consistent for its lifetime —
// see core/rpc/api.go boot wiring).
const envCompactionVar = "HARNESS_COMPACTION"
const envCompactionDisabled = "off"

// envKeychainRotationVar / envKeychainRotationOff control the key-rotation
// feature flag (provider-keychain-rotation-01KQ8TD9 WP03). When the env var
// is set to "off", "0", or "false", the chat runner skips the auth-failure
// interception path entirely and falls through to the existing backend-error
// close — preserving byte-identical behaviour with today's unpatched path.
const envKeychainRotationVar = "HARNESS_KEYCHAIN_ROTATION"

// keychainRotationEnabled reads the feature flag once per call. "off",
// "0", and "false" disable; anything else (including absence) enables.
func keychainRotationEnabled() bool {
	v := os.Getenv(envKeychainRotationVar)
	switch v {
	case "off", "0", "false":
		return false
	default:
		return true
	}
}

// compactionDisabledByEnv reports whether HARNESS_COMPACTION=off is
// set. The chat-runner pre-send hook reads this on every send so a
// mid-day toggle takes effect on the next user turn without a chassis
// restart (per WP08 acceptance: "HARNESS_COMPACTION=off short-circuits
// the hook").
func compactionDisabledByEnv() bool {
	return os.Getenv(envCompactionVar) == envCompactionDisabled
}

// chatAskNodeID is the AskNode id the chassis chat graph (chat_default)
// uses to gate per-turn user input. The runner pre-seeds the AskBus
// answer for this node id on every StartStream so the graph's first
// fire progresses past the AskNode without a chassis-side resume.
const chatAskNodeID = "ask_user"

// SessionMessageReader is the slice of session.Manager the runner
// consumes. Defined here so the chat package doesn't import
// core/session (DIRECTIVE_001).
type SessionMessageReader interface {
	History(ctx context.Context, sessionID string, n int) ([]coreag.Message, error)
}

// HistoryWriter is the persistence surface the SessionWriteNode kind
// uses. Production wiring binds this to core/session.Manager via an
// adapter; tests pass a recording fake.
type HistoryWriter = coreag.HistoryWriter

// MaxTurnsResolver returns the per-run iteration cap to apply onto
// the chat graph's LoopNode max_iterations. The chassis wires this
// to Settings.EffectiveMaxAgentTurns so each StartStream picks up the
// current user-tuned cap without a restart.
type MaxTurnsResolver func() int

// GraphLoader returns the parsed chat graph spec to drive each run.
// In production this loads the bundled chat_default.yaml; tests can
// substitute an arbitrary graph.
type GraphLoader func() (coreag.Graph, error)

// AnswerInjector pushes the latest user message answer into the
// kernel's AskBus for the supplied (runID, askNodeID). The chassis
// wires this to the agentgraph manager's askRouter so the chat
// runner can reuse the existing pause/resume plumbing.
type AnswerInjector func(runID, nodeID, answer string)

// ToolCatalogDiscoverer projects the chassis-side MCP pool catalog
// (plus built-in tools) onto a per-session ToolSpec slice the LLM
// provider adapter forwards to the upstream model. Defined narrow so
// the chat package stays free of the LLM view's discoverer types.
type ToolCatalogDiscoverer interface {
	Tools(ctx context.Context, sessionID string) ([]corellm.ToolSpec, error)
}

// Config bundles the dependencies needed to construct a ChatRunner.
// Every field is required — the runner does not default-fallback on
// production paths because a missing seam would silently deliver a
// broken chat experience.
type Config struct {
	Kernel        *coreag.Kernel
	Registry      corellm.Registry
	Pool          ToolPool
	Perms         ToolPermissionResolver
	Broker        Broker
	History       SessionMessageReader
	HistoryWriter HistoryWriter
	GraphLoader   GraphLoader
	MaxTurns      MaxTurnsResolver
	// ToolDiscoverer publishes the chat-runner-level tool catalog onto
	// each LLMProviderAdapter so the model sees the live MCP+builtin
	// tool list. nil disables discovery — the chat path still works,
	// but the model is never told about any tools.
	ToolDiscoverer ToolCatalogDiscoverer
	// EnvDefaults is an optional callback the runner invokes on the
	// constructed Env before kernel.Run; production wiring threads
	// Memory / Policy / Branch / Hooks-journal seams through it.
	EnvDefaults func(env *coreag.Env)
	// Compaction is the optional pre-send compaction hook configuration
	// (mission compaction-strategy-ui-01KQ8TDI WP08). nil disables the
	// hook entirely — the chat runner falls through to the kernel run
	// without checking the token threshold. Production builds wire
	// this; tests that don't exercise compaction leave it nil.
	Compaction *CompactionDeps

	// AutoTitle is the optional post-run auto-title trigger configuration
	// (mission session-auto-titling-01KQ8TDS WP04). nil disables the
	// trigger entirely. Production builds wire this; tests that don't
	// exercise auto-titling leave it nil.
	AutoTitle *AutoTitleDeps

	// UsageHook is an optional callback fired by the LLMProviderAdapter
	// once per LLM turn, after stream.Final() returns
	// (token-cost-telemetry-01KQ8TD7 WP02). The callback receives the
	// session id, message id (empty string when the writer hasn't been
	// called yet), provider kind, model id, and the full llm.Response.
	// nil disables usage capture entirely.
	UsageHook UsageHookFunc

	// PartialPersister is the long-turn-resilience-01KR3PRS WP03 seam
	// that handles the "kernel returned an error mid-stream" case: when
	// driveRun observes a non-nil err that classifies as backend-error
	// AND the StreamBridge has accumulated text deltas, the runner
	// invokes PartialPersister.PersistPartial so the chassis can land a
	// session_messages row carrying the partial text + the resume-flow
	// metadata (streaming_failed_at, streaming_failure_kind,
	// streaming_recoverable). nil disables the seam entirely — the
	// runner emits the existing backend-error close payload and the
	// frontend's WP00 fallback (the partial bubble committed
	// frontend-side via streamingError) remains the user-visible
	// surface.
	PartialPersister PartialPersister

	// AutonomyKnobs is the optional autonomy-dial knobs provider
	// (autonomy-dial WP04). When non-nil, the kernel tool adapter reads
	// the resolved knobs before each tool call and bypasses the
	// interactive-prompt path for tool families covered by
	// AutoApproveFamilies. nil disables posture-aware short-circuiting —
	// the adapter falls through to the permission resolver on every call
	// (v0.3.0 baseline behaviour). Production wiring threads this from
	// the session's resolved autonomy knobs at StartStream time.
	AutonomyKnobs AutonomyKnobsProvider
}

// PartialPersister is the resume-flow persistence seam. Invoked by
// driveRun once per failed turn after the kernel exits with an
// unrecoverable backend error. The chassis production wiring binds
// this to a small adapter over session.Manager.AppendMessage +
// MarkStreamingFailure (long-turn-resilience-01KR3PRS WP03).
//
// PersistPartial:
//   - sessionID identifies the active session.
//   - partialText is the byte-equal accumulation of every text-delta
//     the StreamBridge saw before the drop. Always non-empty when the
//     runner invokes the seam (the runner skips PersistPartial when
//     no text was seen — the user's bubble would be empty).
//   - kind is the failure classification: "transient" | "auth" |
//     "unknown".
//   - recoverable is true when no tool_use block executed before the
//     drop (continuation prompt is safe).
//
// PersistPartial returns the persisted message id so the runner can
// surface it on the bridge's stream-closed payload — the frontend
// uses the id to wire the Resume button click into Sessions_ResumeMessage.
type PartialPersister interface {
	PersistPartial(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (messageID string, err error)
}

// PartialPersisterFunc adapts a function value to the PartialPersister
// interface so production wiring can pass a closure inline.
type PartialPersisterFunc func(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (string, error)

// PersistPartial satisfies PartialPersister.
func (f PartialPersisterFunc) PersistPartial(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
	return f(ctx, sessionID, partialText, kind, recoverable)
}

// UsageHookFunc is the callback signature for per-turn usage capture.
// sessionID and messageID identify the turn; messageID may be empty
// when the session_write node hasn't fired yet (test paths). providerKind
// and modelID carry the resolved provider kind ("anthropic", "openai", …)
// and the effective model id for the turn — used to populate
// UsageTurn.ProviderKind / UsageTurn.ModelID for token-cost-telemetry
// alignment (backend-context-window-length-01KQ8TD3 WP06). The hook
// must not block the chat turn — it should write async or accept the
// latency.
type UsageHookFunc func(ctx context.Context, sessionID, messageID, providerKind, modelID string, resp corellm.Response)

// CompactionDeps bundles every collaborator the pre-send compaction
// hook needs. The runner reads the active aggressiveness tier on every
// send (so settings changes take effect on the next user turn without
// a restart), counts tokens via the supplied tokenizer, and either
// invokes Engine.Compact (threshold tiers) or Engine.RollingSummarize
// (maximal tier). On a no-op + over-cap we surface compaction.ErrSessionFull
// to the caller; the existing chat-runner error path translates that
// into the user-facing "session full" message.
//
// Every field is required when CompactionDeps is non-nil. The caller
// is responsible for nil-checking before constructing Config.Compaction.
type CompactionDeps struct {
	// Engine is the threshold + rolling compaction surface (plan §2.4 / §2.5).
	Engine compaction.Engine

	// Aggressiveness returns the current effective tier. Read on every
	// send so a Settings change (UI dial) takes effect on the next turn
	// without restarting the harness.
	Aggressiveness func() compaction.CompactionAggressiveness

	// CompactionModel returns the (provider, model) ref the engine
	// should use for the summarization call. An ok=false reply means
	// "fall back to the chat session's active model" — the runner
	// substitutes (profileID, modelOverride) in that case so the
	// summarization runs against the same model the chat is using.
	CompactionModel func() (compaction.ProviderProfileRef, bool)

	// RecentWindow returns the locked-window size (count of most-recent
	// user-assistant pairs compaction never touches). Plumbed through
	// to RollingSummarize; the threshold engine reads its own copy
	// inside Engine.
	RecentWindow func() int

	// MaxContextTokens looks up the active model's MaxContextTokens
	// budget. Returns ok=false on an unknown model — the runner skips
	// the threshold check in that case (the provider's own gate will
	// catch any over-cap span). The compaction.CapabilityLookup
	// adapter from core/compaction/wiring provides a curated builtin
	// table covering the major providers.
	MaxContextTokens func(model compaction.ProviderProfileRef) (int, bool)
}

// modelToTokenize is the helper that builds the (system, msgs) input
// the tokenizer consumes from the chat runner's per-send state. The
// system prompt is the empty string because the chat runner doesn't
// know what system prompt the kernel will inject (that's a graph-side
// concern); the framing overhead the tokenizer adds covers the slot
// regardless.
func tokenizeRequest(history []coreag.Message, userMessage string) int {
	msgs := make([]tokenizer.Message, 0, len(history)+1)
	for _, m := range history {
		msgs = append(msgs, tokenizer.Message{Role: m.Role, Content: m.Content})
	}
	if userMessage != "" {
		msgs = append(msgs, tokenizer.Message{Role: "user", Content: userMessage})
	}
	return tokenizer.CountRequestTokens("", msgs)
}

// ToolPool is the narrow MCP-pool surface the runner consumes. Mirrors
// toolloop.MCPPool so the chassis can pass the existing wrapped pool
// without an additional adapter step. We re-declare the interface here
// (rather than aliasing toolloop) so WP07 can drop the toolloop import
// from this package without a breaking-change to runner construction.
type ToolPool interface {
	Tools(ctx context.Context) ([]ToolEntry, error)
	Call(ctx context.Context, server, tool string, args []byte) ([]byte, error)
}

// ToolEntry is the (server, name) pair the runner publishes on the
// catalog. Mirrors toolloop.Tool.
type ToolEntry struct {
	Server string
	Name   string
}

// ToolPermissionResolver is the narrow surface the runner needs to
// gate tool dispatch. Mirrors toolloop.PermissionResolver. WP06 lifts
// this into the kernel's PolicyGate seam; for now the runner accepts
// the resolver shape directly.
type ToolPermissionResolver interface {
	Resolve(ctx context.Context, sessionID, server, tool string) (PermVerdict, error)
}

// PermVerdict is the resolver's reply. Mirrors toolloop.Resolution.
type PermVerdict struct {
	Server string
	Tool   string
	Policy string // "auto_allow" | "confirm_each" | "deny"
	Reason string
}

// pausedTurn stores the state captured when a chat run is interrupted by
// *llm.ErrProviderAuthFailed so it can be redriven after a key rotation.
// (provider-keychain-rotation-01KQ8TD9 WP03)
type pausedTurn struct {
	subID         string
	sessionID     string
	profileID     string
	modelOverride string
	pausedAt      time.Time
}

// AuthFailedPayload is the broker payload emitted on the
// "provider:auth-failed" topic when a chat run is interrupted by a
// credential rejection. The frontend toast subscribes to this topic to
// show the inline rotate affordance.
// (provider-keychain-rotation-01KQ8TD9 WP03)
type AuthFailedPayload struct {
	SubID     string `json:"sub_id"`
	SessionID string `json:"session_id"`
	ProfileID string `json:"profile_id"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Reason    string `json:"reason"`
}

// AuthResumedPayload is the broker payload emitted on the
// "provider:auth-resumed" topic after a successful key rotation when
// RedriveLastTurn has re-driven the paused turn. Subscribers (chat
// surface useSession; AuthFailureToast / RetryAfterRotateToast queues)
// use this signal to clear any stale auth-failure UI state so the user
// is not left looking at a "paused for key rotation" banner over a
// turn that is once again live.
type AuthResumedPayload struct {
	ProfileID string `json:"profile_id"`
	NewSubID  string `json:"new_sub_id"`
}

// ChatRunner is the kernel-driven entry point that replaces
// core/toolloop as the chassis chat path. One runner per process; the
// chassis constructs it inside the LLM view's wiring and passes it to
// the LLM API via the WP04 Config.ChatRunner field.
//
// StartStream is goroutine-safe: every call constructs a fresh kernel
// run and a fresh subscription id. The runner keeps a per-subscription
// cancel function so StopStream can propagate cancellation upstream.
type ChatRunner struct {
	cfg Config

	mu         sync.Mutex
	subs       map[string]*chatSub
	pausedSubs map[string]*pausedTurn // keyed by profileID; last-write-wins
	nextID     uint64
}

// chatSub is the per-StartStream bookkeeping entry.
type chatSub struct {
	id            string
	sessionID     string
	profileID     string // retained for post-run auto-title trigger
	modelOverride string // retained for post-run auto-title trigger
	cancel        context.CancelFunc
	done          chan struct{}
	bridge        *StreamBridge
	finished      atomic.Bool
}

// New constructs a ChatRunner. Every Config field is validated; a
// missing required seam returns an error so the chassis fails fast
// rather than starting a broken chat surface.
func New(cfg Config) (*ChatRunner, error) {
	if cfg.Kernel == nil {
		return nil, errors.New("chat: kernel required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("chat: llm registry required")
	}
	if cfg.Broker == nil {
		return nil, errors.New("chat: stream broker required")
	}
	if cfg.GraphLoader == nil {
		return nil, errors.New("chat: graph loader required")
	}
	if cfg.MaxTurns == nil {
		// Default to the spec cap; loud-fail if the chassis didn't
		// thread settings through, but don't crash a test path that
		// just wants the kernel-driven chat run with a hardcoded 25.
		cfg.MaxTurns = func() int { return 25 }
	}
	return &ChatRunner{
		cfg:        cfg,
		subs:       map[string]*chatSub{},
		pausedSubs: map[string]*pausedTurn{},
	}, nil
}

// StartStream opens one chat run on the kernel's chat graph and
// returns a subscription id. The runner spawns a goroutine that drives
// the kernel; events fan onto the broker's "llm:stream-chunk" /
// "llm:stream-closed" topics. The userMessage is the new turn — the
// runner appends it to the session via cfg.HistoryWriter before
// firing the kernel so HistoryReadNode picks it up at run start.
func (r *ChatRunner) StartStream(ctx context.Context, profileID, sessionID, modelOverride, userMessage string) (string, error) {
	if r == nil {
		return "", errors.New("chat: nil runner")
	}
	if profileID == "" {
		return "", errors.New("chat: profile id required")
	}
	if sessionID == "" {
		return "", errors.New("chat: session id required")
	}

	// Persist the user turn so HistoryReadNode sees it on the first
	// fire of the kernel run.
	if r.cfg.HistoryWriter != nil && userMessage != "" {
		if _, werr := r.cfg.HistoryWriter.AppendMessage(ctx, sessionID, "user", userMessage); werr != nil {
			return "", fmt.Errorf("chat: persist user turn: %w", werr)
		}
	}

	// Pre-send compaction hook (mission compaction-strategy-ui-01KQ8TDI
	// WP08 / plan §2.3). Runs between user-turn persistence and kernel
	// run so HistoryReadNode picks up the post-compaction transcript on
	// the first fire. On Mode==None over cap or model-too-small in
	// graceful-degrade fallback we return compaction.ErrSessionFull so
	// the chat surface renders the honest "session full" copy.
	if cerr := r.runPreSendCompaction(ctx, profileID, sessionID, modelOverride, userMessage); cerr != nil {
		return "", cerr
	}

	// Resolve the chat graph and apply the per-run MaxAgentTurns dial
	// onto the LoopNode max_iterations.
	graph, err := r.cfg.GraphLoader()
	if err != nil {
		return "", fmt.Errorf("chat: graph load: %w", err)
	}
	maxTurns := r.cfg.MaxTurns()
	if maxTurns <= 0 {
		maxTurns = 25
	}
	applyMaxTurnsDial(&graph, maxTurns)

	// Construct adapters for this run's LLM provider + tool registry.
	// Tool discovery runs once per StartStream so the model sees the
	// live MCP + builtin catalog (gated by Settings toggles for
	// websearch/bash). Discovery failure is non-fatal — the model
	// just won't see any tools for this turn.
	var toolCatalog []corellm.ToolSpec
	if r.cfg.ToolDiscoverer != nil {
		discovered, derr := r.cfg.ToolDiscoverer.Tools(ctx, sessionID)
		if derr != nil {
			logging.L().Warn("chat.tool_discovery.failed",
				"session_id", sessionID, "err", derr.Error())
		} else {
			toolCatalog = discovered
			names := make([]string, 0, len(toolCatalog))
			for _, t := range toolCatalog {
				names = append(names, t.Name)
			}
			logging.L().Info("chat.tool_discovery.ok",
				"session_id", sessionID,
				"count", len(toolCatalog),
				"tools", names)
		}
	} else {
		logging.L().Warn("chat.tool_discovery.no_discoverer", "session_id", sessionID)
	}
	llmAdapter := NewLLMProviderAdapter(r.cfg.Registry, profileID, modelOverride, toolCatalog)
	toolAdapter := newKernelToolAdapter(r.cfg.Pool, r.cfg.Perms, sessionID)
	if r.cfg.AutonomyKnobs != nil {
		toolAdapter.withAutonomy(r.cfg.AutonomyKnobs)
	}

	r.mu.Lock()
	r.nextID++
	subID := fmt.Sprintf("chat-%d", r.nextID)
	r.mu.Unlock()

	bridge := NewStreamBridge(r.cfg.Broker, subID, sessionID)

	streamCtx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-streamCtx.Done():
		}
	}()

	// Pre-seed the AskBus with the user's message so the chat graph's
	// `ask_user` AskNode resolves on its first fire. The chat graph is
	// shaped to pause-then-progress: history_in → ask_user (resolves
	// immediately to the user message) → assistant_turn → ... and the
	// run pauses on the next ask_user fire if the LoopNode body re-
	// enters the AskNode for a follow-up turn.
	askBus := coreag.NewMemAskBus()
	askBus.Answer(subID, chatAskNodeID, userMessage)

	env := &coreag.Env{
		RunID:         subID,
		SessionID:     sessionID,
		Graph:         &graph,
		LLM:           llmAdapter,
		Tools:         toolAdapter,
		HistoryWriter: r.cfg.HistoryWriter,
		StreamSink:    bridge,
		Ask:           askBus,
	}
	if r.cfg.History != nil {
		env.History = historyAdapterFunc(r.cfg.History.History)
	}
	if r.cfg.EnvDefaults != nil {
		r.cfg.EnvDefaults(env)
	}

	// Register the per-turn usage hook via HookPostLLM so it fires
	// AFTER session_write has persisted the assistant message (and
	// thus has a valid messageID). The adapter stores the most recent
	// llm.Response so the hook can record token counts + cost
	// (token-cost-telemetry-01KQ8TD7 WP02).
	//
	// WP06 (backend-context-window-length-01KQ8TD3): also resolve the
	// provider kind and model id from the adapter so the usage hook can
	// populate UsageTurn.ProviderKind / UsageTurn.ModelID for full
	// token-cost-telemetry alignment.
	if r.cfg.UsageHook != nil {
		if env.Hooks == nil {
			env.Hooks = coreag.NewHookManager(env.Memory, env.SessionID, env.ProjectID)
		}
		usageHook := r.cfg.UsageHook
		capturedAdapter := llmAdapter
		capturedSessionID := sessionID
		env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(ctx context.Context, sID, messageID, _ string) {
			resp := capturedAdapter.LastResponse()
			providerKind := capturedAdapter.ProviderKind()
			modelID := capturedAdapter.ActiveModelID()
			usageHook(ctx, capturedSessionID, messageID, providerKind, modelID, resp)
		})
	}

	sub := &chatSub{
		id:            subID,
		sessionID:     sessionID,
		profileID:     profileID,
		modelOverride: modelOverride,
		cancel:        cancel,
		done:          make(chan struct{}),
		bridge:        bridge,
	}
	r.mu.Lock()
	r.subs[subID] = sub
	r.mu.Unlock()

	go r.driveRun(streamCtx, sub, env)
	return subID, nil
}

// StopStream cancels the run for subID. The driver goroutine emits
// llm:stream-closed with reason=stop-called and exits.
func (r *ChatRunner) StopStream(_ context.Context, subID string) error {
	r.mu.Lock()
	sub, ok := r.subs[subID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("chat: subscription %q not found", subID)
	}
	sub.cancel()
	<-sub.done
	return nil
}

// HasPausedSubFor reports whether a paused turn exists for the given profileID.
// When ok is true, token is the sub_id originally assigned to the paused turn.
// Used by the LLM view's TestAndRotateKey to decide whether to mint an
// auto-resume token (provider-keychain-rotation-01KQ8TD9 WP03).
func (r *ChatRunner) HasPausedSubFor(profileID string) (token string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pt, ok := r.pausedSubs[profileID]
	if !ok {
		return "", false
	}
	return pt.subID, true
}

// RedriveLastTurn re-issues a StartStream for the captured (profileID,
// sessionID, modelOverride) of a paused turn WITHOUT appending a new user
// message — the user turn is already in session history courtesy of the
// original StartStream. This is the auto-resume seam called by the LLM
// view after a successful TestAndRotateKey.
//
// Returns the new sub_id on success. The paused entry is removed from
// pausedSubs regardless of the outcome so a failed redrive does not block
// a subsequent manual resend.
//
// provider-keychain-rotation-01KQ8TD9 WP03.
func (r *ChatRunner) RedriveLastTurn(ctx context.Context, profileID string) (newSubID string, err error) {
	r.mu.Lock()
	pt, ok := r.pausedSubs[profileID]
	if ok {
		delete(r.pausedSubs, profileID)
	}
	r.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("chat: no paused turn for profile %q", profileID)
	}
	// StartStream with empty userMessage skips the HistoryWriter.AppendMessage
	// call so the user turn is not double-appended (the HistoryWriter guard
	// in StartStream checks `userMessage != ""`).
	newSubID, err = r.StartStream(ctx, pt.profileID, pt.sessionID, pt.modelOverride, "")
	if err != nil {
		return "", err
	}
	// Notify subscribers (chat surface, toast queues) that the paused turn
	// has been resumed so any stale auth-failure UI state is cleared. The
	// AuthFailureToast and RetryAfterRotateToast both subscribe to this
	// topic and pop their head entry on receipt; useSession clears its
	// streamingError when the profile matches.
	if r.cfg.Broker != nil {
		r.cfg.Broker.Emit("provider:auth-resumed", AuthResumedPayload{
			ProfileID: pt.profileID,
			NewSubID:  newSubID,
		})
	}
	return newSubID, nil
}

// driveRun runs the kernel and emits the terminal close payload. We
// unconditionally fire EmitClosed once the run exits so the chat
// surface always sees a close signal — the bridge's Close() is
// idempotent so a kernel-side Close that already fired is a no-op.
func (r *ChatRunner) driveRun(ctx context.Context, sub *chatSub, env *coreag.Env) {
	log := logging.L()
	defer func() {
		r.mu.Lock()
		delete(r.subs, sub.id)
		r.mu.Unlock()
		close(sub.done)
	}()

	err := r.cfg.Kernel.Run(ctx, env)
	reason := "completed"
	message := ""
	finishReason := ""
	runTerminatedClean := false // true when kernel finished without error (or ErrPaused)

	// provider-keychain-rotation-01KQ8TD9 WP03: auth-failure interception.
	// When the kernel run exits with *ErrProviderAuthFailed AND the key-rotation
	// feature flag is on, we pause the chat surface in "needs_key_rotation" state
	// rather than emitting a backend-error close. The broker topic drives the
	// frontend toast; the paused turn is stored so RedriveLastTurn can resume it
	// after a successful key rotation.
	var authFailed *corellm.ErrProviderAuthFailed
	if errors.As(err, &authFailed) && keychainRotationEnabled() {
		pt := &pausedTurn{
			subID:         sub.id,
			sessionID:     sub.sessionID,
			profileID:     authFailed.ProfileID,
			modelOverride: sub.modelOverride,
			pausedAt:      time.Now(),
		}
		r.mu.Lock()
		r.pausedSubs[authFailed.ProfileID] = pt
		r.mu.Unlock()

		// Emit the synthetic "paused for auth" chunk so the frontend's chat
		// bubble marks the turn as incomplete.
		sub.bridge.Emit(coreag.StreamEvent{
			Kind:   coreag.StreamEventError,
			ErrMsg: "auth failure — paused for key rotation",
		})

		// Broadcast the auth-failure topic so the toast component can mount.
		r.cfg.Broker.Emit("provider:auth-failed", AuthFailedPayload{
			SubID:     sub.id,
			SessionID: sub.sessionID,
			ProfileID: authFailed.ProfileID,
			Provider:  authFailed.Provider,
			Model:     authFailed.ModelID,
			Reason:    authFailed.Reason,
		})

		// Do NOT call EmitClosed — the stream stays open from the frontend's
		// perspective until RedriveLastTurn completes or the user dismisses.
		if !sub.finished.CompareAndSwap(false, true) {
			return
		}
		log.Info("chat.run.auth_failure_paused",
			"sub_id", sub.id,
			"session_id", sub.sessionID,
			"profile_id", authFailed.ProfileID,
		)
		return
	}

	switch {
	case err == nil:
		reason = "completed"
		runTerminatedClean = true
	case errors.Is(err, coreag.ErrPaused):
		// AskNode paused the run — chat surface treats this as the end
		// of one turn; the next user message starts a fresh run. The
		// stream-closed payload still fires so the frontend knows to
		// stop the typing indicator.
		reason = "completed"
		finishReason = "paused"
		runTerminatedClean = true
	case errors.Is(err, coreag.ErrBudgetExceeded):
		reason = "backend-error"
		message = "agent reached the per-run budget cap"
	case errors.Is(err, context.Canceled):
		reason = "stop-called"
	default:
		reason = "backend-error"
		message = err.Error()
	}

	// long-turn-resilience-01KR3PRS WP03: when the kernel exited with
	// a backend-error AND the StreamBridge accumulated text deltas
	// before the drop, persist a partial assistant row through
	// PartialPersister so the resume RPC can land a continuation. The
	// frontend receives the persisted message id on the closed payload
	// and renders the Resume affordance against it.
	//
	// Skip persistence on every other terminal path:
	//   - reason=="completed": the kernel ran cleanly; SessionWriteNode
	//     already persisted the assistant row.
	//   - reason=="stop-called": user-initiated stop; partial-persist
	//     would surface a stale Resume button with no recovery upside.
	//   - no PartialPersister wired: degrade to the WP00 frontend-only
	//     fallback (the partial bubble's streamingError sub-line).
	var (
		partialMessageID   string
		partialFailureKind string
		partialRecoverable bool
	)
	if reason == "backend-error" && r.cfg.PartialPersister != nil {
		partialText, hasTool := sub.bridge.PartialState()
		if partialText != "" {
			partialFailureKind = classifyPartialFailureKind(message)
			partialRecoverable = !hasTool
			// Use a fresh background context so a cancelled streamCtx
			// does not abort the persist call. The chat-runner owns
			// per-session serialization, so a 5s budget is safe.
			persistCtx, cancel := context.WithTimeout(context.Background(), persistPartialTimeout)
			mid, perr := r.cfg.PartialPersister.PersistPartial(persistCtx, sub.sessionID,
				partialText, partialFailureKind, partialRecoverable)
			cancel()
			if perr != nil {
				logging.L().Warn("chat.partial_persist.failed",
					"sub_id", sub.id,
					"session_id", sub.sessionID,
					"err", perr.Error())
			} else {
				partialMessageID = mid
				logging.L().Info("chat.partial_persist.ok",
					"sub_id", sub.id,
					"session_id", sub.sessionID,
					"message_id", mid,
					"failure_kind", partialFailureKind,
					"recoverable", partialRecoverable,
					"bytes", len(partialText),
				)
			}
		}
	}

	// Post-run auto-title trigger (session-auto-titling-01KQ8TDS WP04).
	// Fires asynchronously so it never blocks the stream-closed
	// emission. Conditions:
	//   (1) run terminated cleanly (no error or ErrPaused),
	//   (2) AutoTitle deps are wired.
	// fireAutoTitle re-reads the session store to verify auto_titled is
	// still false, the name still matches the placeholder pattern, and
	// at least one assistant message exists — all inside a fresh
	// 5-second context (NFR-001).
	if runTerminatedClean && r.cfg.AutoTitle != nil {
		go r.fireAutoTitle(sub.sessionID, sub.profileID, sub.modelOverride)
	}

	if !sub.finished.CompareAndSwap(false, true) {
		return
	}
	sub.bridge.EmitClosedPartial(reason, message, finishReason,
		partialMessageID, partialFailureKind, partialRecoverable)
	log.Info("chat.run.complete",
		"sub_id", sub.id,
		"session_id", sub.sessionID,
		"reason", reason,
		"err", message,
	)
}

// persistPartialTimeout caps the partial-persist call so a slow
// session_messages write cannot block the chat-runner terminal goroutine
// indefinitely. 5s is generous — the write is a single UPDATE.
const persistPartialTimeout = 5 * time.Second

// classifyPartialFailureKind returns the failure_kind string the
// frontend persists alongside the partial row. The classifier only sees
// the wrapped error message string at this point (the typed
// llm.ErrTransient/ErrAuth wraps live deeper inside the kernel run, and
// the runner's surface only carries err.Error()), so it does a
// substring sniff over the canonical wrapping prefixes the chat path
// uses (see core/llm/errors.go for the source taxonomy).
//
// long-turn-resilience-01KR3PRS WP03.
func classifyPartialFailureKind(msg string) string {
	switch {
	case msg == "":
		return "unknown"
	case containsAny(msg, "auth", "401", "403", "unauthorized"):
		return "auth"
	case containsAny(msg, "transient", "stream", "network", "connection", "5"):
		// Catch-all for ErrTransient wrapping; the leading "5" handles
		// 5xx HTTP codes mentioned in the wrap. The classifier is
		// best-effort — frontend uses this only to tailor the failure
		// copy, never to gate the resume button.
		return "transient"
	default:
		return "unknown"
	}
}

// containsAny reports whether s contains any of the provided substrings.
// Local helper so the classifier doesn't need a heavyweight import.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if substr(s, sub) {
			return true
		}
	}
	return false
}

// substr is a tiny case-insensitive substring check.
func substr(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	// Fast lowercase comparison — sufficient for the small set of
	// substrings classifyPartialFailureKind uses (no Unicode quirks).
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			cs, csub := s[i+j], sub[j]
			if cs >= 'A' && cs <= 'Z' {
				cs += 'a' - 'A'
			}
			if csub >= 'A' && csub <= 'Z' {
				csub += 'a' - 'A'
			}
			if cs != csub {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// applyMaxTurnsDial walks every LoopNode in the graph and overrides
// its `max_iterations` attr with the supplied cap. The kernel reads
// the per-run override directly from the resolved attrs at fire time.
func applyMaxTurnsDial(g *coreag.Graph, cap int) {
	if g == nil {
		return
	}
	for i := range g.Nodes {
		if g.Nodes[i].Kind != coreag.NodeKindLoop {
			continue
		}
		if a, ok := g.Nodes[i].Attrs.(coreag.LoopAttrs); ok {
			a.MaxIterations = cap
			g.Nodes[i].Attrs = a
		}
	}
}

// runPreSendCompaction is the pre-send hook from plan §2.3 / WP08.
// Returns compaction.ErrSessionFull when the user is honestly out of
// context (off tier + over cap, or maximal-fallback ran out of room);
// returns nil on every other path including soft failures (we log,
// don't crash the chat). The userMessage is INCLUDED in the
// token-count input but is NOT mutated.
//
// Concurrency: per-session serialization is the chat runner's
// invariant (one StartStream goroutine per session at a time); the
// engine relies on it to keep ListActiveMessages → snap → write race-
// free. Multi-session traffic runs in parallel safely.
func (r *ChatRunner) runPreSendCompaction(ctx context.Context, profileID, sessionID, modelOverride, userMessage string) error {
	deps := r.cfg.Compaction
	if deps == nil || deps.Engine == nil {
		// Compaction not wired — fall through to the kernel. This is
		// the test-fixture path and the boot path on a chassis where
		// compaction failed to construct.
		return nil
	}
	// HARNESS_COMPACTION=off short-circuits the hook entirely so the
	// user can A/B test without restarting (WP08 acceptance).
	if compactionDisabledByEnv() {
		return nil
	}
	if deps.Aggressiveness == nil {
		// Defensive: a chassis that wired the engine but not the
		// settings reader can't make a tier decision.
		return nil
	}

	tier := compaction.Tier(deps.Aggressiveness())

	// Helper: load the current active history. Re-fetched after
	// compaction so the kernel run sees the post-compaction transcript.
	loadHistory := func() []coreag.Message {
		if r.cfg.History == nil {
			return nil
		}
		msgs, herr := r.cfg.History.History(ctx, sessionID, 0)
		if herr != nil {
			logging.L().Warn("chat.compaction.history_load_failed",
				"session_id", sessionID, "err", herr.Error())
			return nil
		}
		return msgs
	}

	// Helper: pick the compaction model. Configured ref wins; fallback
	// is the active chat model (treating profileID as the providerID
	// and modelOverride as the modelID — the fallback convention also
	// used by core/compaction/wiring/llm.go's resolveProfile).
	pickModel := func() compaction.ProviderProfileRef {
		if deps.CompactionModel != nil {
			if ref, ok := deps.CompactionModel(); ok && (ref.ProviderID != "" || ref.ModelID != "") {
				return ref
			}
		}
		return compaction.ProviderProfileRef{ProviderID: profileID, ModelID: modelOverride}
	}

	switch tier.Mode {
	case compaction.ModeNone:
		// Off tier: honest "session full" if we'd exceed cap, otherwise
		// proceed without compaction.
		if deps.MaxContextTokens == nil {
			return nil
		}
		history := loadHistory()
		current := tokenizeRequest(history, userMessage)
		activeModel := compaction.ProviderProfileRef{ProviderID: profileID, ModelID: modelOverride}
		if cap, ok := deps.MaxContextTokens(activeModel); ok && cap > 0 && current >= cap {
			logging.L().Warn("chat.compaction.session_full_off",
				"session_id", sessionID,
				"tokens", current, "cap", cap)
			return compaction.ErrSessionFull
		}
		return nil

	case compaction.ModeThreshold:
		if deps.MaxContextTokens == nil {
			return nil
		}
		history := loadHistory()
		current := tokenizeRequest(history, userMessage)
		activeModel := compaction.ProviderProfileRef{ProviderID: profileID, ModelID: modelOverride}
		cap, ok := deps.MaxContextTokens(activeModel)
		if !ok || cap <= 0 {
			// Unknown model cap — skip the trigger check; provider's own
			// gate handles any over-cap span.
			return nil
		}
		if float64(current)/float64(cap) < tier.TriggerPct {
			return nil
		}
		// Trigger! Run a synchronous Compact pass.
		_, cerr := deps.Engine.Compact(ctx, sessionID, pickModel(), tier.SummarizePct)
		if cerr != nil {
			var tooSmall *compaction.ErrCompactionModelTooSmall
			if errors.As(cerr, &tooSmall) {
				// Threshold-mode model-too-small: surface session-full
				// upward so the UI renders the actionable copy.
				logging.L().Warn("chat.compaction.threshold_model_too_small",
					"session_id", sessionID,
					"needs_tokens", tooSmall.NeedsTokens,
					"model_max_tokens", tooSmall.ModelMaxTokens)
				return compaction.ErrSessionFull
			}
			// Other errors: log + proceed without compaction. Partial
			// state is okay because Compact is transactional — either
			// the summary row exists or the originals stayed untouched.
			logging.L().Warn("chat.compaction.threshold_failed",
				"session_id", sessionID, "err", cerr.Error())
			return nil
		}
		// Compact OK. The kernel's HistoryReadNode will pick up the
		// post-compaction transcript on its first fire — the chat
		// runner doesn't need to plumb a fresh history into the kernel
		// run (the kernel always reads through env.History).
		return nil

	case compaction.ModeRolling:
		// Maximal tier: roll every turn.
		recentWindow := 4
		if deps.RecentWindow != nil {
			recentWindow = deps.RecentWindow()
		}
		_, cerr := deps.Engine.RollingSummarize(ctx, sessionID, pickModel(), recentWindow)
		if cerr == nil {
			return nil
		}
		var tooSmall *compaction.ErrCompactionModelTooSmall
		if errors.As(cerr, &tooSmall) {
			// Graceful degrade per plan §2.5 R2: silently treat this
			// turn as the aggressive tier. Run a single Compact pass
			// using the aggressive numerics; the audit emit inside
			// Engine.RollingSummarize already recorded the failure
			// breadcrumb so dashboards can see the fallback.
			logging.L().Warn("chat.compaction.maximal_too_small_fallback_aggressive",
				"session_id", sessionID,
				"needs_tokens", tooSmall.NeedsTokens,
				"model_max_tokens", tooSmall.ModelMaxTokens)
			fallback := compaction.Tier(compaction.AggressivenessAggressive)
			_, fcerr := deps.Engine.Compact(ctx, sessionID, pickModel(), fallback.SummarizePct)
			if fcerr != nil {
				var fts *compaction.ErrCompactionModelTooSmall
				if errors.As(fcerr, &fts) {
					return compaction.ErrSessionFull
				}
				logging.L().Warn("chat.compaction.maximal_fallback_failed",
					"session_id", sessionID, "err", fcerr.Error())
			}
			return nil
		}
		// Other rolling errors: log + proceed without compaction (R1
		// of the risk register: provider hiccup shouldn't block chat
		// when the session isn't yet over cap).
		logging.L().Warn("chat.compaction.rolling_failed",
			"session_id", sessionID, "err", cerr.Error())
		return nil
	}
	return nil
}

// historyAdapterFunc adapts a closure to the agentgraph.HistoryReader
// interface. Mirrors HistoryReaderFunc but without the kernel-side
// type alias collision.
type historyAdapterFunc func(ctx context.Context, sessionID string, n int) ([]coreag.Message, error)

// History satisfies agentgraph.HistoryReader.
func (f historyAdapterFunc) History(ctx context.Context, sessionID string, n int) ([]coreag.Message, error) {
	return f(ctx, sessionID, n)
}
