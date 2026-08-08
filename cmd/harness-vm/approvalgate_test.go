package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// gatedExecutor is a REALISTIC approval point in the :7881 task path: the
// agent step pulls the gate off the run context and parks on it before doing
// its work, allowing on a grant and failing closed on a deny.
//
// It is what a real gated call site would look like, which is the point — it
// proves the context seam actually reaches the executor, so the first genuine
// gate site added to this path needs no plumbing beyond calling the gate.
type gatedExecutor struct {
	surface cedar.PromptSurface

	enteredOnce sync.Once
	entered     chan struct{}

	mu      sync.Mutex
	results []cedar.Resolution
}

func newGatedExecutor() *gatedExecutor {
	return &gatedExecutor{
		surface: cedar.PromptSurface{
			Bash:   &cedar.BashPromptSurface{Pattern: "git *", Argv: []string{"git", "push"}, Dangerous: true},
			Reason: "publishing the branch",
		},
		entered: make(chan struct{}),
	}
}

func (e *gatedExecutor) Generate(ctx context.Context, _ string, input string) (string, error) {
	gate, ok := approvalGateFrom(ctx)
	if !ok {
		return "", errors.New("no_approval_gate: the run context carried no gate")
	}
	e.enteredOnce.Do(func() { close(e.entered) })

	res, err := gate.RequestInteractive(ctx, e.surface)
	e.mu.Lock()
	e.results = append(e.results, res)
	e.mu.Unlock()
	if err != nil {
		return "", err
	}
	if res.Decision == cedar.DecisionDeny {
		// Fail closed. Absence of consent is denial.
		return "", fmt.Errorf("approval_denied: %s", res.Reason)
	}
	return input, nil
}

// awaitParked blocks until the agent step is parked on the gate.
func (e *gatedExecutor) awaitParked(t *testing.T) {
	t.Helper()
	select {
	case <-e.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the task never reached its approval point")
	}
}

func (e *gatedExecutor) resolutions() []cedar.Resolution {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]cedar.Resolution, len(e.results))
	copy(out, e.results)
	return out
}

// startGatedServer wires a real registry and a gated executor together behind
// the :7881 surface, and returns an authenticated connection that has already
// negotiated `approval` plus a task parked at its approval point.
func startGatedServer(t *testing.T, taskID string, timeout time.Duration) (net.Conn, *bufio.Scanner, *cedar.Registry, *gatedExecutor) {
	t.Helper()
	reg := cedar.NewRegistry(cedar.WithTimeout(timeout))
	exec := newGatedExecutor()
	addr := startServerWithRegistryExec(t, reg, exec)

	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	authOK := nextFrame(t, conn, sc)
	if _, ok := authOK["capabilities"]; !ok {
		t.Fatalf("approval was not granted: %v", authOK)
	}
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": taskID, "prompt": "ship it"})
	exec.awaitParked(t)
	return conn, sc, reg, exec
}

// collectUntilTerminal reads frames until a terminal task event arrives.
func collectUntilTerminal(t *testing.T, conn net.Conn, sc *bufio.Scanner) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i := 0; i < 64; i++ {
		m := nextFrame(t, conn, sc)
		out = append(out, m)
		switch m["kind"] {
		case "task.complete", "task.cancelled", "task.error":
			return out
		}
	}
	t.Fatal("no terminal task event in 64 frames")
	return out
}

func countKind(frames []map[string]any, kind string) int {
	n := 0
	for _, f := range frames {
		if f["kind"] == kind {
			n++
		}
	}
	return n
}

func firstOfKind(frames []map[string]any, kind string) map[string]any {
	for _, f := range frames {
		if f["kind"] == kind {
			return f
		}
	}
	return nil
}

func terminalKind(frames []map[string]any) string {
	if len(frames) == 0 {
		return ""
	}
	k, _ := frames[len(frames)-1]["kind"].(string)
	return k
}

// --- pause and resume ------------------------------------------------------

