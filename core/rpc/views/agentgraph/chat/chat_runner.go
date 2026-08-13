package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	artview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
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

// multimodalOutDisabledByEnv reports whether HARNESS_MULTIMODAL_OUT=off is
// set. When true, the generated-image capture pipeline is bypassed: the
// capturer is nil-gated so StreamGeneratedImage events are silently
// discarded regardless of the Settings.AutoCaptureGeneratedImages dial.
// (multimodal-io-extended-01KQ8TD2 WP08)
func multimodalOutDisabledByEnv() bool {
	v := os.Getenv("HARNESS_MULTIMODAL_OUT")
	return v == "off" || v == "0" || v == "false"
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

// ReasoningBudgetResolver returns the extended-thinking token budget to
// apply onto the chat graph's model nodes. The chassis wires this to
// Settings.EffectiveReasoningBudgetTokens so a settings change takes
// effect on the next user turn without a restart.
//
// Zero (the default, and what a nil resolver yields) means "reasoning
// off" and leaves the graph's own attr untouched — see
// applyReasoningBudgetDial.
type ReasoningBudgetResolver func() int

// DefaultMaxOverflowRecoveriesPerTurn is the number of automatic
// compact-and-redrive rescues a single turn gets when nothing configures
// otherwise.
//
// It is 1 deliberately: that is exactly what the hardcoded
// sub.overflowRetried one-shot did, so turn-context-runway-01PMAG03
// WP03 changes the *shape* of the limit (a budget) without changing the
// default behaviour of any existing install. spec.md §3.3 proposes 2;
// raising it is now a one-line resolver change rather than a control-flow
// rewrite, and belongs with the measurement pass plan.md asks for.
const DefaultMaxOverflowRecoveriesPerTurn = 1

// MaxOverflowRecoveriesResolver returns the per-turn budget for
// automatic context-overflow recoveries. Resolved on each terminal
// evaluation so a change lands without a restart — the same
// runtime-dial shape as MaxTurnsResolver.
//
// nil selects DefaultMaxOverflowRecoveriesPerTurn. plan.md open
// question 3 proposes promoting this to an autonomy knob so it inherits
// the layer chain; that is a matter of supplying a different resolver
// here, with no call-site change.
type MaxOverflowRecoveriesResolver func() int

// CompactionWatermarkResolver returns the growth-watermark policy to
// arm on each run's Env (turn-context-runway-01PMAG03 WP02). Resolved
// per StartStream, so a change lands on the next turn without a
// restart — the same runtime-dial shape as MaxTurnsResolver.
//
// nil (and a zero-value policy) selects the agentgraph package
// defaults. Widening this to the Settings UI or the autonomy-knob layer
// chain (plan.md open question 1) is a matter of supplying a different
// resolver here; no call site changes.
type CompactionWatermarkResolver func() coreag.CompactionWatermarkPolicy

// GraphLoader returns the parsed chat graph spec to drive each run.
// In production this loads the bundled chat_default.yaml; tests can
// substitute an arbitrary graph.
type GraphLoader func() (coreag.Graph, error)

// RunSpecRecorder publishes the RESOLVED graph a chat turn is about to
// execute, keyed by run id (agentgraph-total-convergence-01PMGX01 WP12).
//
// A chat turn builds its own Env and calls the shared kernel directly,
// so its events reach the manager's EventLog while its spec does not
// reach the manager's run registry. Without this seam the run is
// materializable only via the library file — which is the WRONG spec:
// the routing gate rewrites the topology and the max-turns dial
// overrides the loop cap before the run starts, so the file on disk is
// not what executed. Nil is safe: chat still runs, its turns just
// cannot be shown as graphs.
type RunSpecRecorder func(runID string, g coreag.Graph)

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
	// RunSpecRecorder registers each turn's resolved spec so the run can
	// be materialized as a graph afterwards (WP12). Nil disables it.
	RunSpecRecorder RunSpecRecorder
	// ReasoningBudget resolves the extended-thinking budget per run.
	// Nil is safe and means "reasoning off" (today's behaviour).
	ReasoningBudget ReasoningBudgetResolver
	// CompactionWatermark resolves the mid-run compaction trigger policy
	// per run (turn-context-runway-01PMAG03 WP02). Nil selects the
	// agentgraph package defaults.
	CompactionWatermark CompactionWatermarkResolver
	// MaxOverflowRecoveries resolves the per-turn budget for automatic
	// context-overflow recoveries (turn-context-runway-01PMAG03 WP03).
	// Nil selects DefaultMaxOverflowRecoveriesPerTurn, which preserves
	// the pre-mission one-shot behaviour exactly.
	MaxOverflowRecoveries MaxOverflowRecoveriesResolver
	// ToolDiscoverer publishes the chat-runner-level tool catalog onto
	// each LLMProviderAdapter so the model sees the live MCP+builtin
	// tool list. nil disables discovery — the chat path still works,
	// but the model is never told about any tools.
	ToolDiscoverer ToolCatalogDiscoverer
	// EnvDefaults is an optional callback the runner invokes on the
	// constructed Env before kernel.Run; production wiring threads
	// Memory / Policy / Branch / Hooks-journal seams through it.
	EnvDefaults func(env *coreag.Env)
	// Compaction bundles the collaborators the five-tier compaction
	// dial needs (mission compaction-strategy-ui-01KQ8TDI WP08). nil
	// disables the dial: the `compact` node still runs, resolves to a
	// strategy with no engine behind it, and passes its input through
	// untouched. Production builds wire this; tests that don't exercise
	// compaction leave it nil.
	Compaction *CompactionDeps

	// CompactionPipeline is the shared FR-041 compaction pipeline the
	// chassis constructed (core/rpc/api.go). The runner binds this run's
	// session-rewrite strategy onto it per StartStream and installs the
	// result as env.Compactor, so the `compact` node in chat_default.yaml
	// has something to dispatch through.
	//
	// nil leaves env.Compactor nil, which makes every compaction node a
	// documented passthrough — the correct behaviour for a chassis with
	// no compaction wired, and for the many tests that construct a bare
	// runner (agentgraph-total-convergence-01PMGX01 WP08).
	CompactionPipeline *compaction.Pipeline

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
	// (v0.3.0 baseline behaviour).
	//
	// KNOWN GAP (confirm-each-enforcement-01PMAG05): the doc line that
	// used to sit here claimed "production wiring threads this from the
	// session's resolved autonomy knobs at StartStream time". It does
	// not — grep `AutonomyKnobs:` outside _test.go and there is no
	// production call site. Every shipped build therefore leaves this
	// nil, so the prompt-skip set never suppresses a confirmation and
	// confirm_each always prompts.
	//
	// That is the SAFE direction, and it is why this was left rather
	// than half-wired: the provider has no session parameter, so the
	// only thing wireable here without a signature change is the global
	// autonomy layer, and a global-only resolution would silently
	// ignore per-project and per-session postures — a knob that lies
	// about its scope is worse than a knob that is off. Wiring belongs
	// with autonomy-knobs-live-01PMAG02 WP05, which owns the posture
	// semantics; the mechanism (WP04) and its tests are complete and
	// exercised at the adapter seam.
	AutonomyKnobs AutonomyKnobsProvider

	// Confirm is the confirm-each pause registry
	// (confirm-each-enforcement-01PMAG05 WP02). It MUST be the same
	// *toolloop.ConfirmBus instance the confirm RPC view resolves
	// against — the runner's tool adapter parks calls on it and the
	// frontend's answers arrive through the view, so two instances mean
	// answers that land on a registry nothing is waiting on.
	//
	// nil means "no prompt channel": a confirm_each verdict falls to
	// ConfirmDeps.Headless, whose default is deny. Never a silent allow.
	Confirm *toolloop.ConfirmBus

	// ConfirmDeps carries the rest of the confirm-each decision path:
	// the Settings toggle (FR-006), the session + persistent grant
	// layers (WP03), the headless policy (WP05), and the audit emitter
	// (FR-007). The zero value is the conservative configuration —
	// always prompt, no grants, deny when headless, silent audit.
	ConfirmDeps ConfirmDeps

	// Clock is the optional injected time source for the environment-
	// context layer of the system prompt (system-prompt-layers WP03).
	// nil falls back to time.Now inside the adapter; production leaves
	// this nil and tests pin it for deterministic dates.
	Clock func() time.Time

	// WorkspaceDir is the absolute agent-workspace path surfaced in the
	// environment-context layer (system-prompt-layers WP03). Empty renders
	// a generic "sandboxed workspace" note instead of a concrete path.
	WorkspaceDir string

	// WorkspaceNote is the honest trailing description for the workspace
	// line (spec 089 FR-4): granted mount vs private sandbox vs fallback.
	// Empty keeps the generic pre-089 wording.
	WorkspaceNote string

	// CustomInstructions returns the user's chat custom-instructions text,
	// read on every StartStream so a Settings edit takes effect on the
	// next turn (system-prompt-layers WP04). nil / empty appends no user
	// layer to the system prompt.
	CustomInstructions func() string

	// GeneratedImageCapturer is the optional auto-capture pipeline for
	// model-generated images (multimodal-io-extended-01KQ8TD2 WP02).
	// When non-nil, the LLMProviderAdapter calls OnGeneratedImage for
	// every StreamGeneratedImage event received during the event drain
	// loop so captured images land in the artifact store with
	// Source=="model_output". nil disables image auto-capture entirely
	// (no error returned; the stream event is still forwarded to the
	// kernel sink for frontend rendering).
	GeneratedImageCapturer artview.GeneratedImageCapturer
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
	Engine compaction.SessionEngine

	// Aggressiveness returns the current effective tier. Read on every
	// send so a Settings change (UI dial) takes effect on the next turn
	// without restarting the harness.
	Aggressiveness func() compactionpolicy.CompactionAggressiveness

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
	// adapter from core/agentgraph/compaction/wiring provides a curated builtin
	// table covering the major providers.
	MaxContextTokens func(model compaction.ProviderProfileRef) (int, bool)
}

