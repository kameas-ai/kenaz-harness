package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// --- helpers ---------------------------------------------------------------

// startApprovalServer spins up a harness-vm listener wired to a real
// cedar.Registry — the same type the in-VM chassis builds — and returns the
// address plus that registry, so a test can raise an approval exactly the way
// a gate site does: by calling RequestInteractive and parking.
//
// timeout overrides the registry's fail-closed budget; pass 0 for one hour
// (i.e. "the timer will not interfere with this test").
func startApprovalServer(t *testing.T, timeout time.Duration) (string, *cedar.Registry, *blockingExecutor) {
	t.Helper()
	if timeout == 0 {
		timeout = time.Hour
	}
	reg := cedar.NewRegistry(cedar.WithTimeout(timeout))
	exec := newBlockingExecutor()
	t.Cleanup(exec.finish) // never leave a task parked past the test
	return startServerWithRegistryExec(t, reg, exec), reg, exec
}

// blockingExecutor holds the agent step open until released, so a test can
// keep a task genuinely IN FLIGHT while it drives an approval. Without this
// the stub graph finishes in ~50ms and the approval races the task's own
// completion — which would make these tests assert timing, not behaviour.
type blockingExecutor struct {
	release     chan struct{}
	started     chan struct{}
	once        sync.Once
	startedOnce sync.Once
}

func newBlockingExecutor() *blockingExecutor {
	return &blockingExecutor{release: make(chan struct{}), started: make(chan struct{})}
}

func (b *blockingExecutor) Generate(ctx context.Context, _ string, input string) (string, error) {
	b.startedOnce.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return input, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// awaitTaskInFlight blocks until the agent step is executing, which is the
// only reliable proof that handleConn has read task.start and BOUND the task
// to the approval bridge. Raising an approval before that binding happens
// would test a race, not the contract.
func (b *blockingExecutor) awaitTaskInFlight(t *testing.T) {
	t.Helper()
	select {
	case <-b.started:
	case <-time.After(5 * time.Second):
		t.Fatal("task never reached the agent step")
	}
}

// finish lets every parked task run to completion. Safe to call repeatedly.
func (b *blockingExecutor) finish() { b.once.Do(func() { close(b.release) }) }

// startServerWithRegistry serves connections wired to reg. A nil reg models a
// process whose chassis never bootstrapped: no gate, so the capability can
// never be granted.
func startServerWithRegistry(t *testing.T, reg *cedar.Registry) string {
	return startServerWithRegistryExec(t, reg, stubExecutor{})
}

// startServerWithRegistryExec is startServerWithRegistry with an explicit
// agent executor.
func startServerWithRegistryExec(t *testing.T, reg *cedar.Registry, exec agentExecutor) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	log := newTestLogger()
	var opts []connOption
	if reg != nil {
		opts = append(opts, withApprovalRegistry(reg, reg))
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(log, conn, "", exec, nil, nil, &readService{log: log}, opts...)
		}
	}()
	return ln.Addr().String()
}

// dialRaw opens a connection and sends the auth frame WITHOUT reading the
// reply, so a caller can inspect the auth.ok bytes exactly as they arrive.
func dialRaw(t *testing.T, addr string, auth map[string]any) (net.Conn, *bufio.Scanner) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sendMsg(t, conn, auth)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return conn, sc
}

// nextLine reads one raw NDJSON line with a deadline.
func nextLine(t *testing.T, conn net.Conn, sc *bufio.Scanner) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	if !sc.Scan() {
		t.Fatalf("nextLine: no line (err=%v)", sc.Err())
	}
	return sc.Text()
}

// nextFrame reads one NDJSON line and decodes it.
func nextFrame(t *testing.T, conn net.Conn, sc *bufio.Scanner) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(nextLine(t, conn, sc)), &m); err != nil {
		t.Fatalf("nextFrame: %v", err)
	}
	return m
}

// awaitKind reads frames until one has the wanted kind, failing on anything
// unexpected in between so a stray emission cannot hide behind a filter.
func awaitKind(t *testing.T, conn net.Conn, sc *bufio.Scanner, want string, skippable ...string) map[string]any {
	t.Helper()
	skip := map[string]bool{}
	for _, s := range skippable {
		skip[s] = true
	}
	for i := 0; i < 32; i++ {
		m := nextFrame(t, conn, sc)
		k, _ := m["kind"].(string)
		if k == want {
			return m
		}
		if !skip[k] {
			t.Fatalf("awaitKind(%q): unexpected frame %v", want, m)
		}
	}
	t.Fatalf("awaitKind(%q): not seen in 32 frames", want)
	return nil
}