// The whole point of 4.C2: the task genuinely stops, the operator answers from
// the host, and the task carries on.
func TestApprovalPause_AllowResumesTheTask(t *testing.T) {
	t.Parallel()
	conn, sc, _, exec := startGatedServer(t, "t-resume", time.Hour)

	req := awaitKind(t, conn, sc, "task.approval_requested")
	if req["task_id"] != "t-resume" {
		t.Fatalf("approval correlated to %v", req["task_id"])
	}

	// While parked, the task must NOT have terminated. If it had, a terminal
	// frame would already be queued ahead of the resolution we are about to
	// trigger, and the ordering assertion below would catch it.
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": req["approval_id"],
		"decision": "allow_once", "source": "host",
	})

	frames := collectUntilTerminal(t, conn, sc)
	if got := terminalKind(frames); got != "task.complete" {
		t.Fatalf("terminal event = %q; an allowed approval must let the task finish (frames: %v)", got, frames)
	}
	if n := countKind(frames, "task.approval_resolved"); n != 1 {
		t.Fatalf("%d approval_resolved frames; want exactly 1", n)
	}
	resolved := firstOfKind(frames, "task.approval_resolved")
	if resolved["decision"] != "allow_once" || resolved["source"] != "host" {
		t.Fatalf("resolution = %v", resolved)
	}
	if res := exec.resolutions(); len(res) != 1 || res[0].Decision != cedar.DecisionAllowOnce {
		t.Fatalf("the gate site observed %+v; the wire said allow_once", res)
	}
}

// A deny aborts the step. The task must land in a terminal error naming the
// denial, not quietly proceed.
func TestApprovalPause_DenyAbortsTheTask(t *testing.T) {
	t.Parallel()
	conn, sc, _, exec := startGatedServer(t, "t-abort", time.Hour)

	req := awaitKind(t, conn, sc, "task.approval_requested")
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": req["approval_id"],
		"decision": "deny", "source": "remote",
	})

	frames := collectUntilTerminal(t, conn, sc)
	if got := terminalKind(frames); got != "task.error" {
		t.Fatalf("terminal event = %q; a denied approval must abort the task", got)
	}
	if n := countKind(frames, "task.approval_resolved"); n != 1 {
		t.Fatalf("%d approval_resolved frames; want exactly 1", n)
	}
	if firstOfKind(frames, "task.approval_resolved")["source"] != "remote" {
		t.Fatalf("provenance lost: %v", firstOfKind(frames, "task.approval_resolved"))
	}
	errFrame := firstOfKind(frames, "task.error")
	if txt, _ := errFrame["message_truncated"].(string); !strings.Contains(txt, "approval_denied") {
		t.Fatalf("task.error = %q; the abort must name its cause", txt)
	}
	if res := exec.resolutions(); len(res) != 1 || res[0].Decision != cedar.DecisionDeny {
		t.Fatalf("the gate site observed %+v", res)
	}
}

// Nobody answers. The harness's own timer denies, the task aborts, and the
// host is told why. There is no auto-allow reachable through this path.
func TestApprovalPause_TimeoutIsAFailClosedDeny(t *testing.T) {
	t.Parallel()
	conn, sc, _, exec := startGatedServer(t, "t-timeout", 200*time.Millisecond)

	req := awaitKind(t, conn, sc, "task.approval_requested")
	if _, ok := req["deadline_at"].(string); !ok {
		t.Fatal("no deadline_at — surfaces have nothing to count down from")
	}

	frames := collectUntilTerminal(t, conn, sc)
	if n := countKind(frames, "task.approval_resolved"); n != 1 {
		t.Fatalf("%d approval_resolved frames; want exactly 1", n)
	}
	resolved := firstOfKind(frames, "task.approval_resolved")
	if resolved["source"] != "timeout" || resolved["decision"] != "deny" {
		t.Fatalf("timeout resolved as %v; absence of consent must be denial", resolved)
	}
	if lat, _ := resolved["latency_ms"].(float64); lat <= 0 {
		t.Fatalf("latency_ms = %v; the approval waited a real interval", lat)
	}
	if got := terminalKind(frames); got != "task.error" {
		t.Fatalf("terminal event = %q; a timed-out approval must abort the task", got)
	}
	if res := exec.resolutions(); len(res) != 1 || res[0].Decision != cedar.DecisionDeny {
		t.Fatalf("the gate site observed %+v", res)
	}
}