// ToolPool is the narrow MCP-pool surface the runner consumes. It is
// structurally identical to toolloop.MCPPool so the chassis can pass
// the existing wrapped pool without an additional adapter step, but it
// is declared here rather than aliased.
//
// That independence is now permanent, not transitional: this package
// still imports core/toolloop (for ConfirmBus, SessionGrantCache and
// the permission verdict) and will keep doing so, because tool-call
// POLICY lives there. Re-declaring the pool SHAPE keeps runner
// construction decoupled from that package's wire types. (The comment
// here used to promise "WP07 can drop the toolloop import from this
// package"; WP07 landed and the import remains, correctly. Corrected
// under 01PMGX01 invariant I8, 2026-08-13.)
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
// gate tool dispatch. Structurally identical to
// toolloop.PermissionResolver; the runner accepts the resolver shape
// directly and applies the verdict in kernelToolAdapter.Call.
//
// (This used to say "WP06 lifts this into the kernel's PolicyGate
// seam". WP06 landed and did not: env.Policy gates FILE and shell
// access at the State-kind executors, while the tool-call verdict is
// applied by the adapter, because a confirm_each verdict has to park
// on a ConfirmBus that the kernel has no seam for. Corrected under
// 01PMGX01 invariant I8, 2026-08-13.)
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

