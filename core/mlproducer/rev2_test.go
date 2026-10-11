package mlproducer

// rev2_test.go — the Rev2Kinds (AI-usage) ship gate (owner decision
// 2026-10-10; Fleet's rev-2 gate on GET /me/ml ai_usage_allowed):
// agent.turn enters the ship outbox only while the gate's consent carries
// AIUsageAllowed, every other kind is unaffected, the shipper drops a
// queued agent.turn whose permission was revoked before it shipped, and
// agent.turn values are clamped to PayloadBounds. Real sqlite throughout.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	collogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

func TestRev2Kinds_AreAgentToolAndTurn(t *testing.T) {
	if len(Rev2Kinds) != 2 || !IsRev2Kind(KindTurn) || !IsRev2Kind(KindTool) {
		t.Fatalf("Rev2Kinds = %v, want [agent.tool agent.turn]", Rev2Kinds)
	}
	for _, k := range Kinds() {
		if k != KindTurn && k != KindTool && IsRev2Kind(k) {
			t.Errorf("%s is a Rev2Kind", k)
		}
	}
	for kind, keys := range PayloadBounds {
		allowed := map[string]bool{}
		for _, k := range PayloadKeys(kind) {
			allowed[k] = true
		}
		for k := range keys {
			if !allowed[k] {
				t.Errorf("PayloadBounds[%s] bounds %q, which is not a payload key", kind, k)
			}
		}
	}
}

// The §8.1 shape driven twice: AI usage allowed vs not. Without it, no
// agent.turn is stored, and every other record (kinds, the idle phase
// change, task counters) is identical.
func TestRecorder_AgentTurnNeedsAIUsage(t *testing.T) {
	t.Parallel()
	run := func(ai bool) ([]decoded, *harness) {
		h := newHarness(t)
		h.ai.Store(ai)
		ctx := context.Background()
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
		h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashFail)
		h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m x"}`, coreag.ToolOutcomeOK, bashOK)
		h.rec.TurnEnded(ctx, "s1", "completed", 2, 3, time.Second)
		h.rec.TurnEnded(ctx, "s1", "completed", 1, 0, time.Second) // tool-less → idle
		return h.outbox(), h
	}
	with, hw := run(true)
	without, hn := run(false)

	if n := len(events(with, KindTurn)); n != 2 {
		t.Fatalf("ai_usage_allowed=true: agent.turn = %d, want 2", n)
	}
	if n := len(events(without, KindTurn)); n != 0 {
		t.Fatalf("ai_usage_allowed=false: agent.turn = %d, want 0 (never stored)", n)
	}
	for _, k := range Kinds() {
		if IsRev2Kind(k) {
			continue
		}
		if a, b := len(events(with, k)), len(events(without, k)); a != b {
			t.Errorf("%s: %d with AI usage, %d without — other kinds must be unaffected", k, a, b)
		}
	}
	idle := func(recs []decoded) bool {
		for _, p := range events(recs, KindPhaseChange) {
			if p.event.Payload["phase"] == string(PhaseIdle) {
				return true
			}
		}
		return false
	}
	if !idle(without) || !idle(with) {
		t.Error("the tool-less turn's idle phase change must record whether or not agent.turn does")
	}
	ctx := context.Background()
	rw, _, _ := hw.store.LoadTask(ctx, hw.taskID("s1"))
	rn, _, _ := hn.store.LoadTask(ctx, hn.taskID("s1"))
	if rw.CommitCount != rn.CommitCount || rw.TestRuns != rn.TestRuns || rw.TestFails != rn.TestFails ||
		len(rw.Files) != len(rn.Files) || rw.Phase != rn.Phase || rn.Phase != string(PhaseIdle) {
		t.Errorf("task state differs: with %+v, without %+v", rw, rn)
	}
	if rn.CommitCount != 1 || rn.TestRuns != 1 || rn.TestFails != 1 {
		t.Errorf("counters without AI usage = %+v, want 1 commit / 1 run / 1 fail", rn)
	}
}

// A Gate that is not an AIUsageGate (a bare GateFunc) never queues
// agent.turn: fail closed.
func TestRecorder_PlainGateFuncWithholdsAgentTurn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.Close()
	h.rec = NewRecorder(Config{Store: h.store, Hasher: NewHasher(h.dir), Gate: GateFunc(func() bool { return true }),
		Workspace: func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1})
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	h.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	recs := h.outbox()
	if n := len(events(recs, KindTurn)); n != 0 {
		t.Fatalf("agent.turn = %d under a gate with no AI-usage answer, want 0", n)
	}
	if n := len(events(recs, KindFile)); n != 1 {
		t.Fatalf("file = %d, want 1", n)
	}
}

