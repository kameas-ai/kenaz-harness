// Command harness-vm is the in-VM kenaz-harness RPC service for Phase 8.
//
// It listens on a TCP port (standing in for a vsock listener — vsock
// framing deferred to a later phase), accepts long-lived per-workbench
// connections, and multiplexes task RPCs over each connection.
//
// Wire contract: see kenaz-harness/contracts/vm-rpc.md (Phase 8 section).
//
// Build with:
//
//	go build -o bin/kenaz-harness-vm ./cmd/harness-vm/
//
// Auth: client must send {"kind":"auth","token":"<HARNESS_VM_TOKEN>"}
// as the first message on each connection. Server replies
// {"kind":"auth.ok"} or closes the connection with {"kind":"auth.error"}.
// When HARNESS_VM_TOKEN is set (the baked-image / production path) the token
// is REQUIRED and validated with a constant-time compare; a mismatch is
// rejected. An unset HARNESS_VM_TOKEN leaves the handshake unauthenticated
// for LOCAL DEV ONLY (the baked image always sets it ⇒ deny-by-default in
// production). The token is never logged.
//
// Task lifecycle (per connection, one active task at a time):
//
//	C→S: {"kind":"task.start","task_id":"<id>","prompt":"<prompt>"}
//	S→C: {"kind":"task.running","task_id":"<id>","text":"<chunk>"}  (0..N)
//	S→C: {"kind":"task.complete","task_id":"<id>"}                  (terminal)
//
// Cancel path:
//
//	C→S: {"kind":"task.cancel","task_id":"<id>"}
//	S→C: {"kind":"task.cancelled","task_id":"<id>"}                 (terminal)
//
// Conflict (second task.start while one is running):
//
//	S→C: {"kind":"task.error","task_id":"<id>","code":"task_conflict","message_truncated":"..."}
//
// Port: 7881 TCP (or $HARNESS_VM_PORT). Phase 9 upgrades to vsock.
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/paths"
)

const defaultHarnessPort = "7881"

// maxMessageLen caps the message_truncated field to this many runes.
// Error diagnostics (provider status + reason, e.g. Anthropic's
// "invalid request (status=400): ...") are provider-generated, not user
// prompt content, and 64 runes clipped them right at "(status=" — making
// every agent-run failure undebuggable from the host. 512 is enough to carry
// a full API error while staying bounded.
const maxMessageLen = 512

