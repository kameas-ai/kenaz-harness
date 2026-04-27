package agentgraph

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Kernel is the graph executor. It walks the ready set of a Graph,
// fires nodes via the executor registry, persists each batch to the
// EventLog, and enforces per-run budget caps.
//
// Concurrency: in-flight nodes are bounded by `maxInFlight` (default
// 8 — NFR / FR-060). Concurrent dispatches inside a single Run are
// serialised by `mu`; concurrent Runs against the same Kernel are
// safe because each call carries its own *Env.
type Kernel struct {
	registry    *executorRegistry
	log         EventLog
	maxInFlight int
	now         func() time.Time
	compactor   Compactor
}

// KernelOption tunes a Kernel.
type KernelOption func(*Kernel)

// WithEventLog plugs in an EventLog. Default is a fresh in-memory log.
func WithEventLog(l EventLog) KernelOption { return func(k *Kernel) { k.log = l } }

// WithMaxInFlight overrides the concurrency cap. Default 8.
func WithMaxInFlight(n int) KernelOption {
	return func(k *Kernel) {
		if n > 0 {
			k.maxInFlight = n
		}
	}
}

// WithExecutor overrides the executor for a kind. Used by tests +
// future spec extensions to plug in custom node-kinds.
func WithExecutor(ex Executor) KernelOption {
	return func(k *Kernel) { k.registry.byKind[ex.Kind()] = ex }
}

// WithClock overrides the clock used for timestamps. Used in tests.
func WithClock(now func() time.Time) KernelOption {
	return func(k *Kernel) { k.now = now }
}

// WithCompactor wires the configurable-compaction subsystem (Bundle
// D — FR-041..FR-045). The kernel pins the compactor onto every Env
// at the start of Run so executors at the LLMNode pre-call and
// ToolNode post-tool sites can dispatch through it. Nil disables
// compaction (the kernel default).
func WithCompactor(c Compactor) KernelOption {
	return func(k *Kernel) { k.compactor = c }
}

// NewKernel constructs a kernel with default executors + memory log.
func NewKernel(opts ...KernelOption) *Kernel {
	k := &Kernel{
		registry:    newExecutorRegistry(),
		log:         NewMemoryEventLog(),
		maxInFlight: 8,
		now:         func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(k)
	}
	return k
}

// EventLog returns the underlying event log.
func (k *Kernel) EventLog() EventLog { return k.log }