func TestRecorder_AgentTurnValuesClamped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	h.rec.TurnEnded(ctx, "s1", "completed", 50_000, 10_001, 48*time.Hour)
	h.rec.TurnEnded(ctx, "s1", "completed", -4, 7, -time.Second)
	turns := events(h.outbox(), KindTurn)
	if len(turns) != 2 {
		t.Fatalf("agent.turn = %d, want 2", len(turns))
	}
	hi, lo := turns[0].event.Payload, turns[1].event.Payload
	if hi["dur_ms"] != float64(86_400_000) || hi["model_calls"] != float64(10_000) || hi["tool_calls"] != float64(10_000) {
		t.Errorf("over-range turn = %v, want clamped to 86400000 / 10000 / 10000", hi)
	}
	if lo["dur_ms"] != float64(0) || lo["model_calls"] != float64(0) || lo["tool_calls"] != float64(7) {
		t.Errorf("under-range turn = %v, want 0 / 0 / 7", lo)
	}
}

func TestClampPayload(t *testing.T) {
	p := ClampPayload(KindTurn, map[string]any{"task": "t", "dur_ms": int64(90_000_000), "model_calls": -1, "tool_calls": 5, "outcome": "completed"})
	if p["dur_ms"] != int64(86_400_000) || p["model_calls"] != int64(0) || p["tool_calls"] != int64(5) || p["task"] != "t" {
		t.Errorf("ClampPayload = %v", p)
	}
	q := ClampPayload(KindTool, map[string]any{"dur_ms": int64(1 << 40), "tool": "kenaz__bash"})
	if q["dur_ms"] != int64(86_400_000) || q["tool"] != "kenaz__bash" {
		t.Errorf("agent.tool clamp = %v", q)
	}
	r := ClampPayload(KindTerminal, map[string]any{"exit_code": 1 << 20})
	if r["exit_code"] != 1<<20 {
		t.Errorf("unbounded kind changed: %v", r)
	}
}