// task.cancel is the always-available undo path, and it must reach a task that
// is parked on an approval — otherwise a pending approval makes a run
// un-cancellable for the length of the timeout.
func TestApprovalPause_CancelReachesAParkedTask(t *testing.T) {
	t.Parallel()
	conn, sc, reg, _ := startGatedServer(t, "t-cancel", time.Hour)

	_ = awaitKind(t, conn, sc, "task.approval_requested")
	sendMsg(t, conn, map[string]any{"kind": "task.cancel", "task_id": "t-cancel"})

	frames := collectUntilTerminal(t, conn, sc)
	if got := terminalKind(frames); got != "task.cancelled" {
		t.Fatalf("terminal event = %q; cancel must win against a parked approval", got)
	}
	if n := countKind(frames, "task.approval_resolved"); n != 1 {
		t.Fatalf("%d approval_resolved frames; want exactly 1", n)
	}
	resolved := firstOfKind(frames, "task.approval_resolved")
	if resolved["source"] != "cancelled" || resolved["decision"] != "deny" {
		t.Fatalf("cancel resolved as %v; want cancelled/deny", resolved)
	}
	waitFor(t, func() bool { return reg.PendingCount() == 0 })
}

// --- the race matrix -------------------------------------------------------

// raceMatrixCase runs one interleaving repeatedly over the real wire and
// asserts the invariants that must hold in EVERY interleaving:
//
//  1. exactly one task.approval_resolved frame;
//  2. a winning source drawn from the legitimate contestants;
//  3. a non-decision source always resolves as deny;
//  4. exactly one terminal task event, consistent with the winner;
//  5. the gate site observed the same decision the surfaces were told.
func raceMatrixCase(
	t *testing.T,
	iterations int,
	timeout time.Duration,
	contend func(t *testing.T, conn net.Conn, approvalID any),
	wantSources map[string]bool,
	wantTerminals map[string]bool,
) {
	t.Helper()
	for i := 0; i < iterations; i++ {
		conn, sc, reg, exec := startGatedServer(t, fmt.Sprintf("t-race-%d", i), timeout)
		req := awaitKind(t, conn, sc, "task.approval_requested")

		contend(t, conn, req["approval_id"])

		frames := collectUntilTerminal(t, conn, sc)

		if n := countKind(frames, "task.approval_resolved"); n != 1 {
			t.Fatalf("iteration %d: %d approval_resolved frames; want exactly 1 (frames: %v)", i, n, frames)
		}
		resolved := firstOfKind(frames, "task.approval_resolved")
		src, _ := resolved["source"].(string)
		if !wantSources[src] {
			t.Fatalf("iteration %d: winning source %q is not a legitimate contestant", i, src)
		}
		switch src {
		case "timeout", "cancelled", "overflow":
			if resolved["decision"] != "deny" {
				t.Fatalf("iteration %d: source %q resolved as %v", i, src, resolved["decision"])
			}
		}
		if resolved["approval_id"] != req["approval_id"] {
			t.Fatalf("iteration %d: resolution names %v, request was %v",
				i, resolved["approval_id"], req["approval_id"])
		}

		term := terminalKind(frames)
		if !wantTerminals[term] {
			t.Fatalf("iteration %d: terminal event %q inconsistent with a %q resolution", i, term, src)
		}
		if n := countKind(frames, "task.complete") + countKind(frames, "task.cancelled") + countKind(frames, "task.error"); n != 1 {
			t.Fatalf("iteration %d: %d terminal task events", i, n)
		}

		// The parked call site and the surfaces must not disagree.
		res := exec.resolutions()
		if len(res) != 1 {
			t.Fatalf("iteration %d: the gate site unparked %d times", i, len(res))
		}
		if string(res[0].Decision) != resolved["decision"] {
			t.Fatalf("iteration %d: the task acted on %q while the host was told %q",
				i, res[0].Decision, resolved["decision"])
		}

		waitFor(t, func() bool { return reg.PendingCount() == 0 })
		_ = conn.Close()
	}
}