// Run fires the graph in env.Graph against the given env. Returns
// nil on completion, ErrPaused if an Ask parked the run, or
// ErrBudgetExceeded if a hard cap fired.
//
// The kernel walks the topological ready set: a node is ready when
// every node feeding its required input ports has completed. Loops
// and Retry node bodies execute inline within their container's
// Execute call (see exec_control.go); the kernel does NOT walk those
// bodies separately.
func (k *Kernel) Run(ctx context.Context, env *Env) error {
	if env == nil {
		return errors.New("agentgraph: kernel: env is nil")
	}
	if env.Graph == nil {
		return errors.New("agentgraph: kernel: env.Graph is nil")
	}
	applyEnvDefaults(env)

	// Fix: applyEnvDefaults's Hooks default needs the resolved memory
	// store, which we set above. Re-apply if it picked nilMemory.
	if env.Hooks == nil {
		env.Hooks = NewHookManager(env.Memory, env.SessionID, env.ProjectID)
	}

	// Pin the kernel's registry into env so nested dispatches inside
	// control executors (Loop, Retry, Parallel) honor any
	// WithExecutor overrides the kernel was built with.
	env.registry = k.registry

	// Pin the kernel's compactor into env so the compute executors
	// (LLMNode pre-call, ToolNode post-tool) reach the same pipeline
	// every node fire walks. Per-Env-overrides still win — only fill
	// in the default when the caller didn't pass one.
	if env.Compactor == nil && k.compactor != nil {
		env.Compactor = k.compactor
	}

	// Run start event.
	var startBatch EventBatch
	_ = startBatch.AppendKind(env.RunID, "", EventRunStart, map[string]any{
		"graph_id":     env.Graph.ID,
		"entrypoints":  env.Graph.Entrypoints,
		"max_in_flight": k.maxInFlight,
	})
	if _, err := k.log.Append(startBatch); err != nil {
		return fmt.Errorf("agentgraph: kernel: append run_start: %w", err)
	}

	idx := env.Graph.nodesByID()
	if len(idx) == 0 {
		return errors.New("agentgraph: kernel: graph has no nodes")
	}

	// Pre-compute the in-degree map (only outside-loop edges count;
	// edges into loop body nodes are handled by the LoopNode executor
	// internally).
	inEdges, outEdges := buildEdges(env.Graph)
	inside := bodyNodeIDs(env.Graph)

	// Initial ready set = entrypoints.
	ready := make([]string, 0, len(env.Graph.Entrypoints))
	for _, ep := range env.Graph.Entrypoints {
		if _, ok := idx[ep]; !ok {
			return fmt.Errorf("agentgraph: kernel: entrypoint %q does not exist", ep)
		}
		ready = append(ready, ep)
	}
	// Remaining in-degree counters.
	inDeg := make(map[string]int, len(idx))
	for id := range idx {
		if _, hidden := inside[id]; hidden {
			continue
		}
		inDeg[id] = len(inEdges[id])
	}
	for _, ep := range ready {
		inDeg[ep] = 0
	}

	completed := make(map[string]bool, len(idx))

	// Concurrency: simple counting semaphore.
	sem := make(chan struct{}, k.maxInFlight)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	pause := false
	pauseReason := ""

	var dispatch func(nodeID string)
	dispatch = func(nodeID string) {
		defer wg.Done()
		defer func() { <-sem }()

		mu.Lock()
		if firstErr != nil || pause {
			mu.Unlock()
			return
		}
		mu.Unlock()

		if err := k.checkBudget(env); err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			return
		}

		nd := idx[nodeID]
		ex, err := k.registry.lookup(nd.Kind)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			return
		}

		// Build inputs from upstream completed nodes' outputs along
		// edges that target this node.
		inputs := PortValues{}
		for _, e := range inEdges[nodeID] {
			out := env.State.Outputs(e.From.Node)
			if v, ok := out[e.From.Port]; ok {
				inputs[e.To.Port] = v
			}
		}

		// Telemetry span around the executor.
		ctx, end := env.Trace.Span(ctx, "node."+string(nd.Kind), map[string]any{
			"node_id": nd.ID,
			"run_id":  env.RunID,
		})

		var batch EventBatch
		_ = batch.AppendKind(env.RunID, nd.ID, EventNodeStart, map[string]any{
			"kind":  string(nd.Kind),
			"title": nd.Title,
		})

		r, err := ex.Execute(ctx, env, nd, inputs)
		// Always commit the node_start + executor events even on error.
		for _, e := range r.Events.Events {
			if e.RunID == "" {
				e.RunID = env.RunID
			}
			if e.NodeID == "" {
				e.NodeID = nd.ID
			}
			batch.Append(e)
		}
		if err != nil {
			_ = batch.AppendKind(env.RunID, nd.ID, EventNodeError, map[string]any{
				"err": err.Error(),
			})
			if _, lerr := k.log.Append(batch); lerr != nil && firstErr == nil {
				err = fmt.Errorf("%w; also log append: %v", err, lerr)
			} else {
				_ = lerr
			}
			env.State.MarkFailed(nd.ID, err)
			end(err)
			mu.Lock()
			if firstErr == nil {
				if errors.Is(err, ErrPaused) {
					pause = true
					pauseReason = "paused"
				} else {
					firstErr = err
				}
			}
			mu.Unlock()
			return
		}

		_ = batch.AppendKind(env.RunID, nd.ID, EventNodeComplete, map[string]any{
			"outputs": len(r.Outputs),
		})
		if _, lerr := k.log.Append(batch); lerr != nil && firstErr == nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = fmt.Errorf("agentgraph: kernel: append batch: %w", lerr)
			}
			mu.Unlock()
		}
		env.State.SetOutputs(nd.ID, r.Outputs)
		end(nil)

		mu.Lock()
		completed[nd.ID] = true
		if r.Pause {
			pause = true
			pauseReason = r.PauseReason
			mu.Unlock()
			return
		}

		// Promote downstream nodes whose in-degree drops to 0.
		newReady := []string{}
		for _, e := range outEdges[nd.ID] {
			to := e.To.Node
			if _, hidden := inside[to]; hidden {
				continue
			}
			if completed[to] {
				continue
			}
			inDeg[to]--
			if inDeg[to] <= 0 {
				newReady = append(newReady, to)
			}
		}
		mu.Unlock()

		for _, nID := range newReady {
			id := nID
			sem <- struct{}{}
			wg.Add(1)
			go dispatch(id)
		}
	}

	for _, id := range ready {
		nID := id
		sem <- struct{}{}
		wg.Add(1)
		go dispatch(nID)
	}
	wg.Wait()

	if pause {
		var pb EventBatch
		_ = pb.AppendKind(env.RunID, "", EventRunPaused, map[string]any{"reason": pauseReason})
		_, _ = k.log.Append(pb)
		return ErrPaused
	}
	if firstErr != nil {
		return firstErr
	}
	var endBatch EventBatch
	_ = endBatch.AppendKind(env.RunID, "", EventRunComplete, map[string]any{
		"completed_nodes": len(completed),
	})
	_, _ = k.log.Append(endBatch)
	return nil
}

