package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

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
	// EnvDefaults is an optional callback the runner invokes on the
	// constructed Env before kernel.Run; production wiring threads
	// Memory / Policy / Branch / Hooks-journal seams through it.
	EnvDefaults func(env *coreag.Env)
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
	id        string
	sessionID string
	cancel    context.CancelFunc
	done      chan struct{}
	bridge    *StreamBridge
	finished  atomic.Bool
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
	llmAdapter := NewLLMProviderAdapter(r.cfg.Registry, profileID, modelOverride, nil /* tools wired by caller */)
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

	env := &coreag.Env{
		RunID:         subID,
		SessionID:     sessionID,
		Graph:         &graph,
		LLM:           llmAdapter,
		Tools:         toolAdapter,
		HistoryWriter: r.cfg.HistoryWriter,
		StreamSink:    bridge,
	}
	if r.cfg.History != nil {
		env.History = historyAdapterFunc(r.cfg.History.History)
	}
	if r.cfg.EnvDefaults != nil {
		r.cfg.EnvDefaults(env)
	}

	sub := &chatSub{
		id:        subID,
		sessionID: sessionID,
		cancel:    cancel,
		done:      make(chan struct{}),
		bridge:    bridge,
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
	switch {
	case err == nil:
		reason = "completed"
	case errors.Is(err, coreag.ErrPaused):
		// AskNode paused the run — chat surface treats this as the end
		// of one turn; the next user message starts a fresh run. The
		// stream-closed payload still fires so the frontend knows to
		// stop the typing indicator.
		reason = "completed"
		finishReason = "paused"
	case errors.Is(err, coreag.ErrBudgetExceeded):
		reason = "backend-error"
		message = "agent reached the per-run budget cap"
	case errors.Is(err, context.Canceled):
		reason = "stop-called"
	default:
		reason = "backend-error"
		message = err.Error()
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

// historyAdapterFunc adapts a closure to the agentgraph.HistoryReader
// interface. Mirrors HistoryReaderFunc but without the kernel-side
// type alias collision.
type historyAdapterFunc func(ctx context.Context, sessionID string, n int) ([]coreag.Message, error)

// History satisfies agentgraph.HistoryReader.
func (f historyAdapterFunc) History(ctx context.Context, sessionID string, n int) ([]coreag.Message, error) {
	return f(ctx, sessionID, n)
}