// raiseApproval parks a goroutine on reg exactly as a gate site does, and
// returns a channel carrying the resolution the parked caller ultimately
// observes.
func raiseApproval(reg *cedar.Registry, ctx context.Context, surface cedar.PromptSurface) chan cedar.Resolution {
	out := make(chan cedar.Resolution, 1)
	go func() {
		res, _ := reg.RequestInteractive(ctx, surface)
		out <- res
	}()
	return out
}

func fsSurface() cedar.PromptSurface {
	return cedar.PromptSurface{
		FS:     &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/workspace/notes.md", Dangerous: true},
		Reason: "recording the plan",
	}
}

// --- negotiation -----------------------------------------------------------

// THE regression test for the whole change: a client that does not negotiate
// must receive the exact bytes it received before this feature existed, even
// on a harness that is fully capable of brokering approvals.
func TestAuthOK_ByteIdenticalWithoutNegotiation(t *testing.T) {
	t.Parallel()
	addr, _, _ := startApprovalServer(t, 0)

	for _, auth := range []map[string]any{
		{"kind": "auth", "token": ""},                             // old client: no key at all
		{"kind": "auth", "token": "", "capabilities": []any{}},    // empty request
		{"kind": "auth", "token": "", "capabilities": "approval"}, // wrong shape
		{"kind": "auth", "token": "", "capabilities": []any{"telepathy"}},
		{"kind": "auth", "token": "", "capabilities": []any{1, true}},
	} {
		conn, sc := dialRaw(t, addr, auth)
		got := nextLine(t, conn, sc)
		if got != `{"kind":"auth.ok"}` {
			t.Fatalf("auth %v produced %s; want the byte-identical single-key auth.ok", auth, got)
		}
	}
}

func TestAuthOK_GrantsNegotiatedCapability(t *testing.T) {
	t.Parallel()
	addr, _, _ := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	m := nextFrame(t, conn, sc)
	if m["kind"] != "auth.ok" {
		t.Fatalf("got %v", m)
	}
	caps, _ := m["capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "approval" {
		t.Fatalf("granted set = %v; want [approval]", m["capabilities"])
	}
}

// auth.ok carries the GRANTED subset, not an echo of the request. A capability
// this build does not implement must be dropped, not reflected — a host that
// gates behaviour on the reply would otherwise enable a surface that does not
// exist.
func TestAuthOK_GrantIsNotAnEcho(t *testing.T) {
	t.Parallel()
	addr, _, _ := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval", "telepathy", "time-travel"},
	})
	m := nextFrame(t, conn, sc)
	caps, _ := m["capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "approval" {
		t.Fatalf("granted set = %v; want exactly [approval]", m["capabilities"])
	}
}

// A process with no cedar gate must NOT grant `approval`. Granting it would be
// a lie the host cannot detect, and the host's "approvals not brokered" state
// exists precisely so the truthful answer is renderable.
func TestAuthOK_NoGateNoGrant(t *testing.T) {
	t.Parallel()
	addr := startServerWithRegistry(t, nil)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	if got := nextLine(t, conn, sc); got != `{"kind":"auth.ok"}` {
		t.Fatalf("gateless harness granted a capability: %s", got)
	}
}