// Resume re-runs Run after an Ask has been answered. The kernel
// rebuilds the in-memory ContextGraph from the EventLog so the run
// picks up at the next-ready node.
func (k *Kernel) Resume(ctx context.Context, env *Env) error {
	if env == nil || env.Graph == nil {
		return errors.New("agentgraph: kernel: env or graph nil")
	}
	applyEnvDefaults(env)
	if err := k.RebuildState(env); err != nil {
		return err
	}
	return k.Run(ctx, env)
}

// RebuildState walks the EventLog and re-derives env.State from the
// recorded node_complete events. It does NOT re-fire any executors —
// the durable execution model says once a node has completed in the
// log, we treat its recorded outputs as authoritative on resume
// (NFR-007 documents the LLM-token-level relaxation).
//
// In v1 we only track the completion bit for each node. The actual
// port values are not stored in the event payload (storing every
// LLM response verbatim explodes the log size). On resume the kernel
// re-fires unfinished nodes; completed ones are skipped.
func (k *Kernel) RebuildState(env *Env) error {
	if env.State == nil {
		env.State = NewRunState()
	}
	return k.log.Replay(env.RunID, func(e Event) error {
		if e.Kind == EventNodeComplete && e.NodeID != "" {
			// We don't have the original outputs in payload — replay
			// is conservative: mark complete but leave outputs nil.
			// Re-firing downstream of a re-fired node is the kernel's
			// job; we surface completion via env.State.completed.
			env.State.SetOutputs(e.NodeID, PortValues{})
		}
		if e.Kind == EventNodeError && e.NodeID != "" {
			env.State.MarkFailed(e.NodeID, errors.New(string(e.Payload)))
		}
		return nil
	})
}

// Leaves returns the IDs of nodes that have no outgoing edges (or
// whose downstream never completed). Used by ActivityNode to surface
// the sub-run's terminal value.
func (k *Kernel) Leaves(_ string, g *Graph) []string {
	if g == nil {
		return nil
	}
	out := []string{}
	hasOut := make(map[string]bool, len(g.Nodes))
	for _, e := range g.Edges {
		hasOut[e.From.Node] = true
	}
	inside := bodyNodeIDs(g)
	for _, n := range g.Nodes {
		if _, hidden := inside[n.ID]; hidden {
			continue
		}
		if !hasOut[n.ID] {
			out = append(out, n.ID)
		}
	}
	return out
}