// Counsel D43/C3: agent.tool is a Rev2Kind. Without AI usage it never
// enters the outbox; the read's file events, the phase change and the
// task counters are unchanged.
func TestRecorder_AgentToolNeedsAIUsage(t *testing.T) {
	t.Parallel()
	run := func(ai bool) ([]decoded, *harness) {
		h := newHarness(t)
		h.ai.Store(ai)
		ctx := context.Background()
		h.call(ctx, "s1", "kenaz__read_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, `{"content":"x","byte_size":1,"truncated":false}`)
		h.call(ctx, "s1", "github__create_issue", `{"title":"x"}`, coreag.ToolOutcomeOK, `{}`)
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"b.go"}`, coreag.ToolOutcomeDenied, "")
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"c.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
		return h.outbox(), h
	}
	with, hw := run(true)
	without, hn := run(false)
	if n := len(events(with, KindTool)); n != 3 {
		t.Fatalf("ai=true: agent.tool = %d, want 3 (read, MCP, denied write)", n)
	}
	if n := len(events(without, KindTool)); n != 0 {
		t.Fatalf("ai=false: agent.tool = %d, want 0", n)
	}
	for _, k := range []string{KindFile, KindTerminal, KindCommit, KindPhaseChange} {
		if a, b := len(events(with, k)), len(events(without, k)); a != b {
			t.Errorf("%s: %d with AI usage, %d without", k, a, b)
		}
	}
	if n := len(events(without, KindFile)); n != 2 {
		t.Errorf("file events without AI usage = %d, want 2 (the read and the write)", n)
	}
	ctx := context.Background()
	rw, _, _ := hw.store.LoadTask(ctx, hw.taskID("s1"))
	rn, _, _ := hn.store.LoadTask(ctx, hn.taskID("s1"))
	if rw.Phase != rn.Phase || len(rw.Files) != len(rn.Files) || rw.LastActive == 0 || rn.LastActive == 0 {
		t.Errorf("task state differs: with %+v, without %+v", rw, rn)
	}
	// With AI usage, the MCP tool is hashed and the bound holds.
	for _, e := range events(with, KindTool) {
		tool, _ := e.event.Payload["tool"].(string)
		if tool == "github__create_issue" || (!IsBuiltinToolName(tool) && !strings.HasPrefix(tool, MCPToolPrefix)) {
			t.Errorf("agent.tool tool = %q, want a builtin or mcp:<hash>", tool)
		}
	}
}

func TestRecorder_AgentToolDurationClamped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"title": "x"})
	h.rec.ToolCallCompleted(coreag.ToolCallRecord{Ctx: ctx, SessionID: "s1", ToolName: "github__create_issue",
		Outcome: coreag.ToolOutcomeOK, Duration: 30 * time.Hour, RawArgs: string(raw)})
	h.rec.ToolCallCompleted(coreag.ToolCallRecord{Ctx: ctx, SessionID: "s1", ToolName: "github__create_issue",
		Outcome: coreag.ToolOutcomeOK, Duration: -time.Second, RawArgs: string(raw)})
	tools := events(h.outbox(), KindTool)
	if len(tools) != 2 || tools[0].event.Payload["dur_ms"] != float64(86_400_000) || tools[1].event.Payload["dur_ms"] != float64(0) {
		t.Fatalf("agent.tool dur_ms = %+v, want 86400000 then 0", tools)
	}
}

func shippedKinds(t *testing.T, reqs []*collogsv1.ExportLogsServiceRequest) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, req := range reqs {
		for _, rl := range req.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				for _, lr := range sl.GetLogRecords() {
					var ev eventBody
					if json.Unmarshal([]byte(lr.GetBody().GetStringValue()), &ev) == nil && ev.Kind != "" {
						out[ev.Kind]++
					}
				}
			}
		}
	}
	return out
}

// Through the REAL ConsentGate: ai_usage_allowed true → agent.turn
// records and ships; false (or absent) → it is never queued and the rest
// ships.
func TestShipper_AgentTurnShipsOnlyWithAIUsage(t *testing.T) {
	t.Parallel()
	for _, ai := range []bool{true, false} {
		r := newShipRig(t)
		r.src.set(func(f *fakeConsent) { f.ai = ai })
		ctx := context.Background()
		if d := r.gate.Refresh(ctx); !d.Open || d.AIUsageAllowed != ai {
			t.Fatalf("ai=%v: decision = %+v", ai, d)
		}
		r.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
		r.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
		if n := len(events(r.outbox(), KindTurn)); (n == 1) != ai {
			t.Fatalf("ai=%v: agent.turn in the outbox = %d", ai, n)
		}
		r.shipNow()
		reqs, _ := r.poster.snapshot()
		got := shippedKinds(t, reqs)
		if (got[KindTurn] == 1) != ai || got[KindFile] != 1 {
			t.Errorf("ai=%v: shipped kinds = %v", ai, got)
		}
		if r.pending() != 0 {
			t.Errorf("ai=%v: pending = %d after ship", ai, r.pending())
		}
	}
}

// Recorded while allowed, revoked before the batch: the shipper's fresh
// read sees ai_usage_allowed=false and drops the queued agent.turn unsent
// (settled, never retried), while the rest of the batch ships.
func TestShipper_AgentTurnRevokedBeforeShipIsDropped(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	r.src.set(func(f *fakeConsent) { f.ai = true })
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	r.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	if n := len(events(r.outbox(), KindTurn)); n != 1 {
		t.Fatalf("agent.turn in the outbox = %d, want 1", n)
	}

	r.src.set(func(f *fakeConsent) { f.ai = false })
	r.shipNow()
	reqs, _ := r.poster.snapshot()
	got := shippedKinds(t, reqs)
	if got[KindTurn] != 0 {
		t.Fatalf("a revoked agent.turn shipped: %v", got)
	}
	if got[KindFile] != 1 {
		t.Errorf("the rest of the batch did not ship: %v", got)
	}
	if r.pending() != 0 {
		t.Errorf("pending = %d, want 0 (the withheld record is settled, not retried)", r.pending())
	}
	if st := r.ship.Status(); st.Rejections[rejectionAIUsageWithheld] != 1 {
		t.Errorf("rejections = %v, want %s=1", st.Rejections, rejectionAIUsageWithheld)
	}

	// A window holding only withheld records settles without a POST.
	r.src.set(func(f *fakeConsent) { f.ai = true })
	r.gate.Refresh(ctx)
	r.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	r.flush()
	r.src.set(func(f *fakeConsent) { f.ai = false })
	before, _ := r.poster.snapshot()
	r.shipNow()
	after, _ := r.poster.snapshot()
	if got := shippedKinds(t, after[len(before):]); got[KindTurn] != 0 {
		t.Errorf("withheld-only window shipped agent.turn: %v", got)
	}
	if r.pending() != 0 {
		t.Errorf("pending = %d after a withheld-only window, want 0", r.pending())
	}
}

func TestPrepareBatch_WithholdsRev2KindsOnly(t *testing.T) {
	body := func(kind string) []byte {
		b, _ := encodeEvent(1, kind, 1, map[string]any{"task": "agent-0123456789abcdef"})
		return b
	}
	var window []mlstore.Record
	for i, k := range []string{KindFile, KindTurn, KindTool, KindTurn} {
		window = append(window, mlstore.Record{Seq: int64(i + 1), Table: mlstore.TableEvents, Op: mlstore.OpInsert,
			RowID: fmt.Sprint(i + 1), Body: body(k)})
	}
	window = append(window, mlstore.Record{Seq: 5, Table: mlstore.TableTasks, Op: mlstore.OpUpsert,
		RowID: "agent-0123456789abcdef", Body: []byte(`{"id":"agent-0123456789abcdef"}`)})
	send, oversize, withheld := prepareBatch(window, false)
	if len(send) != 2 || oversize != 0 || withheld != 3 {
		t.Fatalf("aiUsage=false: send %d, oversize %d, withheld %d; want 2/0/3 (file + task ship; agent.tool and both agent.turn withheld)", len(send), oversize, withheld)
	}
	send, _, withheld = prepareBatch(window, true)
	if len(send) != 5 || withheld != 0 {
		t.Fatalf("aiUsage=true: send %d, withheld %d; want 5/0", len(send), withheld)
	}
}