// ChatRunner is the kernel-driven entry point and the ONLY chassis
// chat path — it replaced the pre-kernel chassis loop, which was
// deleted rather than kept as a fallback. One runner per process; the
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
	// overflowRecoveries counts the automatic overflow-recovery redrives
	// this run has spent, against the MaxOverflowRecoveriesPerTurn
	// budget (turn-context-runway-01PMAG03 WP03).
	//
	// This replaced an atomic.Bool one-shot. The boolean encoded "at
	// most one rescue per turn" as a fact about the control flow rather
	// than as a policy, which made the autonomous-run lifecycle *grow
	// until overflow → one rescue → grow again → die*. The counter makes
	// the same default expressible (budget 1) and dial-able upward.
	overflowRecoveries atomic.Int64
	// cancelCause records WHY the run's context was cancelled, so the
	// terminal close can distinguish an explicit user Stop ("stop-called")
	// from the inbound RPC ctx being cancelled out from under us
	// ("inbound-ctx" — the signature of a desktop focus-loss / webview
	// suspend killing the run). Empty until something cancels.
	cancelCause atomic.Value // string
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
	if cfg.CompactionWatermark == nil {
		// Nil resolver = package defaults. Unlike MaxTurns there is no
		// loud-fail default worth substituting: the agentgraph
		// zero-value policy IS the intended production margin.
		cfg.CompactionWatermark = func() coreag.CompactionWatermarkPolicy {
			return coreag.CompactionWatermarkPolicy{}
		}
	}
	if cfg.ReasoningBudget == nil {
		// Nil resolver = reasoning off. Unlike MaxTurns there is no
		// "loud-fail" default worth substituting: 0 is the correct,
		// intended value and matches every pre-01PMAG04 run.
		cfg.ReasoningBudget = func() int { return 0 }
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

	// Compaction is no longer a pre-send pass here. It is a `compact`
	// node in chat_default.yaml, placed between history_read and the
	// agent loop, dispatching through env.Compactor
	// (agentgraph-total-convergence-01PMGX01 WP08 / spec §4.4). The
	// session-full condition therefore reaches the user on the
	// stream-closed payload rather than as a synchronous StartStream
	// error — see the compaction.ErrSessionFull case in the kernel-exit
	// switch below, and the same delivery the overflow-recovery path
	// has always used.

	// Resolve the chat graph and apply the per-run MaxAgentTurns dial
	// onto the LoopNode max_iterations.
	graph, err := r.cfg.GraphLoader()
	if err != nil {
		return "", fmt.Errorf("chat: graph load: %w", err)
	}

	// autonomy-knobs-live-01PMAG02 fix F8: resolve the autonomy knobs
	// exactly ONCE per StartStream, with the run's real ctx (not
	// context.Background()), and thread the resulting value everywhere
	// below instead of re-invoking r.cfg.AutonomyKnobs. The provider
	// does real I/O (global/project/session store reads); before this
	// fix it was invoked from ~6 separate call sites in this function
	// plus once per Generate (WithRecapStyle/WithAskOnAmbiguity) and
	// once per tool call (kernelToolAdapter.Call) — dozens of redundant
	// store reads on a single chatty turn. resolvedKnobs is the zero
	// ResolvedKnobs{} when no provider is wired, matching every
	// consumer's existing nil-safe fallback.
	resolvedKnobs := r.autonomyKnobs(ctx, sessionID)

	maxTurns := r.cfg.MaxTurns()
	if maxTurns <= 0 {
		maxTurns = 25
	}
	// autonomy-knobs-live-01PMAG02 WP01: the resolved MaxIterations knob
	// (session → project → global(=Settings) → preset default,
	// autonomy.Resolve's precedence) is now the single source of truth
	// for the per-run cap — see spec §1.1 "the maxIterations collision".
	// cfg.MaxTurns() above stays the legacy Settings-only value and is
	// also what production wiring feeds into the resolved chain's global
	// layer (core/rpc/api.go), so a session with no project/session
	// override resolves to the identical number either way (FR-005).
	// cfg.MaxTurns() remains the value used when no AutonomyKnobs
	// provider is wired at all (tests, or a chassis that hasn't migrated).
	if r.cfg.AutonomyKnobs != nil {
		if resolved := resolvedKnobs.MaxIterations; resolved > 0 {
			maxTurns = resolved
		}
	}
	applyMaxTurnsDial(&graph, maxTurns)
	// Extended-thinking budget (wiring-integrity-01PMAG04 WP08). Resolved
	// per StartStream like maxTurns so a settings change lands on the next
	// turn. Zero leaves every model node's attr untouched.
	applyReasoningBudgetDial(&graph, r.cfg.ReasoningBudget())
	// autonomy-knobs-live-01PMAG02 WP02: at askOnAmbiguity=never, every
	// AskNode gets a DefaultAnswer so an unseeded ask resolves instead of
	// pausing the run (spec §3.1 bullet 2). A nil AutonomyKnobs provider
	// resolves AskOnAmbiguity to "" (the zero value), which
	// applyAskOnAmbiguityDial treats as "not never" — no-op, FR-005.
	applyAskOnAmbiguityDial(&graph, resolvedKnobs.AskOnAmbiguity)

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
			// autonomy-knobs-live-01PMAG02 WP02: at proceed/never, withhold
			// kenaz__ask_user_question from the catalog entirely so the
			// model cannot stall the turn on a clarifying question — the
			// cheapest possible enforcement point (spec §3.1 bullet 1). A
			// nil AutonomyKnobs provider resolves AskOnAmbiguity to "" (the
			// zero value), which withholdsAskTool treats as "keep the tool"
			// — no-op, FR-005.
			if withholdsAskTool(resolvedKnobs.AskOnAmbiguity) {
				toolCatalog = filterOutTool(toolCatalog, askuserquestion.ToolName)
			}
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
	imageCapturer := r.cfg.GeneratedImageCapturer
	if multimodalOutDisabledByEnv() {
		imageCapturer = nil
	}
	llmAdapter := NewLLMProviderAdapter(r.cfg.Registry, profileID, modelOverride, toolCatalog, imageCapturer).
		WithSessionID(sessionID).
		WithEnvContext(r.cfg.Clock, r.cfg.WorkspaceDir, r.cfg.WorkspaceNote).
		WithCustomInstructions(r.cfg.CustomInstructions).
		// autonomy-knobs-live-01PMAG02 WP06: recapStyle was resolved
		// through the three-layer chain and read by nothing. The closure
		// captures the already-resolved resolvedKnobs by value (fix F8)
		// rather than re-invoking r.cfg.AutonomyKnobs — Generate calls
		// this once per turn iteration, and the knob value cannot change
		// mid-StartStream anyway.
		WithRecapStyle(func() autonomy.RecapMode { return resolvedKnobs.RecapStyle }).
		// autonomy-knobs-live-01PMAG02 WP02: states the bar for using
		// ask_user_question at hard/major. Empty at every other mode
		// (see buildAskBarBlock), so FR-005 holds with no AutonomyKnobs
		// provider wired.
		WithAskOnAmbiguity(func() autonomy.AskMode { return resolvedKnobs.AskOnAmbiguity })
	toolAdapter := newKernelToolAdapter(r.cfg.Pool, r.cfg.Perms, sessionID)
	if r.cfg.AutonomyKnobs != nil {
		// fix F8: pin the already-resolved value rather than handing the
		// adapter r.cfg.AutonomyKnobs directly — Call() invokes this once
		// per tool call, and the raw provider would re-run the full
		// three-layer store resolution on every one of them. resolvedKnobs
		// does not change for the rest of this StartStream call, so the
		// closure can safely capture it.
		toolAdapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return resolvedKnobs })
	}
	// confirm-each-enforcement-01PMAG05 WP02: give the adapter a real
	// prompt channel. Before this the seam existed and had zero
	// production call sites, so every confirm_each verdict in a shipped
	// build hit the no-channel branch. Attached unconditionally: a nil
	// bus is still meaningful (it selects the headless policy) and the
	// deps bundle's zero value is the safe configuration.
	toolAdapter.withConfirm(r.cfg.Confirm).withConfirmDeps(r.cfg.ConfirmDeps)

	r.mu.Lock()
	r.nextID++
	subID := fmt.Sprintf("chat-%d", r.nextID)
	r.mu.Unlock()

	bridge := NewStreamBridge(r.cfg.Broker, subID, sessionID)

	// The run's ctx derives from Background so it survives the inbound
	// RPC call returning. A watcher goroutine (started after `sub` is
	// built, below) re-attaches the inbound ctx's cancellation AND records
	// the cause, so the terminal path can tell a focus-loss abort apart
	// from an explicit Stop.
	streamCtx, cancel := context.WithCancel(context.Background())

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
		// Budget: carry the graph's declared per-run caps onto the Env
		// (autonomy-knobs-live-01PMAG02 WP03).
		//
		// Until this line, env.Budget was the zero value on every chat
		// run -- nothing anywhere in core/ assigned it. Every guard in
		// kernel.checkBudget is `if env.Budget.X > 0 && ...`, so all of
		// them short-circuited, and chat_default.yaml's budget block
		// (max_llm_calls_per_run: 100, max_tool_calls_per_run: 200,
		// max_tokens_per_run: 200000) was decorative: production chat
		// had no per-run caps at all. The declared numbers read as a
		// safety net in review and enforced nothing at runtime.
		//
		// DoomLoopThreshold and MaxBacktracksPerRun were unaffected --
		// both treat zero as "use the package default" rather than
		// "unlimited" -- which is why the gap survived: the behavioural
		// guards worked while the volume guards silently did not.
		Budget: applyTokenCeilingKnob(graph.Budget, resolvedKnobs),
		// AutoCompaction is the growth watermark in front of the
		// kernel's own automatic pre_call site
		// (turn-context-runway-01PMAG03 WP02).
		//
		// It replaced an unconditional suppression boolean this Env used
		// to set, and it survives the WP08 convergence for a reason that
		// outlived the boolean's. The `compact` node compacts this
		// session at the top of the run; without a watermark the first
		// pre_call visit would immediately compact again, against the
		// transcript the node just produced — the double-fire
		// compaction-convergence-01PMDL05 found, relocated rather than
		// removed.
		//
		// The watermark keeps the single-fire guarantee without the
		// ceiling the boolean imposed: its baseline is latched by the
		// first pre_call site's own observation, so that site is refused
		// by construction, while a site fifteen iterations later — once
		// the turn has genuinely accumulated context past the margin — is
		// admitted. A turn can now be compacted more than once, which is
		// what a 25-iteration loop with a 200-call tool budget needs.
		AutoCompaction: coreag.NewCompactionWatermark(r.compactionWatermarkPolicy()),
		// NodeErrorPolicy (autonomy-knobs-live-01PMAG02 WP04): translates
		// the resolved continueOnError autonomy knob into the generic,
		// autonomy-agnostic enum core/agentgraph's loopExecutor consults.
		// The zero value (ErrorMode "" from a nil AutonomyKnobs provider)
		// maps to NodeErrorPolicyStop, today's behaviour (FR-005).
		NodeErrorPolicy: continueOnErrorPolicy(resolvedKnobs.ContinueOnError),
		// TaskStateArming (agentgraph-total-convergence-01PMGX01 WP11b):
		// derived FROM THE GRAPH, not from the settings flag directly.
		//
		// The exit gate checks the draft against the run's goal, and a
		// run that succeeded has by construction never armed recovery —
		// so a routed graph on failure-only TaskState would be checking
		// the answer against nothing (01PMAG01 G5). Reading the graph
		// rather than re-reading the flag is what makes the two
		// impossible to get out of step: whatever topology this turn
		// actually got is the topology the policy matches, including in
		// tests that hand the runner a graph directly and never touch
		// settings.
		TaskStateArming: taskStateArmingFor(graph),
		// AskPolicy (WP11b, autonomy-knobs finding F7): at
		// askOnAmbiguity proceed/never the ask tool is already withheld
		// from the model above. Without this, the kernel-side recovery
		// path re-opened the same door from somewhere the user never
		// saw — an exit gate hitting its cap escalates, and the
		// escalation ladder's terminal rung asks a human. Withhold
		// makes a FAIL re-enter the loop while budget remains and
		// return the best draft after that.
		AskPolicy: askPolicyFor(resolvedKnobs.AskOnAmbiguity),
		// Compactor is the shared FR-041 pipeline with this run's
		// session-rewrite strategy bound onto it. It is what the
		// `compact` node in chat_default.yaml dispatches through, and
		// what the mid-run pre_call site uses once the watermark admits
		// it. nil when no pipeline was wired, which makes the compact
		// node a passthrough (agentgraph-total-convergence-01PMGX01
		// WP08).
		Compactor: r.bindCompactor(profileID, modelOverride),
	}
	if r.cfg.History != nil {
		env.History = historyAdapterFunc(r.cfg.History.History)
	}
	if r.cfg.EnvDefaults != nil {
		r.cfg.EnvDefaults(env)
	}
	// WP12: register the spec this turn will actually execute, so the
	// turn can be projected back into a graph afterwards. Recorded here
	// — after the routing gate and the max-turns dial have finished
	// rewriting `graph` and after env.Graph points at it — because the
	// materialized graph's attrs must describe the run that happened,
	// not the file on disk.
	if r.cfg.RunSpecRecorder != nil {
		r.cfg.RunSpecRecorder(subID, graph)
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
	if r.cfg.UsageHook != nil || r.cfg.GeneratedImageCapturer != nil {
		if env.Hooks == nil {
			env.Hooks = coreag.NewHookManager(env.Memory, env.SessionID, env.ProjectID)
		}
		capturedAdapter := llmAdapter
		capturedSessionID := sessionID
		if r.cfg.UsageHook != nil {
			usageHook := r.cfg.UsageHook
			env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(ctx context.Context, sID, messageID, _ string) {
				resp := capturedAdapter.LastResponse()
				providerKind := capturedAdapter.ProviderKind()
				modelID := capturedAdapter.ActiveModelID()
				usageHook(ctx, capturedSessionID, messageID, providerKind, modelID, resp)
			})
		}
		// WP02 (multimodal-io-extended-01KQ8TD2): drain buffered generated
		// images into the artifact store now that session_write has produced
		// a stable messageID. Non-fatal: a capture error is logged and
		// dropped so the chat turn is not aborted by an artifact-write
		// failure.
		if r.cfg.GeneratedImageCapturer != nil {
			imageCapturer := r.cfg.GeneratedImageCapturer
			env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(ctx context.Context, sID, messageID, _ string) {
				imgs := capturedAdapter.DrainPendingImages()
				for _, img := range imgs {
					if err := imageCapturer.OnGeneratedImage(ctx, capturedSessionID, messageID, img); err != nil {
						slog.Default().Warn("chat.generated_image.capture_failed",
							"session_id", capturedSessionID,
							"message_id", messageID,
							"index", img.Index,
							"err", err.Error())
					}
				}
			})
		}
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

	logging.L().Info("chat.run.start",
		"sub_id", subID,
		"session_id", sessionID,
		"profile_id", profileID,
		"model_override", modelOverride,
	)

	// Watcher: re-attach the inbound ctx's cancellation to streamCtx and
	// record the cause. The inbound ctx here is the Wails app-lifetime
	// context (bindings.ctx()), so this fires on app shutdown — NOT on
	// window focus loss (focus-loss stalls are an App Nap problem, handled
	// by NSAppSleepDisabled in build/darwin/Info*.plist). Recording the
	// cause as "inbound-ctx" keeps a shutdown-time abort distinguishable
	// from an explicit user Stop in the log.
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.L().Error("chat.run.watcher.panic",
					"sub_id", subID, "session_id", sessionID, "panic", rec)
				cancel() // never leave the run orphaned on a watcher panic
			}
		}()
		select {
		case <-ctx.Done():
			sub.cancelCause.Store("inbound-ctx")
			logging.L().Warn("chat.run.ctx_cancelled",
				"sub_id", subID,
				"session_id", sessionID,
				"cause", "inbound-ctx",
				"ctx_err", ctx.Err(),
			)
			cancel()
		case <-streamCtx.Done():
		}
	}()

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
	sub.cancelCause.Store("stop-called")
	logging.L().Info("chat.run.stop_called",
		"sub_id", subID, "session_id", sub.sessionID)
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
		// Recover any panic in the kernel run or the terminal
		// classification/persist logic. Without this a panic would crash
		// the whole harness process AND (via the deferred close below)
		// the run's bookkeeping would still need to complete so StopStream's
		// `<-sub.done` does not deadlock. close(sub.done) runs last.
		if rec := recover(); rec != nil {
			log.Error("chat.run.panic",
				"sub_id", sub.id, "session_id", sub.sessionID,
				"panic", fmt.Sprintf("%v", rec), "stack", string(debug.Stack()))
			// Best-effort: tell the frontend the turn ended so the typing
			// indicator clears instead of hanging forever.
			if sub.finished.CompareAndSwap(false, true) {
				sub.bridge.EmitClosed("backend-error", "internal error", "")
			}
		}
		r.mu.Lock()
		delete(r.subs, sub.id)
		r.mu.Unlock()
		close(sub.done)
	}()

	// WP02 — periodic partial flush: start a background goroutine that
	// periodically persists accumulated streamed text during the run.
	// This closes the crash-loss window (FR-002). The goroutine exits
	// automatically when the run's context is cancelled.
	if r.cfg.PartialPersister != nil {
		go runPeriodicFlush(ctx, sub.sessionID, sub.bridge, r.cfg.PartialPersister, 0)
	}

	runStart := time.Now()
	err := r.cfg.Kernel.Run(ctx, env)
	reason := "completed"
	message := ""
	finishReason := ""
	// errorKind discriminates the terminal close beyond `reason` so the
	// surface can render honest copy — see StreamClosedPayload.ErrorKind.
	errorKind := ""
	runTerminatedClean := false // true when kernel finished without error (or ErrPaused)

	if err != nil {
		log.Info("chat.run.kernel_exit",
			"sub_id", sub.id,
			"session_id", sub.sessionID,
			"err", err.Error(),
			"cancel_cause", cancelCauseString(sub),
			"duration_ms", time.Since(runStart).Milliseconds(),
		)
	}

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

	// custom-openai-compatible-endpoint-01KQ8VN0 WP05: capability gate
	// interception. When a custom endpoint's probed matrix blocks a request,
	// emit the provider:capability-missing broker topic before the stream-closed
	// payload so the frontend can render a targeted hint instead of a generic
	// backend-error toast.
	var capMissing *corellm.ErrCustomEndpointMissingCapability
	if errors.As(err, &capMissing) {
		r.cfg.Broker.Emit("provider:capability-missing", map[string]any{
			"capability": capMissing.Capability,
			"endpoint":   capMissing.Endpoint,
			"profile_id": capMissing.ProfileID,
		})
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
	case errors.Is(err, compaction.ErrSessionFull):
		// The `compact` node decided the user is genuinely out of
		// context: the dial is "off" and the session is already over
		// cap, or the compaction model is too small to summarise the
		// span that would have to go.
		//
		// Before agentgraph-total-convergence-01PMGX01 WP08 compaction
		// ran as a pre-kernel pass, so this surfaced as a synchronous
		// StartStream error. Now that it is a node, the run has already
		// started when the condition is detected, and the honest report
		// travels on the stream-closed payload instead. The copy is
		// identical, and it is the same channel the overflow-recovery
		// path has always used for the same message
		// (recoverFromOverflow's budget-exhausted branch), so the
		// surface renders one thing for one condition.
		reason = "backend-error"
		message = compaction.ErrSessionFull.Error()
		errorKind = StreamClosedErrorKindSessionFull
	case err != nil && isContextOverflowError(err):
		// Reactive context-overflow recovery (FR-005 / agent-loop-
		// robustness-parity WP05), budgeted by
		// turn-context-runway-01PMAG03 WP03.
		//
		// An overflow that slips past the capability-table pre-check
		// triggers a compact-and-retry path instead of a terminal error.
		// This used to be a hardcoded one-shot (sub.overflowRetried.
		// Swap(true)): grow until overflow → one rescue → grow again →
		// die. It is now a counter against a dial-able budget, so the
		// number of rescues a turn gets is a policy decision rather than
		// a constant baked into the control flow.
		//
		// On compaction failure, or once the budget is spent, we fall
		// through to the terminal path — with the honest "session full"
		// copy when the reason we stopped is that the budget ran out,
		// rather than a generic backend error that tells the user
		// nothing about why their long turn died.
		reason, message, runTerminatedClean = r.recoverFromOverflow(log, sub, env, err)
		// The budget-exhausted branch reports the session-full copy. It
		// is the same situation for the user as the compact node's own
		// session-full verdict — the conversation no longer fits and
		// retrying will not help — so it carries the same discriminator
		// and the surface renders one thing for one condition.
		if message == compaction.ErrSessionFull.Error() {
			errorKind = StreamClosedErrorKindSessionFull
		}
	case err != nil && capMissing != nil:
		reason = "custom_endpoint_missing_capability"
		message = err.Error()
	case errors.Is(err, context.Canceled):
		// The run's context was cancelled. Distinguish an explicit user
		// Stop from the inbound (app-lifetime) ctx being cancelled — the
		// latter ("inbound-ctx") happens at app shutdown. Both surface as
		// "stop-called" to the frontend (no error toast), but the log
		// makes the true cause unambiguous when debugging "the agent
		// stopped on its own".
		reason = "stop-called"
		cause := cancelCauseString(sub)
		if cause == "inbound-ctx" {
			log.Warn("chat.run.aborted_by_inbound_ctx",
				"sub_id", sub.id,
				"session_id", sub.sessionID,
				"note", "run cancelled by inbound app ctx (app shutdown), not a user Stop",
			)
		}
		// FR-001 (agent-loop-robustness-parity WP01): persist the partial
		// assistant text + backfill synthetic is_error tool_results for any
		// dangling tool_use calls so the transcript is API-valid on resume.
		// Use a fresh background context — the run's ctx is already
		// cancelled at this point.
		if r.cfg.HistoryWriter != nil {
			danglingCalls := sub.bridge.SeenToolCalls()
			interruptState := NewInterruptState(sub.bridge, danglingCalls)
			persistCtx, persistCancel := context.WithTimeout(context.Background(), persistPartialTimeout)
			interruptState.PersistInterrupt(persistCtx, sub.sessionID, r.cfg.HistoryWriter)
			persistCancel()
		}
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
	sub.bridge.EmitClosedFull(StreamClosed{
		Reason:             reason,
		Message:            message,
		FinishReason:       finishReason,
		PartialMessageID:   partialMessageID,
		PartialFailureKind: partialFailureKind,
		PartialRecoverable: partialRecoverable,
		ErrorKind:          errorKind,
	})
	log.Info("chat.run.complete",
		"sub_id", sub.id,
		"session_id", sub.sessionID,
		"reason", reason,
		"err", message,
	)
}