func TestNegotiateCapabilities_Table(t *testing.T) {
	t.Parallel()
	avail := map[string]bool{capabilityApproval: true}
	cases := []struct {
		name      string
		requested any
		available map[string]bool
		want      []string
	}{
		{"absent key", nil, avail, nil},
		{"empty list", []any{}, avail, nil},
		{"not a list", "approval", avail, nil},
		{"non-strings", []any{1, false, nil}, avail, nil},
		{"unknown only", []any{"telepathy"}, avail, nil},
		{"approval", []any{"approval"}, avail, []string{"approval"}},
		{"approval among unknowns", []any{"x", "approval", "y"}, avail, []string{"approval"}},
		{"duplicate", []any{"approval", "approval"}, avail, []string{"approval"}},
		{"unavailable", []any{"approval"}, map[string]bool{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := negotiateCapabilities(tc.requested, tc.available)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}

// --- silence absent negotiation --------------------------------------------

// The harness MUST NOT emit an approval kind unless `approval` was granted.
// Emitting unilaterally is a wire-lock violation, and an old host would merely
// log "unexpected message kind" and let the task sit until the deny — a soft
// hang with no user-visible cause.
func TestUnnegotiated_EmitsNoApprovalKinds(t *testing.T) {
	t.Parallel()
	addr, reg, exec := startApprovalServer(t, 0)
	conn := dialAndAuth(t, addr, "") // no capabilities
	sc := bufio.NewScanner(conn)

	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-silent", "prompt": "hi"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := raiseApproval(reg, ctx, fsSurface())
	waitFor(t, func() bool { return reg.PendingCount() == 1 })
	exec.finish()

	// Drain until the task terminates. Not one approval frame may appear.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if !sc.Scan() {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		kind, _ := m["kind"].(string)
		if strings.HasPrefix(kind, "task.approval") {
			t.Fatalf("unnegotiated connection emitted %q: %v", kind, m)
		}
		if kind == "task.complete" || kind == "task.error" {
			break
		}
	}

	// The gate is untouched: the approval is still pending and still resolves
	// at the served surface, which is the whole reason silence is safe.
	if n := reg.PendingCount(); n != 1 {
		t.Fatalf("pending approvals = %d; want the approval still parked on the gate", n)
	}
	cancel()
	<-res
}

// Unnegotiated, task.approval_decision is simply an unknown kind — the same
// bad_request any other unknown kind gets. A new host that skipped negotiation
// must get a truthful protocol error, not a silently-honoured decision.
func TestUnnegotiated_DecisionIsUnknownKind(t *testing.T) {
	t.Parallel()
	addr, _, _ := startApprovalServer(t, 0)
	conn := dialAndAuth(t, addr, "")

	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": "rid-x", "decision": "allow_once",
	})
	m := recvMsg(t, conn)
	if m["kind"] != "task.error" || m["code"] != "bad_request" {
		t.Fatalf("got %v; want a bad_request task.error", m)
	}
	if msgText, _ := m["message_truncated"].(string); !strings.Contains(msgText, "unknown kind") {
		t.Fatalf("message = %q; want the unknown-kind wording every other kind gets", msgText)
	}
}

// --- the three kinds round-trip --------------------------------------------