// checkBudget enforces the per-run hard caps before each fire. Returns
// ErrBudgetExceeded if any cap is exceeded.
func (k *Kernel) checkBudget(env *Env) error {
	if env.Counters == nil {
		return nil
	}
	tokens, calls, tools, cost := env.Counters.Snapshot()
	if env.Budget.MaxTokensPerRun > 0 && tokens > env.Budget.MaxTokensPerRun {
		var b EventBatch
		_ = b.AppendKind(env.RunID, "", EventBudgetCapHit,
			map[string]any{"reason": "max_tokens_per_run", "limit": env.Budget.MaxTokensPerRun, "used": tokens})
		_, _ = k.log.Append(b)
		return ErrBudgetExceeded
	}
	if env.Budget.MaxLLMCallsPerRun > 0 && calls > env.Budget.MaxLLMCallsPerRun {
		var b EventBatch
		_ = b.AppendKind(env.RunID, "", EventBudgetCapHit,
			map[string]any{"reason": "max_llm_calls_per_run", "limit": env.Budget.MaxLLMCallsPerRun, "used": calls})
		_, _ = k.log.Append(b)
		return ErrBudgetExceeded
	}
	if env.Budget.MaxToolCallsPerRun > 0 && tools > env.Budget.MaxToolCallsPerRun {
		var b EventBatch
		_ = b.AppendKind(env.RunID, "", EventBudgetCapHit,
			map[string]any{"reason": "max_tool_calls_per_run", "limit": env.Budget.MaxToolCallsPerRun, "used": tools})
		_, _ = k.log.Append(b)
		return ErrBudgetExceeded
	}
	if env.Budget.MaxCostUSDPerRun > 0 && cost > env.Budget.MaxCostUSDPerRun {
		var b EventBatch
		_ = b.AppendKind(env.RunID, "", EventCostCapHit,
			map[string]any{"reason": "max_cost_usd_per_run", "limit": env.Budget.MaxCostUSDPerRun, "used": cost})
		_, _ = k.log.Append(b)
		return ErrBudgetExceeded
	}
	if env.Budget.MaxWallclockPerRunSecs > 0 && env.Counters.WallclockStart > 0 {
		elapsed := time.Now().UnixNano() - env.Counters.WallclockStart
		if elapsed > int64(env.Budget.MaxWallclockPerRunSecs)*int64(time.Second) {
			var b EventBatch
			_ = b.AppendKind(env.RunID, "", EventBudgetCapHit,
				map[string]any{"reason": "max_wallclock_per_run_seconds", "limit": env.Budget.MaxWallclockPerRunSecs})
			_, _ = k.log.Append(b)
			return ErrBudgetExceeded
		}
	}
	return nil
}

// ---- helpers ----

// buildEdges returns (in-edges, out-edges) maps. Edges entering a
// node inside a Loop/Retry/Review body are filtered out — the kernel
// does not walk those nodes directly.
func buildEdges(g *Graph) (in, out map[string][]Edge) {
	in = make(map[string][]Edge)
	out = make(map[string][]Edge)
	inside := bodyNodeIDs(g)
	for _, e := range g.Edges {
		if _, hidden := inside[e.To.Node]; hidden {
			continue
		}
		if _, hidden := inside[e.From.Node]; hidden {
			continue
		}
		in[e.To.Node] = append(in[e.To.Node], e)
		out[e.From.Node] = append(out[e.From.Node], e)
	}
	return
}

// bodyNodeIDs returns the set of node IDs that live inside a
// Loop/Retry body. Validator already covers the rule; we re-derive
// here for the kernel's own edge filter.
func bodyNodeIDs(g *Graph) map[string]struct{} {
	inside := make(map[string]struct{})
	for _, n := range g.Nodes {
		switch n.Kind {
		case NodeKindLoop:
			if a, ok := n.Attrs.(LoopAttrs); ok {
				for _, b := range a.Body {
					inside[b] = struct{}{}
				}
			}
		case NodeKindRetry:
			if a, ok := n.Attrs.(RetryAttrs); ok {
				for _, b := range a.Body {
					inside[b] = struct{}{}
				}
			}
		}
	}
	return inside
}