// cancelCauseString returns the recorded cancellation cause for a sub
// ("stop-called", "inbound-ctx") or "none" when nothing cancelled it.
// Used by the terminal path to attribute a context.Canceled exit.
func cancelCauseString(sub *chatSub) string {
	if v, ok := sub.cancelCause.Load().(string); ok && v != "" {
		return v
	}
	return "none"
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

// autonomyKnobs resolves the current autonomy dial values for the given
// session, or the zero ResolvedKnobs when no provider is wired (test
// paths). Callers must treat every zero field as "no override".
//
// Session-scoped (autonomy-knobs-live-01PMAG02 WP01): the three-layer
// chain can only resolve a session's own overrides once the session id
// is known.
//
// Fix F8: takes ctx and does real I/O in production (the provider
// walks the global/project/session autonomy stores) — StartStream
// calls this exactly once per run, with the run's real ctx, and
// threads the resulting value everywhere else instead of calling this
// method again.
func (r *ChatRunner) autonomyKnobs(ctx context.Context, sessionID string) autonomy.ResolvedKnobs {
	if r == nil || r.cfg.AutonomyKnobs == nil {
		return autonomy.ResolvedKnobs{}
	}
	return r.cfg.AutonomyKnobs(ctx, sessionID)
}

// askOnAmbiguityNeverDefaultAnswer is the stated assumption an AskNode
// resolves to when askOnAmbiguity=never strands it with no seeded
// answer (autonomy-knobs-live-01PMAG02 WP02; spec Risk table: "the run
// proceeds with a stated assumption rather than hanging — recap
// surfaces the assumption").
const askOnAmbiguityNeverDefaultAnswer = "No user input was available (askOnAmbiguity=never); proceeding on best judgement."

// autonomy-knobs-live-01PMAG02 WP07: declare the consumers co-located
// in this file so the knob-coverage guard
// (core/rpc/views/agentgraph/chat/knob_coverage_guard_test.go) sees
// them registered by the time its test runs.
//
// One registration per knob (wiring-integrity-01PMAG04 WP07 migration
// note): askOnAmbiguity has two call sites in this file
// (applyAskOnAmbiguityDial and withholdsAskTool) but gets a single
// combined registration, not two — core/wiring/knobcoverage.Register
// panics on a second registration for the same (struct, field), so
// collapsing multi-site knobs into one description here keeps this
// ready for that swap. See knob_coverage_guard_test.go's doc comment
// for why the swap hasn't happened on this branch yet.
func init() {
	knobcoverage.Register[autonomy.ResolvedKnobs]("MaxIterations", "chat.ChatRunner.StartStream (applyMaxTurnsDial)")
	knobcoverage.Register[autonomy.ResolvedKnobs]("AskOnAmbiguity", "chat.applyAskOnAmbiguityDial (default_answer) + chat.withholdsAskTool (catalog shaping)")
	knobcoverage.Register[autonomy.ResolvedKnobs]("ContinueOnError", "chat.continueOnErrorPolicy")
	knobcoverage.Register[autonomy.ResolvedKnobs]("TokenCeilingPerTurn", "chat.applyTokenCeilingKnob")
	knobcoverage.RegisterDeferred[autonomy.ResolvedKnobs]("SourceTrace", "resolver bookkeeping, not a tunable knob")
	knobcoverage.RegisterDeferred[autonomy.ResolvedKnobs]("PostureMode", "resolver bookkeeping, not a tunable knob")
}

// applyAskOnAmbiguityDial folds the askOnAmbiguity knob onto the chat
// graph's AskNode(s) (autonomy-knobs-live-01PMAG02 WP02, spec §3.1
// bullet 2). At AskNever, every AskNode that doesn't already declare a
// DefaultAnswer gets one, so core/agentgraph's askExecutor resolves an
// unseeded ask instead of pausing the run with ErrPaused. Every other
// AskMode (including the zero value, which a nil AutonomyKnobs provider
// resolves to) leaves AskAttrs untouched — FR-005.
func applyAskOnAmbiguityDial(g *coreag.Graph, mode autonomy.AskMode) {
	if mode != autonomy.AskNever {
		return
	}
	for i := range g.Nodes {
		if g.Nodes[i].Kind != coreag.NodeKindAsk {
			continue
		}
		if a, ok := g.Nodes[i].Attrs.(coreag.AskAttrs); ok && a.DefaultAnswer == "" {
			a.DefaultAnswer = askOnAmbiguityNeverDefaultAnswer
			g.Nodes[i].Attrs = a
		}
	}
}

// withholdsAskTool reports whether the resolved askOnAmbiguity mode
// means kenaz__ask_user_question must not be offered to the model at
// all (autonomy-knobs-live-01PMAG02 WP02, spec §3.1 bullet 1). Only
// proceed/never withhold the tool — hard/major keep it available (with
// buildAskBarBlock's system-prompt bar), and always/"" (the zero value,
// what a nil AutonomyKnobs provider resolves to) keep today's
// behaviour of offering it unconditionally.
func withholdsAskTool(mode autonomy.AskMode) bool {
	return mode == autonomy.AskProceed || mode == autonomy.AskNever
}

// filterOutTool returns a new slice with every ToolSpec named name
// removed, preserving order. A nil/empty input or no match returns the
// input slice unchanged (no allocation on the common path where the
// tool isn't in the catalog at all).
func filterOutTool(tools []corellm.ToolSpec, name string) []corellm.ToolSpec {
	idx := -1
	for i, t := range tools {
		if t.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return tools
	}
	out := make([]corellm.ToolSpec, 0, len(tools)-1)
	for _, t := range tools {
		if t.Name != name {
			out = append(out, t)
		}
	}
	return out
}

// taskStateArmingFor selects the kernel's TaskState arming policy from
// the topology this turn actually got (agentgraph-total-convergence-
// 01PMGX01 WP11b; design in agentic-turn-routing-01PMAG01 §3.5).
//
// It reads the GRAPH rather than the settings flag on purpose. The
// always-armed policy exists to serve the exit gate, the exit gate
// arrives with the routed topology, and the routed topology is what
// GateAgenticTurnRouting produced from the flag — so keying off the
// graph is keying off the flag, one step later, with no way for the two
// to disagree. It also does the right thing for a caller that supplies
// its own graph and never touches settings at all.
func taskStateArmingFor(g coreag.Graph) coreag.TaskStateArming {
	if coreag.GraphUsesAgenticTurnRouting(g) {
		return coreag.TaskStateArmAlways
	}
	return coreag.TaskStateArmOnFailure
}

// askPolicyFor translates the resolved askOnAmbiguity knob into
// core/agentgraph's autonomy-agnostic AskPolicy enum
// (agentgraph-total-convergence-01PMGX01 WP11b, autonomy-knobs finding
// F7). It reuses withholdsAskTool rather than re-listing the modes, so
// the catalogue-shaping decision and the kernel-side one can never
// drift apart — the user-visible promise is one thing ("do not ask
// me"), and it should have one predicate.
func askPolicyFor(mode autonomy.AskMode) coreag.AskPolicy {
	if withholdsAskTool(mode) {
		return coreag.AskPolicyWithhold
	}
	return coreag.AskPolicyAllow
}

// continueOnErrorPolicy translates the resolved autonomy continueOnError
// knob into core/agentgraph's autonomy-agnostic NodeErrorPolicy enum
// (autonomy-knobs-live-01PMAG02 WP04). Any value other than
// autonomy.ErrorRetryOnce / autonomy.ErrorAdapt — including the zero
// value ErrorMode(""), what a nil AutonomyKnobs provider resolves to —
// maps to NodeErrorPolicyStop, today's behaviour (FR-005).
func continueOnErrorPolicy(mode autonomy.ErrorMode) coreag.NodeErrorPolicy {
	switch mode {
	case autonomy.ErrorRetryOnce:
		return coreag.NodeErrorPolicyRetryOnce
	case autonomy.ErrorAdapt:
		return coreag.NodeErrorPolicyAdapt
	default:
		return coreag.NodeErrorPolicyStop
	}
}

// TopicChatOverflowRecovery carries the mid-turn "your conversation got
// too long, so it was compacted and the turn re-driven" notice
// (agentgraph-total-convergence-01PMGX01 WP17).
//
// Distinct from the TERMINAL session-full report. That one rides
// llm:stream-closed with error_kind="session_full" and means the turn is
// over and did not produce an answer. This one means the turn is still
// coming — it fires while the user is staring at a stalled stream and is
// the only thing that explains the pause.
//
// Emitted once per recovery attempt. Payload: sub_id, session_id,
// attempt (1-based), budget.
//
// NOTE: core/serve/wsstream.go forwards this topic to served-mode
// browsers and duplicates the literal, because core/serve cannot import
// this package (core/rpc imports it, so the edge would be a cycle) —
// the same duplication toolloop.TopicToolConfirmPending carries. Change
// one, change the other.
const TopicChatOverflowRecovery = "chat:overflow-recovery"

// maxOverflowRecoveries resolves this run's overflow-recovery budget.
//
// A nil runner/resolver falls back to the package default. A NEGATIVE
// value also falls back to the default rather than reading as
// "unlimited": an unbounded rescue loop against a session that
// genuinely cannot fit would burn compaction calls forever.
//
// Zero is honoured as-is and means "disabled — no automatic rescues",
// which is a legitimate configuration (a caller who wants overflows to
// surface immediately). TestRecoverFromOverflow_ZeroBudgetSurfacesThe
// RealError pins that a zero budget reports the real overflow rather
// than the session-full copy.
func (r *ChatRunner) maxOverflowRecoveries() int {
	if r == nil || r.cfg.MaxOverflowRecoveries == nil {
		return DefaultMaxOverflowRecoveriesPerTurn
	}
	n := r.cfg.MaxOverflowRecoveries()
	if n < 0 {
		return DefaultMaxOverflowRecoveriesPerTurn
	}
	return n
}

// recoverFromOverflow runs the budgeted compact-and-redrive loop for a
// run that exited with a context-overflow error, and returns the
// terminal (reason, message, clean) triple for the stream-closed
// payload.
//
// Each iteration spends one unit of the MaxOverflowRecoveriesPerTurn
// budget, emits TopicChatOverflowRecovery, and re-drives the kernel on a
// fresh context. That event now has a real subscriber on both surfaces
// (frontend useSession -> the overflow-recovery notice in SessionsView;
// served mode via wsstream passthroughTopics), added by 01PMGX01 WP17 —
// before that it was emitted with this same "so the surface can show the
// user what happened" comment and nothing listened, so the pause here
// was indistinguishable from a hang. A redrive
// that overflows *again* loops for another rescue if the budget allows;
// today's default budget of 1 makes that a single pass, identical to
// the pre-WP03 one-shot.
//
// Exhausting the budget is reported with compaction.ErrSessionFull's
// copy — the same honest wording the pre-send path uses when the user
// is genuinely out of context — instead of the raw provider overflow
// string, which reads as an opaque backend failure.
func (r *ChatRunner) recoverFromOverflow(
	log *slog.Logger,
	sub *chatSub,
	env *coreag.Env,
	overflowErr error,
) (reason, message string, clean bool) {
	budget := r.maxOverflowRecoveries()
	lastErr := overflowErr

	for {
		used := sub.overflowRecoveries.Load()
		if int(used) >= budget {
			// Budget spent (or zero to begin with). If we never got to
			// try, the honest report is still the overflow itself;
			// if we did, the user is out of runway.
			if used == 0 {
				log.Info("chat.overflow_recovery.disabled",
					"sub_id", sub.id, "session_id", sub.sessionID, "budget", budget)
				return "backend-error", lastErr.Error(), false
			}
			log.Warn("chat.overflow_recovery.budget_exhausted",
				"sub_id", sub.id, "session_id", sub.sessionID,
				"budget", budget, "used", used, "err", lastErr.Error())
			return "backend-error", compaction.ErrSessionFull.Error(), false
		}

		if recErr := attemptOverflowRecovery(
			context.Background(), // fresh ctx — run ctx may be cancelled
			sub.sessionID,
			sub.profileID,
			sub.modelOverride,
			r.cfg.Compaction,
		); recErr != nil {
			// Compaction is not possible (deps unwired, engine error).
			// Surface the real problem rather than the session-full
			// copy: the user's session is not necessarily full, our
			// recovery path is just unavailable.
			log.Info("chat.overflow_recovery.compact_unavailable",
				"sub_id", sub.id, "session_id", sub.sessionID, "err", recErr.Error())
			return "backend-error", lastErr.Error(), false
		}

		// The recovery just compacted the persisted history, so the
		// transcript the redrive starts from is a new, smaller starting
		// point. Re-baseline the watermark against it.
		//
		// Without this the redrive carries the pre-overflow baseline —
		// a baseline latched against a transcript that no longer
		// exists, and a large one, since we only got here by
		// overflowing. Every mid-run site would then be measured
		// against that inflated number and refused, making mid-run
		// compaction LEAST likely in exactly the run that just proved
		// it needs it.
		env.AutoCompaction.Rearm()

		attempt := sub.overflowRecoveries.Add(1)
		log.Info("chat.overflow_recovery.auto_redrive",
			"sub_id", sub.id, "session_id", sub.sessionID,
			"attempt", attempt, "budget", budget)
		if r.cfg.Broker != nil {
			r.cfg.Broker.Emit(TopicChatOverflowRecovery, map[string]any{
				"sub_id":     sub.id,
				"session_id": sub.sessionID,
				"attempt":    attempt,
				"budget":     budget,
			})
		}

		redriveCtx, redriveCancel := context.WithCancel(context.Background())
		redriveErr := r.cfg.Kernel.Run(redriveCtx, env)
		redriveCancel()

		switch {
		case redriveErr == nil:
			return "completed", "", true
		case isContextOverflowError(redriveErr):
			// Overflowed again. Loop: another rescue if the budget has
			// room, otherwise the exhaustion branch above reports
			// session-full.
			lastErr = redriveErr
		default:
			log.Info("chat.overflow_recovery.redrive_exit",
				"sub_id", sub.id, "session_id", sub.sessionID, "err", redriveErr.Error())
			return "backend-error", redriveErr.Error(), false
		}
	}
}

// compactionWatermarkPolicy resolves the mid-run compaction trigger
// policy for this run (turn-context-runway-01PMAG03 WP02). Resolved per
// StartStream so the dial is live without a restart; a nil runner or
// resolver yields the zero-value policy, which the agentgraph package
// fills with its defaults.
func (r *ChatRunner) compactionWatermarkPolicy() coreag.CompactionWatermarkPolicy {
	if r == nil || r.cfg.CompactionWatermark == nil {
		return coreag.CompactionWatermarkPolicy{}
	}
	return r.cfg.CompactionWatermark()
}

// applyTokenCeilingKnob folds the autonomy tokenCeilingPerTurn dial into
// the graph's declared budget (autonomy-knobs-live-01PMAG02 WP03).
//
// The knob may only LOWER the graph's ceiling, never raise it. A graph's
// budget block is the author's safety cap; letting a Settings toggle
// raise it would mean a UI control could silently defeat a limit the
// graph declared on purpose. Raising the ceiling is a graph edit.
//
// Zero on either side means "no opinion": a zero knob leaves the graph
// value alone, and a zero graph value (no declared cap) lets the knob
// establish one.
func applyTokenCeilingKnob(b coreag.Budget, knobs autonomy.ResolvedKnobs) coreag.Budget {
	ceiling := knobs.TokenCeilingPerTurn
	if ceiling <= 0 {
		return b
	}
	if b.MaxTokensPerRun <= 0 || ceiling < b.MaxTokensPerRun {
		b.MaxTokensPerRun = ceiling
	}
	return b
}

// applyReasoningBudgetDial threads the resolved extended-thinking budget
// onto every model node in the graph (wiring-integrity-01PMAG04 WP08).
//
// ModelAttrs.ReasoningBudgetTokens -> LLMRequest.ReasoningBudgetTokens ->
// GenerationRequest.Reasoning has been plumbed and tested since
// model-request-path-live-01PMDL01 WP06b, but no shipped graph ever set
// the attr, so the entire path was dead. This is the missing last hop.
//
// budget <= 0 is a no-op: the attr is left exactly as the graph author
// wrote it (normally unset), so a harness with the dial off produces
// byte-identical requests to pre-01PMAG04. That "no-op means untouched"
// property — rather than "no-op means write 0" — is what lets a graph
// author set the attr explicitly and not have the dial clobber it.
func applyReasoningBudgetDial(g *coreag.Graph, budget int) {
	if g == nil || budget <= 0 {
		return
	}
	for i := range g.Nodes {
		if g.Nodes[i].Kind != coreag.NodeKindModel {
			continue
		}
		if a, ok := g.Nodes[i].Attrs.(coreag.ModelAttrs); ok {
			a.ReasoningBudgetTokens = budget
			g.Nodes[i].Attrs = a
		}
	}
}

// historyAdapterFunc adapts a closure to the agentgraph.HistoryReader
// interface. Mirrors HistoryReaderFunc but without the kernel-side
// type alias collision.
type historyAdapterFunc func(ctx context.Context, sessionID string, n int) ([]coreag.Message, error)

// History satisfies agentgraph.HistoryReader.
func (f historyAdapterFunc) History(ctx context.Context, sessionID string, n int) ([]coreag.Message, error) {
	return f(ctx, sessionID, n)
}