func TestApprovalKinds_RoundTrip(t *testing.T) {
	t.Parallel()
	addr, reg, exec := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc) // auth.ok

	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-round", "prompt": "hello"})
	exec.awaitTaskInFlight(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())

	req := awaitKind(t, conn, sc, "task.approval_requested", "task.running", "task.complete")

	// --- payload shape ---
	if req["task_id"] != "t-round" {
		t.Fatalf("task_id = %v; the approval must be correlated to the in-flight task", req["task_id"])
	}
	approvalID, _ := req["approval_id"].(string)
	if !strings.HasPrefix(approvalID, "rid-") {
		t.Fatalf("approval_id = %q; must be cedar's RequestID verbatim", approvalID)
	}
	if req["family"] != "fs" {
		t.Fatalf("family = %v", req["family"])
	}
	if req["action_kind"] != "fs::file::write" {
		t.Fatalf("action_kind = %v; want the structural class", req["action_kind"])
	}
	if ak, _ := req["action_kind"].(string); strings.Contains(ak, "/workspace") {
		t.Fatalf("action_kind %q leaks the path — it is what the host ledgers", ak)
	}
	summary, _ := req["summary"].(string)
	if !strings.Contains(summary, "/workspace/notes.md") {
		t.Fatalf("summary = %q; a surface that cannot see the path cannot decide", summary)
	}
	if !strings.Contains(summary, "recording the plan") {
		t.Fatalf("summary = %q dropped the stated reason", summary)
	}
	if req["dangerous"] != true {
		t.Fatalf("dangerous = %v; a surface must be able to style without parsing summary", req["dangerous"])
	}
	requestedAt, _ := req["requested_at"].(string)
	deadlineAt, _ := req["deadline_at"].(string)
	tReq, err := time.Parse(time.RFC3339, requestedAt)
	if err != nil {
		t.Fatalf("requested_at %q: %v", requestedAt, err)
	}
	tDeadline, err := time.Parse(time.RFC3339, deadlineAt)
	if err != nil {
		t.Fatalf("deadline_at %q: %v", deadlineAt, err)
	}
	if !tDeadline.After(tReq) {
		t.Fatalf("deadline_at %v is not after requested_at %v", tDeadline, tReq)
	}
	if _, ok := req["timeout_s"].(float64); !ok {
		t.Fatalf("timeout_s missing or not a number: %v", req["timeout_s"])
	}
	// No wire field may assert that a device-auth challenge occurred: such a
	// field would be attacker-controlled on a compromised device.
	for _, forbidden := range []string{"device_id", "device_name", "account_id", "authenticated", "biometric"} {
		if _, present := req[forbidden]; present {
			t.Fatalf("task.approval_requested carries forbidden field %q", forbidden)
		}
	}

	// --- decision in, resolution out ---
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "task_id": "t-round",
		"approval_id": approvalID, "decision": "allow_once", "source": "host",
	})

	resolved := awaitKind(t, conn, sc, "task.approval_resolved", "task.running", "task.complete")
	if resolved["approval_id"] != approvalID {
		t.Fatalf("resolution names %v; requested %v", resolved["approval_id"], approvalID)
	}
	if resolved["task_id"] != "t-round" {
		t.Fatalf("resolution task_id = %v", resolved["task_id"])
	}
	if resolved["decision"] != "allow_once" {
		t.Fatalf("decision = %v", resolved["decision"])
	}
	if resolved["source"] != "host" {
		t.Fatalf("source = %v; provenance must survive the round trip", resolved["source"])
	}
	if _, ok := resolved["resolved_at"].(string); !ok {
		t.Fatalf("resolved_at missing")
	}
	if _, ok := resolved["latency_ms"].(float64); !ok {
		t.Fatalf("latency_ms missing or not a number: %v", resolved["latency_ms"])
	}
	if _, present := resolved["summary"]; present {
		t.Fatal("task.approval_resolved carries summary — the resolution is provenance, not content")
	}

	// --- and the parked gate site actually unparks with that decision ---
	select {
	case got := <-resCh:
		if got.Decision != cedar.DecisionAllowOnce {
			t.Fatalf("gate site resumed with %q; the wire said allow_once", got.Decision)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gate site never unparked after the decision landed")
	}

	// The task resumes and completes: an approval is a pause, not a terminus.
	exec.finish()
	if done := awaitKind(t, conn, sc, "task.complete", "task.running"); done["task_id"] != "t-round" {
		t.Fatalf("task completed as %v", done["task_id"])
	}
}

// allow_always is a real cedar decision the desktop panel offers. Collapsing
// the wire to allow|deny would silently drop the transient-grant path and
// force the adapter to invent a mapping.
func TestApprovalDecision_AllowAlwaysSurvivesTheWire(t *testing.T) {
	t.Parallel()
	addr, reg, exec := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-always", "prompt": "x"})
	exec.awaitTaskInFlight(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())

	req := awaitKind(t, conn, sc, "task.approval_requested", "task.running", "task.complete")
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": req["approval_id"],
		"decision": "allow_always", "source": "host",
	})
	resolved := awaitKind(t, conn, sc, "task.approval_resolved", "task.running", "task.complete")
	if resolved["decision"] != "allow_always" {
		t.Fatalf("decision = %v; want allow_always", resolved["decision"])
	}
	if got := <-resCh; got.Decision != cedar.DecisionAllowAlways {
		t.Fatalf("gate site saw %q", got.Decision)
	}
	exec.finish()
}

// --- inbound validation ----------------------------------------------------

// A duplicate or late decision is ordinary traffic on an at-least-once stream:
// acked and dropped, with no error, no second resolution and no state change.
// That is what makes the stream safe to retry.
func TestApprovalDecision_UnknownIDIsAckedAndDropped(t *testing.T) {
	t.Parallel()
	addr, reg, exec := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-dup", "prompt": "x"})
	exec.awaitTaskInFlight(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())
	req := awaitKind(t, conn, sc, "task.approval_requested", "task.running", "task.complete")
	approvalID := req["approval_id"]

	// First decision wins.
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": approvalID, "decision": "deny", "source": "host",
	})
	first := awaitKind(t, conn, sc, "task.approval_resolved", "task.running", "task.complete")
	if first["decision"] != "deny" {
		t.Fatalf("first resolution = %v", first["decision"])
	}
	<-resCh

	// Second decision for the same id, and one for an id that never existed.
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": approvalID, "decision": "allow_once", "source": "remote",
	})
	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": "rid-deadbeefdeadbeefdeadbeef", "decision": "allow_once", "source": "host",
	})
	// A liveness probe behind them: if either produced output, it arrives
	// before this does.
	sendMsg(t, conn, map[string]any{"kind": "nonsense.kind"})
	probe := nextFrame(t, conn, sc)
	if probe["kind"] != "task.error" {
		t.Fatalf("a dropped decision produced a frame: %v", probe)
	}
	if txt, _ := probe["message_truncated"].(string); !strings.Contains(txt, "nonsense.kind") {
		t.Fatalf("expected the probe's own error, got %v", probe)
	}
	exec.finish()
}