func main() {
	addr := "0.0.0.0:" + defaultHarnessPort
	if p := os.Getenv("HARNESS_VM_PORT"); p != "" {
		addr = "0.0.0.0:" + p
	}

	token := os.Getenv("HARNESS_VM_TOKEN")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Auth posture. When HARNESS_VM_TOKEN is set (the baked-image / production
	// path), the dispatch handshake is REQUIRED: every connection's auth token
	// is validated with a constant-time compare and a mismatch is rejected. When
	// the env is empty (local dev only) the handshake stays unauthenticated.
	// NEVER log the token itself — only whether one is configured.
	if token != "" {
		log.Info("kenaz-harness-vm: dispatch auth REQUIRED (HARNESS_VM_TOKEN set)")
	} else {
		log.Warn("kenaz-harness-vm: dispatch auth DISABLED (HARNESS_VM_TOKEN unset) — local dev only")
	}

	// Ledger emitter: when SIGIL_INGEST_SOCKET names the in-VM reporter
	// ingest endpoint, task lifecycle (start/tool_call/complete) is pushed
	// there for the host audit timeline (criterion #5 / finding #11). When
	// unset, emission is a no-op and the task surface runs unchanged.
	ledger := newLedgerEmitter(
		os.Getenv(ledgerSocketEnv),
		"", // default network ("unix")
		os.Getenv("SIGIL_WORKBENCH_ID"),
		log,
	)
	if ledger.enabled() {
		log.Info("kenaz-harness-vm: ledger emission enabled", "ingest_socket", ledger.addr)
	} else {
		log.Info("kenaz-harness-vm: ledger emission disabled (SIGIL_INGEST_SOCKET unset)")
	}

	// Audit sink: when KENAZ_HARNESS_EVENT_SOCK names the reporter collector's
	// audit socket, each task lifecycle transition (start / tool_call /
	// tool_result / complete) is written there as one metadata-only JSON line
	// (contracts/harness-audit-events.md). When unset the sink is a no-op and
	// the task surface runs unchanged (dev / host mode). NEVER blocks a task.
	audit := newAuditSink(os.Getenv(auditSocketEnv), "", log)
	if audit.enabled() {
		log.Info("kenaz-harness-vm: audit emission enabled", "event_sock", audit.addr)
	} else {
		log.Info("kenaz-harness-vm: audit emission disabled (KENAZ_HARNESS_EVENT_SOCK unset)")
	}

	// Read surface (Phase G): the sessions / tools / memory / workflows /
	// providers queries the kenaz host renders in its IDE-merger views. Bootstrap
	// is best-effort — a failure (e.g. a locked or absent data dir) leaves the
	// task surface fully functional and read RPCs respond code:"unavailable".
	// HARNESS_READ_DATADIR overrides the resolved data dir (tests / host dev).
	readDataDir := os.Getenv("HARNESS_READ_DATADIR")
	if readDataDir == "" {
		if dd, derr := paths.DataDir(); derr == nil {
			readDataDir = dd
		}
	}
	reads, rerr := newReadService(context.Background(), readDataDir, log)
	if rerr != nil {
		log.Warn("kenaz-harness-vm: read surface disabled (bootstrap failed)", "err", rerr, "data_dir", readDataDir)
		reads = &readService{log: log} // nil api → reads answer code:"unavailable"
	} else {
		log.Info("kenaz-harness-vm: read surface enabled", "data_dir", readDataDir)
	}

	// Approval brokering (spec 074 task 4.C1/4.C2). The gate is the cedar
	// prompt registry the in-process chassis ALREADY built — the same
	// singleton every gate site and the served (:7880) permission modal use.
	// We attach a listener to it; we do not build a second one.
	//
	// A failed chassis bootstrap means there is no engine in this process and
	// therefore no gate. The capability is then never granted, and the host
	// renders its "approvals not brokered on this workbench" state — which is
	// the honest answer, and specifically not an empty pending list (that is
	// indistinguishable from "nothing is waiting").
	var connOpts []connOption
	if promptReg := reads.promptRegistry(); promptReg != nil {
		connOpts = append(connOpts, withApprovalRegistry(promptReg, promptReg))
		log.Info("kenaz-harness-vm: approval brokering available (negotiate capability \"approval\")")
	} else {
		log.Info("kenaz-harness-vm: approval brokering unavailable (no cedar gate in this process)")
	}

	// Agent execution (Spec 058): REAL model-backed by default, resolved from
	// the in-VM environment (agentexec.go). KENAZ_AGENT_EXEC=stub keeps the
	// offline echo graph for CI; a real mode with no resolvable credential
	// fails every task with a named error — never a silent echo.
	//
	// reads.policyGuard() is the process's real Cedar gate (same singleton
	// promptRegistry() above attaches to) — threading it here closes HV-03:
	// prior to this, the registry.Options literal in agentexec.go left
	// Policy unset and silently ran under llm.AllowAllGuard{}, which can
	// never refuse (mission vm-execution-surface-truth-01PMZD14 UNIT-1).
	exec := resolveAgentExecutor(log, reads.policyGuard())

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("kenaz-harness-vm: listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	log.Info("kenaz-harness-vm listening", "addr", addr)

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		log.Info("kenaz-harness-vm: shutting down")
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Info("kenaz-harness-vm: accept loop exiting", "reason", err)
			return
		}
		go handleConn(log, conn, token, exec, ledger, audit, reads, connOpts...)
	}
}

// msg is a generic inbound or outbound message.
type msg map[string]any

// connWriter serialises concurrent writes to a net.Conn.
// handleConn and runTask goroutines both write to the same conn;
// unprotected concurrent writes corrupt the NDJSON stream.
type connWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (w *connWriter) send(m msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	_, err = w.conn.Write(b)
	w.mu.Unlock()
	return err
}

// connOption configures optional per-connection dependencies. It exists as a
// variadic tail so surfaces added after Phase 8 (spec 074's approval bridge is
// the first) do not churn handleConn's signature — and so the pre-existing
// call sites, tests included, keep compiling untouched.
type connOption func(*connConfig)

type connConfig struct {
	// approvals is the process's single cedar prompt registry. nil means this
	// process has no gate to broker, in which case the `approval` capability
	// is never granted and the wire stays exactly as it was.
	approvals approvalRegistry
	// approvalGate is the registry as the engine-side park seam. Held
	// separately because approvalRegistry deliberately excludes
	// RequestInteractive — listening to the gate and blocking on it are
	// different privileges.
	approvalGate approvalGate
}