// A decision landing as the fail-closed timer fires. Either may win; two
// resolutions, or a task acting on a decision the host was not told about,
// may not happen.
func TestApprovalWireRace_DecisionVsTimeout(t *testing.T) {
	t.Parallel()
	raceMatrixCase(t, 40, 25*time.Millisecond,
		func(t *testing.T, conn net.Conn, approvalID any) {
			sendMsg(t, conn, map[string]any{
				"kind": "task.approval_decision", "approval_id": approvalID,
				"decision": "allow_once", "source": "host",
			})
		},
		map[string]bool{"host": true, "timeout": true},
		// allow → the step proceeds and the task completes; deny-by-timeout →
		// the step fails closed and the task errors.
		map[string]bool{"task.complete": true, "task.error": true},
	)
}

// The operator approves from the phone at the same moment they cancel the run
// from the desktop.
func TestApprovalWireRace_DecisionVsCancel(t *testing.T) {
	t.Parallel()
	raceMatrixCase(t, 40, time.Hour,
		func(t *testing.T, conn net.Conn, approvalID any) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				sendMsg(t, conn, map[string]any{
					"kind": "task.approval_decision", "approval_id": approvalID,
					"decision": "allow_once", "source": "remote",
				})
			}()
			go func() {
				defer wg.Done()
				sendMsg(t, conn, map[string]any{"kind": "task.cancel"})
			}()
			wg.Wait()
		},
		map[string]bool{"remote": true, "cancelled": true},
		// The frames are read in order on one connection, so both orderings
		// are legitimate: an allow that lands first lets the step proceed
		// (complete), a cancel that lands first denies and unwinds (cancelled),
		// and an allow followed immediately by cancel can surface either the
		// cancelled terminal or a fail-closed error depending on where the
		// context cancellation lands inside the step.
		map[string]bool{"task.complete": true, "task.cancelled": true, "task.error": true},
	)
}

// Two surfaces decide the same approval simultaneously, with OPPOSITE
// decisions. First to reach the registry wins; the loser changes nothing, and
// crucially does not produce a second resolution the host would ledger twice.
func TestApprovalWireRace_DoubleDecision(t *testing.T) {
	t.Parallel()
	raceMatrixCase(t, 40, time.Hour,
		func(t *testing.T, conn net.Conn, approvalID any) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				sendMsg(t, conn, map[string]any{
					"kind": "task.approval_decision", "approval_id": approvalID,
					"decision": "allow_always", "source": "host",
				})
			}()
			go func() {
				defer wg.Done()
				sendMsg(t, conn, map[string]any{
					"kind": "task.approval_decision", "approval_id": approvalID,
					"decision": "deny", "source": "remote",
				})
			}()
			wg.Wait()
		},
		map[string]bool{"host": true, "remote": true},
		map[string]bool{"task.complete": true, "task.error": true},
	)
}

// --- derived run status ----------------------------------------------------