func TestApprovalDecision_MalformedFramesAreBadRequest(t *testing.T) {
	t.Parallel()
	addr, _, _ := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)

	cases := []struct {
		name  string
		frame map[string]any
		want  string
	}{
		{"no approval_id", map[string]any{
			"kind": "task.approval_decision", "decision": "allow_once",
		}, "approval_id required"},
		{"unknown decision", map[string]any{
			"kind": "task.approval_decision", "approval_id": "rid-1", "decision": "maybe",
		}, "invalid decision"},
		{"missing decision", map[string]any{
			"kind": "task.approval_decision", "approval_id": "rid-1",
		}, "invalid decision"},
		// guest is the served modal's own class; timeout / cancelled /
		// overflow are registry-synthesised. Accepting either inbound would
		// let a host forge provenance the ledger then records as fact.
		{"forged guest source", map[string]any{
			"kind": "task.approval_decision", "approval_id": "rid-1",
			"decision": "allow_once", "source": "guest",
		}, "invalid source"},
		{"forged timeout source", map[string]any{
			"kind": "task.approval_decision", "approval_id": "rid-1",
			"decision": "deny", "source": "timeout",
		}, "invalid source"},
		{"device identity as source", map[string]any{
			"kind": "task.approval_decision", "approval_id": "rid-1",
			"decision": "allow_once", "source": "remote:iphone-of-nick",
		}, "invalid source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sendMsg(t, conn, tc.frame)
			m := nextFrame(t, conn, sc)
			if m["kind"] != "task.error" || m["code"] != "bad_request" {
				t.Fatalf("got %v; want bad_request", m)
			}
			if txt, _ := m["message_truncated"].(string); !strings.Contains(txt, tc.want) {
				t.Fatalf("message = %q; want it to mention %q", txt, tc.want)
			}
		})
	}
}

// source defaults to host — the desktop panel is the surface that forwards
// without annotating.
func TestApprovalDecision_AbsentSourceDefaultsToHost(t *testing.T) {
	t.Parallel()
	addr, reg, exec := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)
	sendMsg(t, conn, map[string]any{"kind": "task.start", "task_id": "t-src", "prompt": "x"})
	exec.awaitTaskInFlight(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())
	req := awaitKind(t, conn, sc, "task.approval_requested", "task.running", "task.complete")

	sendMsg(t, conn, map[string]any{
		"kind": "task.approval_decision", "approval_id": req["approval_id"], "decision": "deny",
	})
	resolved := awaitKind(t, conn, sc, "task.approval_resolved", "task.running", "task.complete")
	if resolved["source"] != "host" {
		t.Fatalf("source = %v; want host", resolved["source"])
	}
	<-resCh
	exec.finish()
}

// --- correlation limits ----------------------------------------------------

// The task↔approval binding rests on one-task-per-connection. With no task in
// flight there is no id to correlate to, and speculating one would attribute
// an action to the wrong run — so the bridge stays silent and the gate resolves
// at the served surface as it always did.
func TestApproval_NoTaskInFlightIsNotForwarded(t *testing.T) {
	t.Parallel()
	addr, reg, _ := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())

	// Wait for the gate to be genuinely parked before probing.
	waitFor(t, func() bool { return reg.PendingCount() == 1 })

	sendMsg(t, conn, map[string]any{"kind": "nonsense.kind"})
	probe := nextFrame(t, conn, sc)
	if probe["kind"] != "task.error" {
		t.Fatalf("an uncorrelatable approval was forwarded: %v", probe)
	}

	cancel()
	<-resCh
}