// withApprovalRegistry wires the :7881 approval surface to the process's
// existing cedar gate. Both arguments are normally the SAME *cedar.Registry.
func withApprovalRegistry(reg approvalRegistry, gate approvalGate) connOption {
	return func(c *connConfig) {
		c.approvals = reg
		c.approvalGate = gate
	}
}

// handleConn manages the full lifecycle of one client connection:
// auth handshake, then a loop dispatching task messages.
func handleConn(log *slog.Logger, conn net.Conn, token string, exec agentExecutor, ledger *ledgerEmitter, audit *auditSink, reads *readService, opts ...connOption) {
	defer func() { _ = conn.Close() }()

	var cfg connConfig
	for _, o := range opts {
		o(&cfg)
	}

	w := &connWriter{conn: conn}
	scanner := bufio.NewScanner(conn)

	// --- Auth handshake ---
	if !scanner.Scan() {
		return // closed before auth
	}
	var authMsg msg
	if err := json.Unmarshal(scanner.Bytes(), &authMsg); err != nil {
		_ = w.send(msg{"kind": "auth.error", "message_truncated": "bad json"})
		return
	}
	if authMsg["kind"] != "auth" {
		_ = w.send(msg{"kind": "auth.error", "message_truncated": "expected auth message"})
		return
	}
	// When a token is configured (the baked-image / production path), the
	// dispatch handshake is REQUIRED: the client's token must match. The
	// comparison is constant-time (crypto/subtle) so a rejected handshake leaks
	// no timing signal about how many leading bytes matched. The token is NEVER
	// logged or echoed back. An empty configured token (local dev only) skips
	// validation entirely — deny-by-default holds in production because the
	// baked image always sets HARNESS_VM_TOKEN.
	if token != "" {
		clientToken, _ := authMsg["token"].(string)
		if subtle.ConstantTimeCompare([]byte(clientToken), []byte(token)) != 1 {
			_ = w.send(msg{"kind": "auth.error", "message_truncated": "invalid token"})
			return
		}
	}
	// --- Capability negotiation (spec 074 task 4.C1) ---
	//
	// One optional key each way. A client that sends no `capabilities` gets an
	// auth.ok with no `capabilities` — the single-key object this wire has
	// always emitted, byte for byte. A client that asks for something this
	// build cannot honour gets it omitted from the grant rather than echoed,
	// because auth.ok carries the GRANTED subset, not an echo of the request.
	granted := negotiateCapabilities(authMsg["capabilities"], map[string]bool{
		capabilityApproval: cfg.approvals != nil,
	})
	authOK := msg{"kind": "auth.ok"}
	if len(granted) > 0 {
		authOK["capabilities"] = granted
	}
	if err := w.send(authOK); err != nil {
		return
	}

	// The approval bridge exists only when `approval` was granted. Everywhere
	// below, a nil bridge is the "not negotiated" path and emits nothing —
	// the fail-safe direction is silence, and the gate still resolves at the
	// harness's own served (:7880) modal regardless.
	var approvals *approvalBridge
	if hasCapability(granted, capabilityApproval) {
		approvals = newApprovalBridge(w, log)
		removeDispatcher := cfg.approvals.AddDispatcher(approvals)
		removeObserver := cfg.approvals.AddResolutionObserver(approvals)
		defer removeDispatcher()
		defer removeObserver()
	}

	// --- Per-connection session: one active task at a time ---
	//
	// busy == 1  → a task goroutine is running.
	// busy == 0  → connection is idle, ready for a new task.start.
	var busy atomic.Int32

	// cancelFn holds the cancel function for the currently-running task.
	// Access is safe: cancelFn is written by handleConn's scanner loop before
	// busy is set, and read only in the cancel branch of the same loop.
	// Since task.start and task.cancel are processed sequentially in the same
	// goroutine, no concurrent write can race with the cancel read.
	var cancelFn context.CancelFunc

	for scanner.Scan() {
		var m msg
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			_ = w.send(msg{
				"kind":              "task.error",
				"code":              "bad_request",
				"message_truncated": truncate(fmt.Sprintf("json decode: %v", err), maxMessageLen),
			})
			continue
		}

		kind, _ := m["kind"].(string)

		// Phase G read RPCs are synchronous request/response and independent of
		// the task busy-guard — a read can be served while a task streams. They
		// are routed here, before the task dispatcher, so an unknown kind still
		// falls through to the default bad_request branch below.
		if isReadKind(kind) {
			_ = w.send(reads.handle(context.Background(), kind, m))
			continue
		}

		switch kind {
		case "task.start":
			taskID, _ := m["task_id"].(string)
			if taskID == "" {
				_ = w.send(msg{
					"kind":              "task.error",
					"code":              "bad_request",
					"message_truncated": "task_id required",
				})
				continue
			}

			// Optional run_params (kenaz.harness.run-control). Absent run_params
			// ⇒ today's exact behaviour: params.isZero() and presetSteps==nil.
			// Validation + preset resolution happen BEFORE the busy guard so a
			// malformed request is rejected without ever marking the connection
			// busy (a subsequent well-formed task.start proceeds immediately).
			params, hasParams := parseRunParams(m)
			var presetSteps []string
			if hasParams {
				if reason := validateRunParams(params); reason != "" {
					_ = w.send(msg{
						"kind":              "task.error",
						"task_id":           taskID,
						"code":              "bad_request",
						"message_truncated": truncate(reason, maxMessageLen),
					})
					continue
				}
				if params.WorkflowPreset != "" {
					steps, ok := resolveWorkflowPreset(params.WorkflowPreset)
					if !ok {
						_ = w.send(msg{
							"kind":              "task.error",
							"task_id":           taskID,
							"code":              "unknown_preset",
							"message_truncated": truncate("unknown workflow_preset: "+params.WorkflowPreset, maxMessageLen),
						})
						continue
					}
					presetSteps = steps
				}
			}

			// Concurrent-task guard (Bug 2 fix).
			if !busy.CompareAndSwap(0, 1) {
				_ = w.send(msg{
					"kind":              "task.error",
					"task_id":           taskID,
					"code":              "task_conflict",
					"message_truncated": "a task is already running on this connection",
				})
				continue
			}

			prompt, _ := m["prompt"].(string)
			ctx, cancel := context.WithCancel(context.Background())
			cancelFn = cancel

			// Bind the task to the approval bridge (correlation) and put the
			// gate on the run context so any approval point in the task path
			// parks on the process's single cedar registry. Both are no-ops
			// when `approval` was not negotiated.
			endTask := func() {}
			if approvals != nil && cfg.approvalGate != nil {
				var gate approvalGate
				gate, endTask = approvals.beginTask(taskID, cfg.approvalGate)
				ctx = withApprovalGate(ctx, gate)
			}

			// Spawn the task runner. It writes via w and clears busy when done.
			go func() {
				defer endTask()
				runTask(log, w, &busy, cancel, ctx, taskID, prompt, params, presetSteps, exec, ledger, audit)
			}()

		case "task.cancel":
			if busy.Load() == 0 {
				// No task running — silently ignore (noop, as tests expect).
				continue
			}
			// Signal cancellation. The running goroutine emits task.cancelled
			// and clears busy (Bug 1 + Bug 2 fix).
			if cancelFn != nil {
				cancelFn()
			}

		case "task.approval_decision":
			// Only routed when `approval` was negotiated. Unnegotiated, this
			// kind is unknown and falls through to bad_request exactly as it
			// did before — an old host never sends it, and a new host that
			// sends it without negotiating gets a truthful protocol error
			// rather than a silently-accepted decision.
			if approvals == nil {
				_ = w.send(msg{
					"kind":              "task.error",
					"code":              "bad_request",
					"message_truncated": truncate(fmt.Sprintf("unknown kind: %q", kind), maxMessageLen),
				})
				continue
			}
			if reply, ok := approvals.handleApprovalDecision(cfg.approvals, m); ok {
				_ = w.send(reply)
			}

		default:
			_ = w.send(msg{
				"kind":              "task.error",
				"code":              "bad_request",
				"message_truncated": truncate(fmt.Sprintf("unknown kind: %q", kind), maxMessageLen),
			})
		}
	}
}

