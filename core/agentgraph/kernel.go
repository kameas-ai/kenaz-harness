package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// safeExecute invokes an executor with panic recovery. A buggy node (a
// wrong type assertion on node.Attrs, a nil-map write) or a panicking
// tool/seam would otherwise crash the entire harness process, because
// executors run inside bare goroutines (kernel dispatch, Parallel) with
// no recovery of their own. Here a panic is converted into an ordinary
// node-level error — logged with a full stack at ERROR — so the run
// fails cleanly and the rest of the app keeps running.
//
// Every site that calls Executor.Execute (the kernel's dispatch loop and
// the inline body/target dispatches in Loop/Retry/Parallel) routes
// through this wrapper.
func safeExecute(ctx context.Context, ex Executor, env *Env, node *Node, inputs PortValues) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			runID, sessID, nodeID, kind := "", "", "", ""
			if env != nil {
				runID, sessID = env.RunID, env.SessionID
			}
			if node != nil {
				nodeID, kind = node.ID, string(node.Kind)
			}
			logging.L().Error("agentgraph.node.panic",
				"run_id", runID,
				"session_id", sessID,
				"node_id", nodeID,
				"kind", kind,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(stack),
			)
			res = NewResult()
			err = fmt.Errorf("agentgraph: node %q (%s) panicked: %v", nodeID, kind, r)
		}
	}()
	return ex.Execute(ctx, env, node, inputs)
}

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
	runStart := time.Now()
	logging.L().Info("agentgraph.run.start",
		"run_id", env.RunID,
		"session_id", env.SessionID,
		"graph_id", env.Graph.ID,
		"nodes", len(env.Graph.Nodes),
		"entrypoints", env.Graph.Entrypoints,
	)
	defer func() {
		logging.L().Info("agentgraph.run.end",
			"run_id", env.RunID,
			"graph_id", env.Graph.ID,
			"duration_ms", time.Since(runStart).Milliseconds(),
		)
	}()
	applyEnvDefaults(env)

	// Fix: applyEnvDefaults's Hooks default needs the resolved memory
	// store, which we set above. Re-apply if it picked nilMemory.
	if env.Hooks == nil {
		env.Hooks = NewHookManager(env.Memory, env.SessionID, env.ProjectID)
		if env.JournalWriter != nil {
			env.Hooks.SetJournalWriter(env.JournalWriter)
		}
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
	// Emit one kind_alias_resolved event per (old → new) pair seen
	// during the wire decode of this graph (NFR-003 / WP08). The
	// once-per-process slog warning fires the first time a process
	// loads each alias; the EventLog event below fires every run so
	// the audit panel surfaces a per-run deprecation footprint.
	for _, ar := range env.Graph.AliasesSeen {
		_ = startBatch.AppendKind(env.RunID, "", EventKindAliasResolved, map[string]any{
			"old":         ar.Old,
			"new":         ar.New,
			"removal_in":  ar.RemovalIn,
			"graph_id":    env.Graph.ID,
		})
	}
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

	// inFlight + generation close a double-dispatch / dropped-dependency
	// hole in the backtrack in-degree recompute below (see the
	// "refire" block). A node whose in-degree just closed is normally
	// safe to dispatch immediately — but a sibling fan-out branch below
	// a backtrack target can already be executing (dispatched off a
	// path independent of the node that requested the rewind) at the
	// moment the recompute clears its completed bit and re-arms its
	// in-degree. Without tracking that a dispatch is already running,
	// the promote-downstream/backtrackReady paths can fire a second,
	// concurrent dispatch of the same node while the first is still
	// executing (double-dispatch — breaks MutatingTools serialization
	// for ToolNode), or the stale run's own completion can mark the
	// node done before the fresh run ever happens (dropped dependency).
	//
	// inFlight[id] is true from the moment a dispatch goroutine for id
	// is launched until that goroutine's own completion bookkeeping
	// runs. generation[id] is bumped every time a backtrack recompute
	// re-arms id (a member of the refire set); a dispatch captures the
	// generation value at launch time (myGen) and, on completion,
	// compares it against the current generation[id] — a mismatch
	// means a rewind superseded this run's inputs while it was still
	// executing, so its completion is a no-op for topology purposes
	// (no completed bit, no child promotion) rather than a real
	// completion. See the stale-generation guard inside dispatch.
	inFlight := make(map[string]bool, len(idx))
	generation := make(map[string]int, len(idx))

	// Concurrency: simple counting semaphore.
	sem := make(chan struct{}, k.maxInFlight)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	pause := false
	pauseReason := ""

	// TaskState population (autonomy-recovery-runtime-01PMDL03 WP05):
	// Goal / completed-steps / forbidden-actions only start earning
	// their place in the composed system prompt once this run has
	// actually hit trouble — a node error or an honored backtrack.
	// initialGoal is derived unconditionally (cheap: an in-memory/DB
	// history read already exercised elsewhere on this path) but is
	// never written to env.TaskState — and therefore never changes a
	// single byte of any composed prompt — unless armRecovery fires.
	// A run that never fails or backtracks never calls SetTaskGoal,
	// AddTaskCompletedStep, or AddTaskForbidden, so its system prompt
	// stays byte-identical to pre-WP05 behavior (see
	// TestKernel_ZeroFailureRunComposesByteIdenticalPrompt).
	initialGoal := firstUserTurnGoal(ctx, env)
	recoveryArmed := false
	armRecovery := func() {
		mu.Lock()
		already := recoveryArmed
		recoveryArmed = true
		mu.Unlock()
		if !already && initialGoal != "" {
			_ = k.SetTaskGoal(env, initialGoal)
		}
	}
	isRecoveryArmed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return recoveryArmed
	}

	var dispatch func(nodeID string, myGen int)
	dispatch = func(nodeID string, myGen int) {
		defer wg.Done()
		defer func() { <-sem }()
		// Backstop: safeExecute already wraps the executor call, but a
		// panic in the surrounding bookkeeping (event append, log write,
		// state mutation) would otherwise escape this bare goroutine and
		// crash the process. Convert it to a run-terminating firstErr.
		defer func() {
			if rec := recover(); rec != nil {
				logging.L().Error("agentgraph.dispatch.panic",
					"run_id", env.RunID, "node_id", nodeID,
					"panic", fmt.Sprintf("%v", rec), "stack", string(debug.Stack()))
				mu.Lock()
				inFlight[nodeID] = false
				if firstErr == nil {
					firstErr = fmt.Errorf("agentgraph: kernel: dispatch %q panicked: %v", nodeID, rec)
				}
				mu.Unlock()
			}
		}()

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

		// Per-node-fire log so the kernel's traversal is visible in
		// ~/.kenaz/harness.log without grepping the EventLog. The log
		// line is a sibling of EventNodeStart; the EventLog stays the
		// authoritative replay surface.
		fireStart := time.Now()
		inputPorts := make([]string, 0, len(inputs))
		for k := range inputs {
			inputPorts = append(inputPorts, k)
		}
		logging.L().Info("agentgraph.node.fire",
			"run_id", env.RunID,
			"session_id", env.SessionID,
			"node_id", nd.ID,
			"kind", string(nd.Kind),
			"title", nd.Title,
			"inputs", inputPorts,
		)

		r, err := safeExecute(ctx, ex, env, nd, inputs)
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
			logging.L().Warn("agentgraph.node.error",
				"run_id", env.RunID,
				"node_id", nd.ID,
				"kind", string(nd.Kind),
				"err", err.Error(),
				"duration_ms", time.Since(fireStart).Milliseconds(),
			)
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
			inFlight[nd.ID] = false
			isPause := errors.Is(err, ErrPaused)
			if firstErr == nil {
				if isPause {
					pause = true
					pauseReason = "paused"
				} else {
					firstErr = err
				}
			}
			mu.Unlock()

			// A node error is this run's TaskState recovery trigger
			// (unless it is just a nested Ask surfacing ErrPaused, which
			// is a normal pause, not a failure). A policy-gate deny is
			// additionally recorded as a forbidden action so a later
			// re-entry after backtrack/resume knows not to repeat it.
			if !isPause {
				armRecovery()
				var denial policyDenialError
				if errors.As(err, &denial) {
					if summary := strings.TrimSpace(denial.DeniedSummary()); summary != "" {
						_ = k.AddTaskForbidden(env, summary)
					}
				}
			}
			return
		}

		outputPorts := make([]string, 0, len(r.Outputs))
		for k := range r.Outputs {
			outputPorts = append(outputPorts, k)
		}
		logging.L().Info("agentgraph.node.complete",
			"run_id", env.RunID,
			"node_id", nd.ID,
			"kind", string(nd.Kind),
			"outputs", outputPorts,
			"events", len(r.Events.Events),
			"pause", r.Pause,
			"duration_ms", time.Since(fireStart).Milliseconds(),
		)
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

		// Stale-generation guard: a backtrack targeting an ancestor of
		// nd.ID may have fired — and re-armed nd.ID for a fresh run —
		// while THIS execution of nd.ID (captured at dispatch time as
		// myGen) was already in flight. That is the ordinary
		// sibling-fan-out case: nd.ID became ready via a path
		// independent of whatever node requested the rewind, so its
		// inputs here predate the rewind and are already superseded.
		// Treat this completion as a no-op for topology purposes —
		// no completed bit, no child promotion, no honoring of any
		// backtrack this stale run itself requests — rather than
		// letting stale data mark the node done (dropping the fresh
		// dependency) or racing a concurrent fresh dispatch of the
		// same node (double-dispatch). Freeing inFlight here is also
		// what the promote-downstream/backtrackReady launch sites are
		// waiting on, so re-check readiness before returning: if this
		// node's in-degree already closed while it was stuck in
		// flight, this is the only place left to fire the fresh run.
		mu.Lock()
		stale := generation[nd.ID] != myGen
		inFlight[nd.ID] = false
		if stale {
			var refireNow bool
			var refireGen int
			if !completed[nd.ID] && inDeg[nd.ID] <= 0 {
				inFlight[nd.ID] = true
				refireGen = generation[nd.ID]
				refireNow = true
			}
			mu.Unlock()
			if refireNow {
				sem <- struct{}{}
				wg.Add(1)
				go dispatch(nd.ID, refireGen)
			}
			return
		}
		mu.Unlock()

		// Resolve a typed backtrack request either from the executor's
		// Result.Backtrack field or (back-compat) the legacy
		// should_retry/retry_target Outputs convention ReviewNode's
		// fail path already emits (autonomy-recovery-runtime-01PMDL03
		// WP01). Validate the target + budget before touching any
		// kernel traversal state.
		var backtrackErr error
		var validBacktrack bool
		bt := resolveBacktrack(r)
		if bt != nil {
			if _, hidden := inside[bt.TargetNode]; hidden {
				backtrackErr = fmt.Errorf("agentgraph: kernel: backtrack target %q is inside a Loop/Retry body", bt.TargetNode)
			} else if _, ok := idx[bt.TargetNode]; !ok {
				backtrackErr = fmt.Errorf("agentgraph: kernel: backtrack target %q does not exist", bt.TargetNode)
			} else {
				used := env.Counters.AddBacktrack()
				maxBT := env.Budget.MaxBacktracksPerRun
				if maxBT <= 0 {
					maxBT = DefaultMaxBacktracksPerRun
				}
				if used > maxBT {
					k.emitCapHit(env, EventBudgetCapHit, "max_backtracks_per_run", float64(maxBT), float64(used))
					backtrackErr = ErrBudgetExceeded
				} else {
					validBacktrack = true
					ann := FailureAnnotation{
						Node:             bt.TargetNode,
						Reason:           bt.Reason,
						RejectedApproach: bt.RejectedApproach,
						Iteration:        used,
						Timestamp:        k.now(),
					}
					env.State.AddFailureAnnotation(ann)

					// An honored backtrack is this run's TaskState
					// recovery trigger (autonomy-recovery-runtime-
					// 01PMDL03 WP05): a rejected approach worth
					// remembering is exactly what Review/Reflect's fail
					// path just supplied via bt.RejectedApproach.
					armRecovery()
					if approach := strings.TrimSpace(bt.RejectedApproach); approach != "" {
						_ = k.AddTaskForbidden(env, approach)
					}

					var btBatch EventBatch
					_ = btBatch.AppendKind(env.RunID, nd.ID, EventBacktrackFired, map[string]any{
						"target":            bt.TargetNode,
						"reason":            bt.Reason,
						"rejected_approach": bt.RejectedApproach,
						"iteration":         used,
					})
					if _, lerr := k.log.Append(btBatch); lerr != nil {
						backtrackErr = fmt.Errorf("agentgraph: kernel: append backtrack batch: %w", lerr)
					}
				}
			}
		}

		mu.Lock()

		// refire holds TargetNode + everything downstream of it when a
		// backtrack was honored — these nodes lose their completed bit
		// and have their in-degree re-armed to count only edges from
		// other refire-set members (edges from outside the set are
		// already satisfied and don't need to fire again).
		var refire map[string]bool
		var backtrackReady bool
		if validBacktrack {
			refireSet := descendantsInclusive(bt.TargetNode, outEdges, inside)
			refire = make(map[string]bool, len(refireSet))
			for _, id := range refireSet {
				refire[id] = true
				delete(completed, id)
				// Bump the generation of every refire-set member. A
				// dispatch of one of these nodes that is currently in
				// flight (the sibling-fan-out case — see the
				// stale-generation guard above) was launched under an
				// older generation number; on its own completion it
				// will observe the mismatch and no-op instead of
				// marking the node done or promoting its children with
				// now-superseded outputs.
				generation[id]++
			}
			for _, id := range refireSet {
				n := 0
				for _, e := range inEdges[id] {
					if refire[e.From.Node] {
						// Edge from another refire-set member: it just
						// lost its completed bit above, so this
						// dependency is unsatisfied again regardless of
						// where it sits in the set.
						n++
					} else if !completed[e.From.Node] {
						// Edge from a node outside the refire set that
						// simply hasn't fired yet (a concurrent sibling
						// branch, e.g. root -> {target, W}, W -> this
						// node). Dropping this edge from the count — as
						// if every non-refire predecessor were already
						// satisfied — let a fan-out member become
						// "ready" and dispatch before W ever produced
						// its output, firing with a silently missing
						// input port. Completed non-refire predecessors
						// correctly contribute 0 (already satisfied,
						// no need to re-wait).
						n++
					}
				}
				inDeg[id] = n
			}
			backtrackReady = inDeg[bt.TargetNode] <= 0 && !inFlight[bt.TargetNode]
		}

		// nd.ID completed this firing UNLESS it is itself downstream of
		// the TargetNode it just requested a rewind to — in that case
		// it is part of the refire set and must re-fire once
		// TargetNode produces fresh outputs, so its completed bit stays
		// cleared.
		if !refire[nd.ID] {
			completed[nd.ID] = true
		}
		if backtrackErr != nil && firstErr == nil {
			firstErr = backtrackErr
		}

		// stepDone mirrors the completed[nd.ID] write above: true unless
		// nd.ID is itself part of the refire set (about to re-fire, not
		// actually done yet). Captured now — a local, goroutine-private
		// read of the `refire` map — for use after mu.Unlock() below;
		// AddTaskCompletedStep/isRecoveryArmed take their own locks and
		// must never be called while this dispatch's own mu is held.
		stepDone := !refire[nd.ID]

		if r.Pause {
			pause = true
			pauseReason = r.PauseReason
			mu.Unlock()
			if stepDone && isMeaningfulNodeKind(nd.Kind) && isRecoveryArmed() {
				_ = k.AddTaskCompletedStep(env, completedStepSummary(nd))
			}
			return
		}

		// Promote downstream nodes whose in-degree drops to 0. Skipped
		// when nd.ID is itself being rewound — its children's in-degree
		// was already set above by the refire-set computation and must
		// not be double-decremented here.
		//
		// A node whose in-degree closes here but is already inFlight
		// (dispatched off a path independent of nd.ID — the
		// sibling-fan-out race) is deliberately left alone: launching
		// it again now would run two concurrent executions of the same
		// node. Its own completion (the stale-generation guard above)
		// re-checks readiness once the in-flight slot frees up, so the
		// fresh run still happens exactly once.
		type launch struct {
			id  string
			gen int
		}
		var toLaunch []launch
		if !refire[nd.ID] {
			for _, e := range outEdges[nd.ID] {
				to := e.To.Node
				if _, hidden := inside[to]; hidden {
					continue
				}
				if completed[to] {
					continue
				}
				inDeg[to]--
				if inDeg[to] <= 0 && !inFlight[to] {
					inFlight[to] = true
					toLaunch = append(toLaunch, launch{id: to, gen: generation[to]})
				}
			}
		}
		if backtrackReady {
			inFlight[bt.TargetNode] = true
			toLaunch = append(toLaunch, launch{id: bt.TargetNode, gen: generation[bt.TargetNode]})
		}
		mu.Unlock()

		if stepDone && isMeaningfulNodeKind(nd.Kind) && isRecoveryArmed() {
			_ = k.AddTaskCompletedStep(env, completedStepSummary(nd))
		}

		for _, l := range toLaunch {
			id, gen := l.id, l.gen
			sem <- struct{}{}
			wg.Add(1)
			go dispatch(id, gen)
		}
	}

	// Claim inFlight for every entrypoint under mu before any goroutine
	// launches. Setting it inline in this loop (read: no lock) would
	// race against the very goroutines the earlier iterations spawn,
	// which mutate the same map under mu as soon as they complete.
	type seed struct {
		id  string
		gen int
	}
	seeds := make([]seed, 0, len(ready))
	mu.Lock()
	for _, id := range ready {
		inFlight[id] = true
		seeds = append(seeds, seed{id: id, gen: generation[id]})
	}
	mu.Unlock()
	for _, s := range seeds {
		sem <- struct{}{}
		wg.Add(1)
		go dispatch(s.id, s.gen)
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

// RebuildState walks the EventLog and re-derives env.State (and, as of
// autonomy-recovery-runtime-01PMDL03 WP03, env.TaskState) from the
// recorded events. It does NOT re-fire any executors — the durable
// execution model says once a node has completed in the log, we treat
// its recorded outputs as authoritative on resume (NFR-007 documents
// the LLM-token-level relaxation).
//
// In v1 we only track the completion bit for each node. The actual
// port values are not stored in the event payload (storing every
// LLM response verbatim explodes the log size). On resume the kernel
// re-fires unfinished nodes; completed ones are skipped.
//
// TaskState reconstruction (WP03):
//   - FailureAnnotations (and therefore TaskState's rendered
//     FailedAttempts view) are rebuilt by replaying EventBacktrackFired.
//     This also fixes a WP01 gap: before this change, backtrack
//     annotations were never restored on resume at all — a resumed run
//     silently lost the "why was this rewound" memory the whole
//     mechanism exists to preserve.
//   - Goal (EventTaskGoalSet), the completed-step summary
//     (EventTaskStepCompleted), and forbidden actions
//     (EventTaskForbidden) are rebuilt verbatim from their own events.
//     These are the fields SetTaskGoal / AddTaskCompletedStep /
//     AddTaskForbidden persist below.
//
// What does NOT survive resume: nothing, by design — every TaskState
// dimension that can be mutated has a corresponding event and a replay
// case here. The one caveat is FailureAnnotation.Timestamp, which on
// replay reflects the EventLog row's wall-clock Timestamp rather than
// the original kernel-clock value passed to WithClock in tests (the
// backtrack_fired payload itself does not carry a timestamp field) —
// immaterial in production (both are real wall-clock time) but worth
// knowing if a test pins an exact mocked timestamp across a resume.
// RebuildState replays a run's EventLog into a fresh env's RunState +
// TaskState after a process restart or resumed pause. Alongside the
// pre-existing TaskState/backtrack-annotation replay cases, this also
// reconstructs EscalationLadder progress: without this, a resumed run
// would silently reset every ladder node back to rung 0 (retry) and
// clear the run-global escalation-used flag, so a paused-and-resumed
// run could re-escalate (burning the shared slot a second time) or
// re-ask a question the human already answered before the pause.
//
// ladderAnswered tracks, per node, whether an EventAskAnswered has
// already been observed for it in the replay so a later EventLadderRung
// "ask" entry (which the executor also appends alongside
// EventAskAnswered, see exec_escalation_ladder.go) is reconstructed as
// the exhausted rung rather than re-armed as still-pending. This
// relies on the EventLog preserving strict append order per run
// (confirmed in memEventLog.Append/Replay), since EventAskAnswered is
// always appended before the "ask" EventLadderRung entry it pairs
// with.
func (k *Kernel) RebuildState(env *Env) error {
	if env.State == nil {
		env.State = NewRunState()
	}
	if env.TaskState == nil {
		env.TaskState = NewTaskState()
	}
	ladderAnswered := make(map[string]bool)
	return k.log.Replay(env.RunID, func(e Event) error {
		switch e.Kind {
		case EventNodeComplete:
			if e.NodeID != "" {
				// We don't have the original outputs in payload — replay
				// is conservative: mark complete but leave outputs nil.
				// Re-firing downstream of a re-fired node is the kernel's
				// job; we surface completion via env.State.completed.
				env.State.SetOutputs(e.NodeID, PortValues{})
			}
		case EventNodeError:
			if e.NodeID != "" {
				env.State.MarkFailed(e.NodeID, errors.New(string(e.Payload)))
			}
		case EventBacktrackFired:
			var payload struct {
				Target           string `json:"target"`
				Reason           string `json:"reason"`
				RejectedApproach string `json:"rejected_approach"`
				Iteration        int    `json:"iteration"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil {
				env.State.AddFailureAnnotation(FailureAnnotation{
					Node:             payload.Target,
					Reason:           payload.Reason,
					RejectedApproach: payload.RejectedApproach,
					Iteration:        payload.Iteration,
					Timestamp:        e.Timestamp,
				})
			}
		case EventTaskGoalSet:
			var payload struct {
				Goal string `json:"goal"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil {
				env.TaskState.SetGoal(payload.Goal)
			}
		case EventTaskStepCompleted:
			var payload struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil {
				env.TaskState.AddCompletedStep(payload.Summary)
			}
		case EventTaskForbidden:
			var payload struct {
				Actions []string `json:"actions"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil {
				env.TaskState.AddForbidden(payload.Actions...)
			}
		case EventAskAnswered:
			if e.NodeID != "" {
				ladderAnswered[e.NodeID] = true
			}
		case EventLadderRung:
			if e.NodeID == "" {
				return nil
			}
			var payload struct {
				Rung    string `json:"rung"`
				Attempt int    `json:"attempt"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				return nil
			}
			stateKey := ladderStateKey(e.NodeID)
			prior := env.State.Outputs(stateKey)
			retryAttempts, _ := prior["retry_attempts"].(int)
			var rung int
			switch payload.Rung {
			case "retry":
				rung = ladderRungRetry
				retryAttempts = payload.Attempt
			case "escalate":
				rung = ladderRungEscalate
			case "replan":
				rung = ladderRungReplan
			case "ask":
				if ladderAnswered[e.NodeID] {
					rung = ladderRungExhausted
				} else {
					rung = ladderRungAsk
				}
			default:
				return nil
			}
			env.State.SetOutputs(stateKey, PortValues{"rung": rung, "retry_attempts": retryAttempts})
		case EventEscalateTriggered:
			env.State.SetOutputs(ladderGlobalEscalatedKey, PortValues{"fired": true})
		}
		return nil
	})
}

// SetTaskGoal updates env.TaskState's goal and durably persists the
// change (EventTaskGoalSet) so it survives Kernel.RebuildState on
// resume (autonomy-recovery-runtime-01PMDL03 WP03). Callable mid-run
// by anything with access to the owning Kernel + Env — a future
// escalation-ladder/replan controller (WP04) is the expected caller;
// no built-in executor calls this yet (see the WP03 completion report
// for what is and isn't wired).
func (k *Kernel) SetTaskGoal(env *Env, goal string) error {
	if env.TaskState == nil {
		env.TaskState = NewTaskState()
	}
	env.TaskState.SetGoal(goal)
	var b EventBatch
	if err := b.AppendKind(env.RunID, "", EventTaskGoalSet, map[string]any{"goal": goal}); err != nil {
		return fmt.Errorf("agentgraph: kernel: SetTaskGoal: %w", err)
	}
	if _, err := k.log.Append(b); err != nil {
		return fmt.Errorf("agentgraph: kernel: SetTaskGoal: append: %w", err)
	}
	return nil
}

// AddTaskCompletedStep appends a completed-step summary line to
// env.TaskState and durably persists it (EventTaskStepCompleted) so it
// survives resume. A no-op for an empty summary.
func (k *Kernel) AddTaskCompletedStep(env *Env, summary string) error {
	if summary == "" {
		return nil
	}
	if env.TaskState == nil {
		env.TaskState = NewTaskState()
	}
	env.TaskState.AddCompletedStep(summary)
	var b EventBatch
	if err := b.AppendKind(env.RunID, "", EventTaskStepCompleted, map[string]any{"summary": summary}); err != nil {
		return fmt.Errorf("agentgraph: kernel: AddTaskCompletedStep: %w", err)
	}
	if _, err := k.log.Append(b); err != nil {
		return fmt.Errorf("agentgraph: kernel: AddTaskCompletedStep: append: %w", err)
	}
	return nil
}

// AddTaskForbidden merges one or more forbidden-action names into
// env.TaskState and durably persists them (EventTaskForbidden) so they
// survive resume. A no-op when actions is empty.
func (k *Kernel) AddTaskForbidden(env *Env, actions ...string) error {
	if len(actions) == 0 {
		return nil
	}
	if env.TaskState == nil {
		env.TaskState = NewTaskState()
	}
	env.TaskState.AddForbidden(actions...)
	var b EventBatch
	if err := b.AppendKind(env.RunID, "", EventTaskForbidden, map[string]any{"actions": actions}); err != nil {
		return fmt.Errorf("agentgraph: kernel: AddTaskForbidden: %w", err)
	}
	if _, err := k.log.Append(b); err != nil {
		return fmt.Errorf("agentgraph: kernel: AddTaskForbidden: append: %w", err)
	}
	return nil
}

// maxTaskGoalRunes caps the goal text firstUserTurnGoal derives from
// the session's first user turn. A run's Goal only ever reaches the
// composed system prompt once armRecovery fires (Run's dispatch
// closure), but it should still stay bounded so a very long opening
// message doesn't blow the token budget once it does.
const maxTaskGoalRunes = 400

// firstUserTurnGoal derives a run's TaskState goal from the session's
// first user-role message, read through the same HistoryReader seam
// modelExecutor already falls back to for multi-turn chat
// (exec_compute.go). Returns "" when history is unavailable, empty, or
// has no user turn — callers must treat that as "no goal available"
// rather than an error; TaskState.Goal defaulting to "" is the
// harmless no-op renderTaskState already handles.
//
// Called unconditionally at the top of every Run, but its result is
// only ever committed to env.TaskState by Run's armRecovery closure —
// which fires only once this run has actually hit a node error or an
// honored backtrack — so a zero-failure run never calls SetTaskGoal
// and its composed prompt stays byte-identical to pre-WP05 behavior.
func firstUserTurnGoal(ctx context.Context, env *Env) string {
	if env == nil || env.History == nil || env.SessionID == "" {
		return ""
	}
	msgs, err := env.History.History(ctx, env.SessionID, 0)
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		if !strings.EqualFold(m.Role, "user") {
			continue
		}
		goal := strings.TrimSpace(m.Content)
		if goal == "" {
			return ""
		}
		if r := []rune(goal); len(r) > maxTaskGoalRunes {
			goal = string(r[:maxTaskGoalRunes]) + "…"
		}
		return goal
	}
	return ""
}

// policyDenialError is a narrow structural interface an error returned
// through the PolicyGate seam may satisfy to signal an explicit
// Cedar-style deny decision, as opposed to an ordinary I/O failure.
// *cedar.PolicyDeniedError (core/policy/cedar/engine.go) satisfies
// this via its DeniedSummary method. Matched here with errors.As
// rather than a direct type assertion / import: core/agentgraph
// intentionally does not depend on core/policy/cedar (see the
// PolicyGate doc in seams.go) — the kernel only needs enough of the
// error's shape to log a forbidden-action summary, not the concrete
// Decision type.
type policyDenialError interface {
	error
	// DeniedSummary returns a short human-readable description of the
	// denied action (e.g. "Read file:/etc/passwd"), suitable for
	// TaskState.AddForbidden.
	DeniedSummary() string
}

// nonMeaningfulTaskStepKinds are node kinds whose completion is pure
// graph plumbing (control flow, telemetry, pause/resume bookkeeping)
// rather than a step a recovering model call would benefit from being
// reminded of. Everything else counts as "meaningful" for
// AddTaskCompletedStep — deliberately an inclusive default (deny-list,
// not an allow-list) so a new node kind added later still gets logged
// without this switch needing an update.
var nonMeaningfulTaskStepKinds = map[NodeKind]bool{
	NodeKindDecision:         true,
	NodeKindJoin:             true,
	NodeKindLoop:             true,
	NodeKindRetry:            true,
	NodeKindBranch:           true,
	NodeKindMerge:            true,
	NodeKindParallel:         true,
	NodeKindCheckpoint:       true,
	NodeKindTraceWrite:       true,
	NodeKindAttachment:       true,
	NodeKindHistoryRead:      true,
	NodeKindAsk:              true,
	NodeKindCompact:          true,
	NodeKindSleep:            true,
	NodeKindEscalationLadder: true,
}

// isMeaningfulNodeKind reports whether kind's completion is worth a
// TaskState completed-step entry (see nonMeaningfulTaskStepKinds).
func isMeaningfulNodeKind(kind NodeKind) bool {
	return !nonMeaningfulTaskStepKinds[kind]
}

// completedStepSummary renders the short, human-readable line
// AddTaskCompletedStep stores for nd's completion — the node's title
// when the author set one (more meaningful than a raw ID), falling
// back to its ID, paired with its kind for disambiguation.
func completedStepSummary(nd *Node) string {
	label := strings.TrimSpace(nd.Title)
	if label == "" {
		label = nd.ID
	}
	return fmt.Sprintf("%s (%s)", label, nd.Kind)
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

// ---- Backtrack primitive (autonomy-recovery-runtime-01PMDL03 WP01) ----
//
// The kernel is otherwise a strict DAG walk keyed by in-degree: once a
// node lands in the `completed` set (kernel.go dispatch closure) its
// outgoing edges are never re-promoted (`if completed[to] { continue }`).
// BacktrackRequest is the explicit, typed escape hatch: an executor
// asks the kernel to clear an earlier node's completed flag and
// re-fire it (and everything downstream of it), instead of walking
// forward only.

// BacktrackRequest is the kernel-level rewind signal. A compute
// executor sets Result.Backtrack to ask the kernel to clear
// TargetNode's completed flag, re-queue it, and re-fire everything
// downstream of it once it produces fresh outputs.
//
// This is the typed successor to the legacy convention ReviewNode's
// fail path already emits — res.Outputs["should_retry"] = true and
// res.Outputs["retry_target"] = a.UpstreamNode (exec_compute.go) — a
// signal that, before this WP, nothing read. The kernel honors both:
// new node kinds should set Backtrack directly; resolveBacktrack below
// still translates the legacy Outputs convention so ReviewNode did not
// need to change.
type BacktrackRequest struct {
	// TargetNode is the node ID to rewind to. Must be a node the
	// kernel walks directly (not hidden inside a Loop/Retry body).
	TargetNode string
	// Reason is a short human-readable explanation of why the prior
	// attempt was rejected (e.g. a review verdict's text).
	Reason string
	// RejectedApproach captures what was actually tried and should not
	// be repeated verbatim (e.g. the rejected draft).
	RejectedApproach string
}

// FailureAnnotation is the structured record of why a backtracked
// node's prior attempt was rejected. The kernel injects one on every
// honored backtrack and appends it to RunState; graphBaseOf
// (exec_compute.go) renders the accumulated list into every compute
// executor's SystemPrompt ahead of the model call — a pinned system
// message, never a bare user-role string like reflectExecutor's
// "revision" output.
//
// Because it rides in SystemPrompt (not the Messages slice), it is
// naturally exempt from compaction: CompactionInput.SystemPrompt is
// documented as "preserved across compaction"
// (core/agentgraph/compaction/compactor.go) and no compaction strategy
// folds it into the prunable message slice — no new pinning mechanism
// was needed.
type FailureAnnotation struct {
	// Node is the target node the backtrack rewound to.
	Node string `json:"node"`
	// Reason is why the prior attempt was rejected.
	Reason string `json:"reason"`
	// RejectedApproach is what was tried and must not be repeated.
	RejectedApproach string `json:"rejected_approach"`
	// Iteration is this run's 1-based backtrack sequence number
	// (mirrors RunCounters.BacktracksUsed at the time it fired).
	Iteration int `json:"iteration"`
	// Timestamp is when the backtrack fired (kernel clock, so tests
	// with WithClock get deterministic values).
	Timestamp time.Time `json:"timestamp"`
}

// DefaultMaxBacktracksPerRun is the ceiling applied when
// Budget.MaxBacktracksPerRun is unset (<= 0). Unlike the other Budget
// fields, backtrack has no "0 == unlimited" escape hatch — re-firing a
// completed node is inherently unsafe unbounded, so a cap always
// applies (spec risk: "the backtrack primitive must be explicit and
// bounded ... to avoid infinite rewind").
const DefaultMaxBacktracksPerRun = 3

// resolveBacktrack extracts a typed BacktrackRequest from a node's
// Result, preferring the explicit Result.Backtrack field and falling
// back to the legacy should_retry/retry_target Outputs convention
// ReviewNode's fail path emits. Returns nil when neither is present.
func resolveBacktrack(r Result) *BacktrackRequest {
	if r.Backtrack != nil {
		return r.Backtrack
	}
	shouldRetry, _ := r.Outputs["should_retry"].(bool)
	if !shouldRetry {
		return nil
	}
	target, _ := r.Outputs["retry_target"].(string)
	if target == "" {
		return nil
	}
	reason := "verification failed"
	if v, ok := r.Outputs["verdict"].(map[string]any); ok {
		if txt, ok := v["text"].(string); ok && txt != "" {
			reason = txt
		}
	}
	rejected, _ := r.Outputs["approved"].(string)
	return &BacktrackRequest{
		TargetNode:       target,
		Reason:           reason,
		RejectedApproach: rejected,
	}
}

// descendantsInclusive returns TargetNode's ID plus every node
// reachable from it via outEdges (excluding nodes hidden inside a
// Loop/Retry body), i.e. the set the kernel must clear from `completed`
// and re-arm the in-degree of when honoring a backtrack. The graph is
// a DAG (validated at load time), so this terminates and TargetNode
// itself can never appear as its own descendant.
func descendantsInclusive(target string, outEdges map[string][]Edge, inside map[string]struct{}) []string {
	seen := map[string]bool{target: true}
	order := []string{target}
	queue := []string{target}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range outEdges[cur] {
			to := e.To.Node
			if _, hidden := inside[to]; hidden {
				continue
			}
			if seen[to] {
				continue
			}
			seen[to] = true
			order = append(order, to)
			queue = append(queue, to)
		}
	}
	return order
}

// PauseMarker is the EventBudgetCapHit / EventCostCapHit payload
// extension introduced by WP17 (cap-hit pause-not-kill). It carries
// the resume token the user will hand back via BumpAndResume to
// revalidate the run against a freshly-bumped cap.
//
// On cap hit the kernel emits EventBudgetCapHit (or EventCostCapHit)
// with this payload AND returns ErrBudgetExceeded. The caller treats
// the run as paused — RebuildState replays the marker on Resume and
// the kernel re-checks the (now larger) cap before re-firing the
// budget-blocked node.
type PauseMarker struct {
	Reason      string  `json:"reason"`
	Limit       float64 `json:"limit"`
	Used        float64 `json:"used"`
	ResumeToken string  `json:"resume_token"`
	// PausePending is true when the run can be resumed once the
	// dial is bumped. Always true for current cap-hit emissions —
	// reserved for future "fatal cap" semantics.
	PausePending bool `json:"pause_pending_cap_increase"`
}

// checkBudget enforces the per-run hard caps before each fire. Returns
// ErrBudgetExceeded if any cap is exceeded; the EventLog row carries
// a PauseMarker so the resume path can revalidate against a bumped
// cap (WP17 — cap-hit pause-not-kill).
func (k *Kernel) checkBudget(env *Env) error {
	if env.Counters == nil {
		return nil
	}
	tokens, calls, tools, cost := env.Counters.Snapshot()
	if env.Budget.MaxTokensPerRun > 0 && tokens > env.Budget.MaxTokensPerRun {
		k.emitCapHit(env, EventBudgetCapHit, "max_tokens_per_run",
			float64(env.Budget.MaxTokensPerRun), float64(tokens))
		return ErrBudgetExceeded
	}
	if env.Budget.MaxLLMCallsPerRun > 0 && calls > env.Budget.MaxLLMCallsPerRun {
		k.emitCapHit(env, EventBudgetCapHit, "max_llm_calls_per_run",
			float64(env.Budget.MaxLLMCallsPerRun), float64(calls))
		return ErrBudgetExceeded
	}
	if env.Budget.MaxToolCallsPerRun > 0 && tools > env.Budget.MaxToolCallsPerRun {
		k.emitCapHit(env, EventBudgetCapHit, "max_tool_calls_per_run",
			float64(env.Budget.MaxToolCallsPerRun), float64(tools))
		return ErrBudgetExceeded
	}
	if env.Budget.MaxCostUSDPerRun > 0 && cost > env.Budget.MaxCostUSDPerRun {
		k.emitCapHit(env, EventCostCapHit, "max_cost_usd_per_run",
			env.Budget.MaxCostUSDPerRun, cost)
		return ErrBudgetExceeded
	}
	if env.Budget.MaxWallclockPerRunSecs > 0 && env.Counters.WallclockStart > 0 {
		elapsed := time.Now().UnixNano() - env.Counters.WallclockStart
		if elapsed > int64(env.Budget.MaxWallclockPerRunSecs)*int64(time.Second) {
			k.emitCapHit(env, EventBudgetCapHit, "max_wallclock_per_run_seconds",
				float64(env.Budget.MaxWallclockPerRunSecs), float64(elapsed)/float64(time.Second))
			return ErrBudgetExceeded
		}
	}
	return nil
}

// emitCapHit appends a cap-hit event with a PauseMarker. The resume
// token is the run id + a deterministic suffix per reason — the
// frontend echoes it back via BumpAndResume so the kernel can
// match resume-without-bump as a no-op (the marker is still in the
// log).
func (k *Kernel) emitCapHit(env *Env, kind EventKind, reason string, limit, used float64) {
	marker := PauseMarker{
		Reason:       reason,
		Limit:        limit,
		Used:         used,
		ResumeToken:  env.RunID + ":" + reason,
		PausePending: true,
	}
	var b EventBatch
	_ = b.AppendKind(env.RunID, "", kind, marker)
	_, _ = k.log.Append(b)
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