// The bridge must detach when the connection ends: a registry that keeps
// dispatching into a closed connection leaks a listener per connection for the
// life of the process.
func TestApprovalBridge_DetachesOnDisconnect(t *testing.T) {
	t.Parallel()
	addr, reg, _ := startApprovalServer(t, 0)
	conn, sc := dialRaw(t, addr, map[string]any{
		"kind": "auth", "token": "", "capabilities": []any{"approval"},
	})
	_ = nextFrame(t, conn, sc)
	_ = conn.Close()
	_ = sc

	// Give handleConn's deferred removals time to run, then drive a full
	// approval cycle. Nothing may panic or block on the dead connection.
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())
	waitFor(t, func() bool { return reg.PendingCount() == 1 })
	for _, p := range reg.ListPending() {
		if err := reg.ResolveFrom(p.RequestID, cedar.DecisionDeny, cedar.SourceHost); err != nil {
			t.Fatalf("resolve after disconnect: %v", err)
		}
	}
	select {
	case <-resCh:
	case <-time.After(3 * time.Second):
		t.Fatal("gate site never unparked after the bridge detached")
	}
}

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never became true")
}

// --- bridge unit tests -----------------------------------------------------

// recordingConn captures every NDJSON frame a connWriter emits.
type recordingConn struct {
	mu    sync.Mutex
	lines []string
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}
func (c *recordingConn) Read([]byte) (int, error)         { return 0, nil }
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return nil }
func (c *recordingConn) RemoteAddr() net.Addr             { return nil }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func (c *recordingConn) frames(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, 0, len(c.lines))
	for _, l := range c.lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("frames: %v (line %q)", err, l)
		}
		out = append(out, m)
	}
	return out
}

// newBridgeHarness returns a bridge attached to a real registry, plus the
// frame recorder and the registry.
func newBridgeHarness(t *testing.T, timeout time.Duration) (*approvalBridge, *recordingConn, *cedar.Registry) {
	t.Helper()
	rc := &recordingConn{}
	b := newApprovalBridge(&connWriter{conn: rc}, newTestLogger())
	reg := cedar.NewRegistry(cedar.WithTimeout(timeout))
	t.Cleanup(reg.AddDispatcher(b))
	t.Cleanup(reg.AddResolutionObserver(b))
	return b, rc, reg
}

// Queue overflow resolves an approval that was never requested. Without the
// resolution the denial is invisible on every surface.
func TestBridge_OverflowEmitsResolvedWithoutRequested(t *testing.T) {
	t.Parallel()
	b, rc, reg := newBridgeHarness(t, time.Hour)
	_, end := b.beginTask("t-overflow", reg)
	defer end()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < cedar.PromptQueueCap; i++ {
		s := cedar.PromptSurface{FS: &cedar.FSPromptSurface{Op: "read", CanonicalPath: "/f" + string(rune('a'+i))}}
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = reg.RequestInteractive(ctx, s) }()
	}
	waitForFrames(t, rc, "task.approval_requested", cedar.PromptQueueCap)

	res, _ := reg.RequestInteractive(ctx, cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/over"},
	})
	if res.Decision != cedar.DecisionDeny {
		t.Fatalf("overflow decision = %q", res.Decision)
	}

	var overflow map[string]any
	requested := 0
	for _, f := range rc.frames(t) {
		switch f["kind"] {
		case "task.approval_requested":
			requested++
		case "task.approval_resolved":
			if f["source"] == "overflow" {
				overflow = f
			}
		}
	}
	if requested != cedar.PromptQueueCap {
		t.Fatalf("requested frames = %d; want %d (the overflowed one is never dispatched)",
			requested, cedar.PromptQueueCap)
	}
	if overflow == nil {
		t.Fatal("queue overflow produced no task.approval_resolved — the denial is invisible")
	}
	if overflow["decision"] != "deny" {
		t.Fatalf("overflow decision = %v", overflow["decision"])
	}
	if overflow["task_id"] != "t-overflow" {
		t.Fatalf("overflow task_id = %v", overflow["task_id"])
	}
	if overflow["latency_ms"] != float64(0) {
		t.Fatalf("overflow latency_ms = %v; nothing waited", overflow["latency_ms"])
	}

	cancel()
	wg.Wait()
}