// runTask is the per-task goroutine. It dispatches task.start{prompt} onto a
// real core/ agentgraph run (see graphrun.go), streams each node's output back
// as task.running chunks, then emits the appropriate terminal event and clears
// busy. The graph run is wired to two audit surfaces, both metadata-only:
//
//   - ledger (SIGIL_INGEST_SOCKET): the pre-existing event.Event-shaped reporter
//     bridge (criterion #5 / finding #11). Preserved unchanged.
//   - audit  (KENAZ_HARNESS_EVENT_SOCK): the harness audit line-protocol
//     (contracts/harness-audit-events.md). tool_call / tool_result per node are
//     driven by the kernel's TraceSink hook via newNodeTracer.
//
// IMPORTANT: busy.Store(0) is called BEFORE writing the terminal event. This
// ensures that once the client reads task.complete or task.cancelled, the busy
// flag is already clear and a subsequent task.start on the same connection can
// proceed immediately (Bug 3 fix).
func runTask(
	log *slog.Logger,
	w *connWriter,
	busy *atomic.Int32,
	cancel context.CancelFunc,
	ctx context.Context,
	taskID string,
	prompt string,
	params RunParams,
	presetSteps []string,
	exec agentExecutor,
	ledger *ledgerEmitter,
	audit *auditSink,
) {
	defer cancel() // release the cancel func's resources

	started := time.Now()
	if !params.isZero() {
		// Structural only — never the values' content beyond the preset label,
		// which is a fixed catalog id (not user content).
		log.Info("kenaz-harness-vm: task starting", "task_id", taskID,
			"workflow_preset", params.WorkflowPreset, "preset_steps", len(presetSteps))
	} else {
		log.Info("kenaz-harness-vm: task starting", "task_id", taskID)
	}
	// task.start on both audit surfaces. Ledger carries prompt length (never
	// the text); the audit line carries no length at all — metadata only.
	ledger.emitTaskStart(taskID, len(prompt))
	audit.emitTaskStart(taskID)

	// Drive a real graph run. The TraceSink (newNodeTracer) emits the audit
	// tool_call / tool_result pair per node fire; the ledger tool_call is
	// emitted from the chunk callback so the existing ledger lifecycle
	// (start → tool_call → complete) is preserved. The chunk callback also
	// forwards each node's output as a task.running event on the RPC stream.
	tracer := newNodeTracer(audit, taskID)
	onChunk := func(node, text string) {
		if err := w.send(msg{
			"kind":    "task.running",
			"task_id": taskID,
			"text":    text,
		}); err != nil {
			log.Warn("kenaz-harness-vm: write task.running failed", "err", err)
			return
		}
		// Ledger tool_call keyed by node kind (structural — never args/output).
		ledger.emitToolCall(taskID, node)
	}

	// When a workflow_preset resolved to a step sequence, drive the graph from
	// it (the node sequence surfaces on the ledger trail — Spec 056 AC-5).
	// Otherwise reproduce today's exact two-node plan→run graph.
	var runErr error
	if len(presetSteps) > 0 {
		runErr = runAgentPresetGraph(ctx, taskID, prompt, presetSteps, exec, tracer, onChunk)
	} else {
		runErr = runAgentTaskGraph(ctx, taskID, prompt, exec, tracer, onChunk)
	}
	durationMs := time.Since(started).Milliseconds()

	// Cancellation wins over any run error: a cancelled context surfaces as a
	// terminal task.cancelled, matching the Phase-1 wire contract.
	if ctx.Err() != nil {
		log.Info("kenaz-harness-vm: task cancelled", "task_id", taskID)
		busy.Store(0)
		_ = w.send(msg{
			"kind":    "task.cancelled",
			"task_id": taskID,
		})
		ledger.emitTaskCancelled(taskID)              // terminal ledger event
		audit.emitTaskComplete(taskID, 1, durationMs) // terminal audit event (non-zero = not ok)
		return
	}

	if runErr != nil {
		log.Warn("kenaz-harness-vm: graph run failed", "task_id", taskID, "err", runErr)
		busy.Store(0)
		// Agent-exec configuration failures (no credential, bad mode) surface
		// their NAMED cause: the kernel's node wrapping would push the name
		// past the 64-rune truncation (Spec 058 US3 — the error must name the
		// missing grant on the wire).
		wireMsg := runErr.Error()
		var cfgErr *agentConfigError
		if errors.As(runErr, &cfgErr) {
			wireMsg = cfgErr.Error()
		}
		_ = w.send(msg{
			"kind":              "task.error",
			"task_id":           taskID,
			"code":              "graph_run_failed",
			"message_truncated": truncate(wireMsg, maxMessageLen),
		})
		ledger.emitTaskComplete(taskID, 1)            // terminal ledger event (exit_code=1: failed)
		audit.emitTaskComplete(taskID, 1, durationMs) // non-zero exit = failed
		return
	}

	log.Info("kenaz-harness-vm: task complete", "task_id", taskID)
	// Clear busy BEFORE writing task.complete so the client can immediately
	// send the next task.start without getting task_conflict (Bug 3 fix).
	busy.Store(0)
	_ = w.send(msg{
		"kind":    "task.complete",
		"task_id": taskID,
	})
	ledger.emitTaskComplete(taskID, 0)            // terminal ledger event (exit_code=0: ok)
	audit.emitTaskComplete(taskID, 0, durationMs) // terminal audit event (ok)
}

// truncate returns s truncated to n runes with "..." suffix when truncation occurred.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