// waiting_for_input goes live with this surface. It is DERIVED from the
// request/resolve pair rather than carried as a wire field, so there is no
// second, race-prone authority on what a run's status is.
func TestRunStatus_WaitingForInputAndBack(t *testing.T) {
	t.Parallel()
	b, _, reg := newBridgeHarness(t, time.Hour)

	if got := b.runStatus(); got != "running" {
		t.Fatalf("idle bridge reports %q", got)
	}
	_, end := b.beginTask("t-status", reg)
	if got := b.runStatus(); got != "running" {
		t.Fatalf("a bound task with no approval reports %q; want running", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())
	waitFor(t, func() bool { return b.runStatus() == "waiting_for_input" })

	for _, p := range reg.ListPending() {
		if err := reg.ResolveFrom(p.RequestID, cedar.DecisionAllowOnce, cedar.SourceHost); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	<-resCh

	waitFor(t, func() bool { return b.runStatus() == "running" })
	if n := b.pendingCount(); n != 0 {
		t.Fatalf("bridge leaked %d pending approvals", n)
	}
	end()
	if got := b.runStatus(); got != "running" {
		t.Fatalf("unbound bridge reports %q", got)
	}
}

// Every interleaving must drain the bridge's pending map. A leak here means a
// run stuck in waiting_for_input forever on the host's status view.
func TestRunStatus_DrainsInEveryInterleaving(t *testing.T) {
	t.Parallel()
	for i := 0; i < 50; i++ {
		b, rc, reg := newBridgeHarness(t, 5*time.Millisecond)
		_, end := b.beginTask("t-drain", reg)

		ctx, cancel := context.WithCancel(context.Background())
		resCh := raiseApproval(reg, ctx, fsSurface())

		// Wait for the DISPATCHED FRAME, not for a pending count.
		//
		// The 5 ms TTL above is deliberate — expiry is the third racer this
		// test wants, alongside the remote deny and the ctx cancel. But that
		// makes "the registry has one pending approval" a condition with a
		// 5 ms lifetime, and polling it is how this test flaked on loaded CI:
		// one overshooting poll and the approval had already self-resolved as
		// a timeout, so the count sat at zero forever and the wait burned its
		// full deadline. The recorded task.approval_requested frame proves the
		// same precondition (the approval was raised AND dispatched) and is
		// append-only, so no interleaving can take it away.
		waitForFrames(t, rc, "task.approval_requested", 1)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// May be empty if the TTL won the race — that is a legitimate
			// interleaving, and the drain assertion below still has to hold.
			for _, p := range reg.ListPending() {
				_ = reg.ResolveFrom(p.RequestID, cedar.DecisionDeny, cedar.SourceRemote)
			}
		}()
		go func() { defer wg.Done(); cancel() }()
		wg.Wait()
		<-resCh

		waitFor(t, func() bool { return b.pendingCount() == 0 && b.runStatus() == "running" },
			fmt.Sprintf("bridge drained on iteration %d", i))
		end()
	}
}

// The gate must actually be on the run context. Without it the first real
// gated call site in this path would silently find nothing to park on, and the
// fail direction would be whatever that call site happened to choose.
func TestApprovalGate_ReachesTheAgentStep(t *testing.T) {
	t.Parallel()
	conn, sc, _, _ := startGatedServer(t, "t-seam", time.Hour)
	req := awaitKind(t, conn, sc, "task.approval_requested")
	if req["action_kind"] != "bash::command::exec" {
		t.Fatalf("action_kind = %v; want the structural bash class", req["action_kind"])
	}
	if ak, _ := req["action_kind"].(string); strings.Contains(ak, "git") {
		t.Fatalf("action_kind %q leaks the command", ak)
	}
	if s, _ := req["summary"].(string); !strings.Contains(s, "git push") {
		t.Fatalf("summary = %q; the decider needs to see the command", s)
	}
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": req["approval_id"], "decision": "deny",
	})
	_ = collectUntilTerminal(t, conn, sc)
}

// withApprovalGate / approvalGateFrom must not invent a gate. A call site that
// finds none has to choose its own fail direction explicitly.
func TestApprovalGateContext_AbsentIsAbsent(t *testing.T) {
	t.Parallel()
	if g, ok := approvalGateFrom(context.Background()); ok || g != nil {
		t.Fatalf("bare context yielded a gate: %v", g)
	}
	ctx := withApprovalGate(context.Background(), nil)
	if g, ok := approvalGateFrom(ctx); ok || g != nil {
		t.Fatalf("a nil gate was stored as present: %v", g)
	}
	reg := cedar.NewRegistry()
	if g, ok := approvalGateFrom(withApprovalGate(context.Background(), reg)); !ok || g == nil {
		t.Fatal("a real gate did not survive the context")
	}
}