// The served (:7880) modal is a decision surface the host cannot observe. When
// it decides, the host must LEARN about it — as source `guest`, distinct from
// `host`, so the ledger does not record a decision the desktop never made.
func TestBridge_GuestDecisionIsReportedToTheHost(t *testing.T) {
	t.Parallel()
	b, rc, reg := newBridgeHarness(t, time.Hour)
	_, end := b.beginTask("t-guest", reg)
	defer end()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface())
	waitFor(t, func() bool { return reg.PendingCount() == 1 })

	// Resolve the way the served RPC handler does.
	for _, p := range reg.ListPending() {
		if err := reg.Resolve(p.RequestID, cedar.DecisionAllowOnce); err != nil {
			t.Fatalf("guest resolve: %v", err)
		}
	}
	<-resCh

	var resolved map[string]any
	for _, f := range rc.frames(t) {
		if f["kind"] == "task.approval_resolved" {
			resolved = f
		}
	}
	if resolved == nil {
		t.Fatal("a guest decision produced no resolution on :7881 — the host would wait for the timeout")
	}
	if resolved["source"] != "guest" {
		t.Fatalf("source = %v; want guest", resolved["source"])
	}
}

// The registry is process-global and fans every request to every attached
// bridge. Without a session match, two connected hosts would each report the
// other's approvals under their own run — an operator action attributed to a
// task that never asked for it.
func TestApprovalBridge_DoesNotClaimAnotherRunsApproval(t *testing.T) {
	t.Parallel()
	bA, rcA, reg := newBridgeHarness(t, time.Hour)
	bB := newApprovalBridge(&connWriter{conn: &recordingConn{}}, newTestLogger())
	t.Cleanup(reg.AddDispatcher(bB))
	t.Cleanup(reg.AddResolutionObserver(bB))

	_, endA := bA.beginTask("task-A", reg)
	defer endA()
	_, endB := bB.beginTask("task-B", reg)
	defer endB()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A gate site inside task-B's run names its session.
	surface := fsSurface()
	surface.SessionID = "task-B"
	resCh := raiseApproval(reg, ctx, surface)
	waitFor(t, func() bool { return bB.pendingCount() == 1 })

	if n := bA.pendingCount(); n != 0 {
		t.Fatalf("bridge A claimed %d of task-B's approvals", n)
	}
	for _, f := range rcA.frames(t) {
		if strings.HasPrefix(f["kind"].(string), "task.approval") {
			t.Fatalf("bridge A emitted %v for an approval belonging to task-B", f)
		}
	}

	for _, p := range reg.ListPending() {
		_ = reg.ResolveFrom(p.RequestID, cedar.DecisionDeny, cedar.SourceHost)
	}
	<-resCh
	for _, f := range rcA.frames(t) {
		if strings.HasPrefix(f["kind"].(string), "task.approval") {
			t.Fatalf("bridge A emitted %v on resolution of task-B's approval", f)
		}
	}
	waitFor(t, func() bool { return bB.pendingCount() == 0 })
}

// A gate site that names no session still correlates to the connection's
// in-flight task — the busy-flag fallback, which is what every pre-074 gate
// site will hit until it starts populating SessionID.
func TestApprovalBridge_UnnamedSessionUsesTheInFlightTask(t *testing.T) {
	t.Parallel()
	b, rc, reg := newBridgeHarness(t, time.Hour)
	_, end := b.beginTask("task-only", reg)
	defer end()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resCh := raiseApproval(reg, ctx, fsSurface()) // no SessionID
	waitForFrames(t, rc, "task.approval_requested", 1)

	req := firstFrameOfKind(t, rc, "task.approval_requested")
	if req == nil || req["task_id"] != "task-only" {
		t.Fatalf("unnamed approval correlated to %v", req)
	}
	for _, p := range reg.ListPending() {
		_ = reg.ResolveFrom(p.RequestID, cedar.DecisionDeny, cedar.SourceHost)
	}
	<-resCh
}

// waitForFrames waits until at least n frames of kind have been WRITTEN.
//
// Registry state is not a proxy for wire state: RequestInteractive registers
// the pending entry under its mutex and dispatches only after releasing it, so
// PendingForFamily can reach the cap while several frames are still in flight.
// Tests that read frames must wait on frames.
func waitForFrames(t *testing.T, rc *recordingConn, kind string, n int) {
	t.Helper()
	waitFor(t, func() bool {
		c := 0
		for _, f := range rc.frames(t) {
			if f["kind"] == kind {
				c++
			}
		}
		return c >= n
	})
}

func firstFrameOfKind(t *testing.T, rc *recordingConn, kind string) map[string]any {
	t.Helper()
	for _, f := range rc.frames(t) {
		if f["kind"] == kind {
			return f
		}
	}
	return nil
}
