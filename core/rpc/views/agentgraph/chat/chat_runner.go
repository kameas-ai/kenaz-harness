package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

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
}

// UsageHookFunc is the callback signature for per-turn usage capture.
// sessionID and messageID identify the turn; messageID may be empty
// when the session_write node hasn't fired yet (test paths). The hook
// must not block the chat turn — it should write async or accept the
// latency.
type UsageHookFunc func(ctx context.Context, sessionID, messageID string, resp corellm.Response)

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

	mu     sync.Mutex
	subs   map[string]*chatSub
	nextID uint64
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
		cfg:  cfg,
		subs: map[string]*chatSub{},
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
	if r.cfg.UsageHook != nil {
		if env.Hooks == nil {
			env.Hooks = coreag.NewHookManager(env.Memory, env.SessionID, env.ProjectID)
		}
		usageHook := r.cfg.UsageHook
		capturedAdapter := llmAdapter
		capturedSessionID := sessionID
		env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(ctx context.Context, sID, messageID, _ string) {
			resp := capturedAdapter.LastResponse()
			usageHook(ctx, capturedSessionID, messageID, resp)
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
	sub.bridge.EmitClosed(reason, message, finishReason)
	log.Info("chat.run.complete",
		"sub_id", sub.id,
		"session_id", sub.sessionID,
		"reason", reason,
		"err", message,
	)
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
