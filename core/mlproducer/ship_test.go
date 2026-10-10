package mlproducer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
)

// ship_test.go drives the Shipper over REAL sqlite (the recorder_test
// harness) and a fake Poster that decodes each batch the way Fleet does
// (protobuf ExportLogsServiceRequest). The response-table rows are proven
// again through the real fleet.Client in core/rpc/mlproducer_wiring_test.go.

type postFn func(call int, req *collogsv1.ExportLogsServiceRequest, size int) (PostResult, error)

type fakePoster struct {
	mu    sync.Mutex
	fn    postFn
	reqs  []*collogsv1.ExportLogsServiceRequest
	sizes []int
}

func (p *fakePoster) PostLogs(_ context.Context, body []byte) (PostResult, error) {
	var req collogsv1.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return PostResult{}, fmt.Errorf("fake fleet: undecodable batch: %w", err)
	}
	p.mu.Lock()
	p.reqs = append(p.reqs, &req)
	p.sizes = append(p.sizes, len(body))
	n, fn := len(p.reqs), p.fn
	p.mu.Unlock()
	if fn == nil {
		return okResult(recordCount(&req)), nil
	}
	return fn(n, &req, len(body))
}

func (p *fakePoster) setFn(fn postFn) { p.mu.Lock(); p.fn = fn; p.mu.Unlock() }

func (p *fakePoster) snapshot() ([]*collogsv1.ExportLogsServiceRequest, []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*collogsv1.ExportLogsServiceRequest(nil), p.reqs...), append([]int(nil), p.sizes...)
}

func recordCount(req *collogsv1.ExportLogsServiceRequest) int {
	n := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

func okResult(n int) PostResult {
	b, _ := json.Marshal(map[string]any{"accepted": n, "ml": map[string]any{
		"accepted": n, "duplicates": 0, "rejected": 0, "rejections": map[string]int{},
	}})
	return PostResult{Status: 200, Body: b}
}

func errResult(status int, code string) PostResult {
	return PostResult{Status: status, Body: []byte(`{"code":"` + code + `","message":"x"}`)}
}

type shipRig struct {
	*harness
	src    *fakeConsent
	gate   *ConsentGate
	poster *fakePoster
	ship   *Shipper
	gen    atomic.Uint64
	reEnr  atomic.Int32
}

func newShipRig(t *testing.T) *shipRig {
	t.Helper()
	h := newHarness(t)
	r := &shipRig{harness: h, src: devConsent(), poster: &fakePoster{}}
	r.gate = NewConsentGate(GateConfig{Source: r.src, Purge: func(ctx context.Context) error { return h.rec.Purge(ctx) }})
	// The recorder answers to the REAL consent gate here (the harness's
	// default is a bare atomic bool).
	h.rec.Close()
	h.rec = NewRecorder(Config{
		Store: h.store, Hasher: NewHasher(h.dir), Gate: r.gate,
		Workspace: func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1,
	})
	r.ship = NewShipper(ShipperConfig{
		Store: h.store, Poster: r.poster, Gate: r.gate,
		EnrollGen: r.gen.Load,
		ReEnroll: func(context.Context) error {
			r.reEnr.Add(1)
			return nil
		},
		Interval: 20 * time.Millisecond, PollEvery: 5 * time.Millisecond,
		BackoffBase: 10 * time.Millisecond, BackoffMax: 40 * time.Millisecond,
	})
	t.Cleanup(r.ship.Stop)
	return r
}

// seedEvents commits n events straight into the outbox, each body padded
// to roughly pad bytes.
func (r *shipRig) seedEvents(n, pad int) {
	r.t.Helper()
	drafts := make([]mlstore.EventDraft, n)
	for i := range drafts {
		drafts[i] = mlstore.EventDraft{CreatedAt: 1_760_000_000_000, Body: func(seq int64) ([]byte, error) {
			return encodeEvent(seq, KindTool, 1_760_000_000_000, map[string]any{
				"task": "agent-0123456789abcdef", "tool": "kenaz__read_file", "outcome": "ok",
				"dur_ms": int64(1), "pad": strings.Repeat("x", pad),
			})
		}}
	}
	if _, err := r.store.Commit(context.Background(), mlstore.Write{Events: drafts}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *shipRig) pending() int {
	r.t.Helper()
	n, err := r.store.Pending(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *shipRig) shipNow() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.ship.ShipNow(ctx); err != nil {
		r.t.Fatalf("ShipNow: %v", err)
	}
}

// The exact wire shape: one resource with exactly service.name /
// kameas.org.id / kameas.node.id; every record exactly the five
// kameas.ml.* attributes; the body the outbox's JSON as a string.
func TestShipper_ExactWireAttributes(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	r.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	want := r.outbox()
	r.shipNow()

	reqs, _ := r.poster.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("batches = %d, want 1", len(reqs))
	}
	rls := reqs[0].GetResourceLogs()
	if len(rls) != 1 {
		t.Fatalf("resources = %d, want 1", len(rls))
	}
	res := map[string]string{}
	for _, kv := range rls[0].GetResource().GetAttributes() {
		res[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	wantRes := map[string]string{"service.name": "kenaz-harness", "kameas.org.id": "390451413051827052", "kameas.node.id": "NODE1"}
	if fmt.Sprint(res) != fmt.Sprint(wantRes) {
		t.Errorf("resource attrs = %v, want exactly %v", res, wantRes)
	}
	var lrs = rls[0].GetScopeLogs()[0].GetLogRecords()
	if len(lrs) != len(want)-countSupersededTasks(want) {
		t.Fatalf("records = %d, outbox had %d (coalesced %d)", len(lrs), len(want), countSupersededTasks(want))
	}
	for i, lr := range lrs {
		keys := []string{}
		attrs := map[string]string{}
		for _, kv := range lr.GetAttributes() {
			keys = append(keys, kv.GetKey())
			if kv.GetKey() == "kameas.ml.schema_version" {
				if kv.GetValue().GetIntValue() != 1 {
					t.Errorf("record %d schema_version = %v, want int 1", i, kv.GetValue())
				}
				continue
			}
			attrs[kv.GetKey()] = kv.GetValue().GetStringValue()
		}
		sort.Strings(keys)
		if got := strings.Join(keys, ","); got != "kameas.ml.op,kameas.ml.row_id,kameas.ml.schema_version,kameas.ml.source,kameas.ml.table" {
			t.Errorf("record %d attribute keys = %s", i, got)
		}
		if attrs["kameas.ml.source"] != "harness" {
			t.Errorf("record %d source = %q", i, attrs["kameas.ml.source"])
		}
		body := lr.GetBody().GetStringValue()
		switch attrs["kameas.ml.table"] {
		case "events":
			var ev eventBody
			if err := json.Unmarshal([]byte(body), &ev); err != nil {
				t.Fatalf("event body not JSON: %v", err)
			}
			if attrs["kameas.ml.op"] != "insert" || attrs["kameas.ml.row_id"] != fmt.Sprint(ev.ID) {
				t.Errorf("event record op=%q row_id=%q, body id %d", attrs["kameas.ml.op"], attrs["kameas.ml.row_id"], ev.ID)
			}
		case "tasks":
			var tb taskBody
			if err := json.Unmarshal([]byte(body), &tb); err != nil {
				t.Fatalf("task body not JSON: %v", err)
			}
			if attrs["kameas.ml.op"] != "upsert" || attrs["kameas.ml.row_id"] != tb.ID || !strings.HasPrefix(tb.ID, TaskIDPrefix) {
				t.Errorf("task record op=%q row_id=%q id=%q", attrs["kameas.ml.op"], attrs["kameas.ml.row_id"], tb.ID)
			}
		default:
			t.Errorf("record %d table = %q", i, attrs["kameas.ml.table"])
		}
	}
	if r.pending() != 0 {
		t.Errorf("pending after a 2xx = %d, want 0 (cursor = table head)", r.pending())
	}
}

func countSupersededTasks(recs []decoded) int {
	seen := map[string]int{}
	for _, r := range recs {
		if r.Table == mlstore.TableTasks {
			seen[r.RowID]++
		}
	}
	n := 0
	for _, c := range seen {
		n += c - 1
	}
	return n
}

// Task upserts coalesce to the newest per task id within a batch.
func TestShipper_CoalescesTaskUpserts(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	for i := 0; i < 3; i++ {
		task := mlstore.TaskRow{TaskID: "agent-t1", Phase: "coding", Files: map[string]int{}, StartedAt: 1, LastActive: int64(10 + i)}
		body, _ := json.Marshal(taskWireBody(task, ""))
		if _, err := r.store.Commit(ctx, mlstore.Write{Task: &task, TaskUpsert: body}); err != nil {
			t.Fatal(err)
		}
	}
	r.seedEvents(2, 0)
	r.shipNow()
	reqs, _ := r.poster.snapshot()
	var tasks []taskBody
	events := 0
	for _, lr := range reqs[0].GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords() {
		if lr.GetAttributes()[0].GetValue().GetStringValue() == "tasks" {
			var tb taskBody
			_ = json.Unmarshal([]byte(lr.GetBody().GetStringValue()), &tb)
			tasks = append(tasks, tb)
		} else {
			events++
		}
	}
	if len(tasks) != 1 || tasks[0].LastActive != 12 || events != 2 {
		t.Fatalf("tasks = %+v (want one, last_active 12), events = %d (want 2)", tasks, events)
	}
	if r.pending() != 0 {
		t.Fatalf("superseded upserts left in the outbox: %d", r.pending())
	}
}

// ≤ 500 records and ≤ 200 KiB per request; a record over 64 KiB is never
// sent, is counted, and does not wedge the outbox.
func TestShipper_BatchCaps(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.seedEvents(1200, 0)
	r.seedEvents(30, 20<<10)            // ~20 KiB each: forces the byte cap
	r.seedEvents(1, MaxRecordBytes+100) // oversize
	r.seedEvents(3, 0)
	r.shipNow()
	reqs, sizes := r.poster.snapshot()
	total := 0
	for i, req := range reqs {
		n := recordCount(req)
		total += n
		if n > MaxBatchRecords {
			t.Errorf("batch %d has %d records > %d", i, n, MaxBatchRecords)
		}
		if sizes[i] > MaxBatchBytes {
			t.Errorf("batch %d is %d bytes > %d", i, sizes[i], MaxBatchBytes)
		}
		for _, lr := range req.GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords() {
			if len(lr.GetBody().GetStringValue()) > MaxRecordBytes {
				t.Errorf("an oversize record (%d bytes) was sent", len(lr.GetBody().GetStringValue()))
			}
		}
	}
	if total != 1233 {
		t.Errorf("records shipped = %d, want 1233 (1234 minus the oversize one)", total)
	}
	if r.pending() != 0 {
		t.Errorf("pending = %d, want 0", r.pending())
	}
	if st := r.ship.Status(); st.Rejections[rejectionLocalOversize] != 1 {
		t.Errorf("local oversize count = %v, want 1", st.Rejections)
	}
}

// 413 ml_batch_too_large splits the batch in half and resends (new code:
// audit_archive latches instead).
func TestShipper_413SplitsAndResends(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.seedEvents(400, 0)
	r.poster.setFn(func(_ int, req *collogsv1.ExportLogsServiceRequest, _ int) (PostResult, error) {
		if n := recordCount(req); n > 100 {
			return errResult(http.StatusRequestEntityTooLarge, CodeMLBatchTooLarge), nil
		}
		return okResult(recordCount(req)), nil
	})
	r.shipNow()
	reqs, _ := r.poster.snapshot()
	accepted := 0
	for _, req := range reqs {
		if n := recordCount(req); n <= 100 {
			accepted += n
		}
	}
	if accepted != 400 || r.pending() != 0 {
		t.Fatalf("accepted %d of 400 after splits (pending %d); request sizes: %v", accepted, r.pending(), sizesOf(reqs))
	}
	if recordCount(reqs[0]) != 400 || recordCount(reqs[1]) != 200 || recordCount(reqs[2]) != 100 {
		t.Errorf("split sequence = %v, want 400 → 200 → 100 …", sizesOf(reqs))
	}
}

func sizesOf(reqs []*collogsv1.ExportLogsServiceRequest) []int {
	out := make([]int, len(reqs))
	for i, r := range reqs {
		out[i] = recordCount(r)
	}
	return out
}

// 2xx advances the cursor (deletes through the batch's max seq) even when
// Fleet rejects records, and the rejections are counted by reason.
func TestShipper_2xxAdvancesCursorAndCountsRejections(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.seedEvents(5, 0)
	r.poster.setFn(func(int, *collogsv1.ExportLogsServiceRequest, int) (PostResult, error) {
		return PostResult{Status: 200, Body: []byte(`{"accepted":3,"ml":{"accepted":2,"duplicates":1,"rejected":2,` +
			`"rejections":{"outside_retention":1,"invalid_field":1},` +
			`"rejected_records":[{"index":0,"row_id":"1","reason":"outside_retention"},{"index":3,"row_id":"4","reason":"invalid_field"}]}}`)}, nil
	})
	r.shipNow()
	if r.pending() != 0 {
		t.Fatalf("pending = %d after a 2xx with rejections, want 0 (rejected records are not retried)", r.pending())
	}
	st := r.ship.Status()
	if st.Accepted != 2 || st.Duplicates != 1 || st.Rejected != 2 ||
		st.Rejections["outside_retention"] != 1 || st.Rejections["invalid_field"] != 1 || st.LastBatchAt.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	if reqs, _ := r.poster.snapshot(); len(reqs) != 1 {
		t.Fatalf("batches = %d, want 1 (no resend)", len(reqs))
	}
}

// 403 ml_not_effective mid-stream: stop, purge (nothing recorded so far
// ever ships), and the gate closes until a re-read says effective.
func TestShipper_MLNotEffectiveMidStreamPurges(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.seedEvents(MaxBatchRecords+50, 0) // two batches' worth
	r.poster.setFn(func(call int, req *collogsv1.ExportLogsServiceRequest, _ int) (PostResult, error) {
		if call == 1 {
			return okResult(recordCount(req)), nil
		}
		return errResult(http.StatusForbidden, CodeMLNotEffective), nil
	})
	r.shipNow()
	if r.pending() != 0 {
		t.Fatalf("pending = %d after ml_not_effective, want 0 (purged)", r.pending())
	}
	if r.gate.Recording() || r.gate.Last().Reason != ReasonNotEffective {
		t.Fatalf("gate after ml_not_effective = %+v, want closed / %s", r.gate.Last(), ReasonNotEffective)
	}
	if st := r.ship.Status(); st.StopReason != ReasonNotEffective {
		t.Errorf("stop reason = %q", st.StopReason)
	}
	// Recording is closed: a new call writes nothing.
	r.call(ctx, "s1", "kenaz__read_file", `{"path":"a"}`, coreag.ToolOutcomeOK, "x")
	if n := len(r.outbox()); n != 0 {
		t.Fatalf("recorded %d rows after the stop", n)
	}
	// The next batch re-reads /me/ml; still not effective → still nothing.
	r.src.set(func(f *fakeConsent) { f.eff = false })
	r.seedEvents(1, 0)
	before, _ := r.poster.snapshot()
	r.shipNow()
	after, _ := r.poster.snapshot()
	if len(after) != len(before) {
		t.Fatal("shipped while /me/ml says not effective")
	}
}

// 403 ml_node_not_enrolled: hold, trigger re-enroll, do not resend the
// batch until the enrolment generation moves.
func TestShipper_NodeNotEnrolledHoldsUntilEnrolmentChanges(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.seedEvents(3, 0)
	r.poster.setFn(func(int, *collogsv1.ExportLogsServiceRequest, int) (PostResult, error) {
		return errResult(http.StatusForbidden, CodeMLNodeNotEnrolled), nil
	})
	r.shipNow()
	r.shipNow()
	r.shipNow()
	if reqs, _ := r.poster.snapshot(); len(reqs) != 1 {
		t.Fatalf("posts = %d, want 1 (held after ml_node_not_enrolled)", len(reqs))
	}
	waitFor(t, func() bool { return r.reEnr.Load() == 1 }, "re-enroll triggered")
	if r.pending() != 3 || r.ship.Status().StopReason != CodeMLNodeNotEnrolled {
		t.Fatalf("pending=%d stop=%q", r.pending(), r.ship.Status().StopReason)
	}
	r.poster.setFn(nil)
	r.gen.Add(1) // enrolment changed
	r.shipNow()
	if r.pending() != 0 {
		t.Fatalf("pending = %d after enrolment changed, want 0", r.pending())
	}
}

// 400 unsupported_schema_version / ml_invalid_table: stop for good,
// "update the harness"; nothing is resent and the outbox is kept.
func TestShipper_SchemaRefusalLatches(t *testing.T) {
	t.Parallel()
	for _, code := range []string{CodeUnsupportedSchema, CodeMLInvalidTable} {
		code := code
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			r := newShipRig(t)
			r.gate.Refresh(context.Background())
			r.seedEvents(2, 0)
			r.poster.setFn(func(int, *collogsv1.ExportLogsServiceRequest, int) (PostResult, error) {
				return errResult(http.StatusBadRequest, code), nil
			})
			r.shipNow()
			r.shipNow()
			r.ship.Start(context.Background()) // refuses: latched
			time.Sleep(50 * time.Millisecond)
			if reqs, _ := r.poster.snapshot(); len(reqs) != 1 {
				t.Fatalf("posts = %d, want 1", len(reqs))
			}
			if st := r.ship.Status(); !strings.HasPrefix(st.StopReason, StopUpdateHarness) || !strings.Contains(st.StopReason, code) || st.Running {
				t.Fatalf("status = %+v", st)
			}
			if r.pending() != 2 {
				t.Fatalf("pending = %d, want 2 (kept)", r.pending())
			}
		})
	}
}

// 429 / 503 wait Retry-After; 401 (after the client's one refresh) and
// org_paused are errors that back off. None of them loses records.
func TestShipper_RetryAfterAndErrorsKeepRecords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		res  PostResult
		err  error
		wait time.Duration
		stop string
	}{
		{"429 rate_limited", PostResult{Status: 429, RetryAfter: "7", Body: []byte(`{"code":"rate_limited"}`)}, nil, 7 * time.Second, ""},
		{"429 daily cap (HTTP date)", PostResult{Status: 429, RetryAfter: "", Body: []byte(`{"code":"ml_daily_cap_exceeded"}`)}, nil, 0, ""},
		{"503 ml_tenant_unavailable", PostResult{Status: 503, RetryAfter: "300", Body: []byte(`{"code":"ml_tenant_unavailable"}`)}, nil, 300 * time.Second, ""},
		{"401 after refresh", PostResult{}, errors.New("fleet: token expired"), 0, ""},
		{"org paused", PostResult{}, fmt.Errorf("%w: paused", ErrOrgPaused), 0, ReasonOrgPaused},
		{"403 org_paused body", errResult(403, "org_paused"), nil, 0, ReasonOrgPaused},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newShipRig(t)
			ctx := context.Background()
			r.gate.Refresh(ctx)
			r.seedEvents(4, 0)
			r.poster.setFn(func(int, *collogsv1.ExportLogsServiceRequest, int) (PostResult, error) { return tc.res, tc.err })
			res := r.ship.cycle(ctx)
			if res.progressed {
				t.Fatal("a refused batch counted as progress")
			}
			if tc.wait > 0 && res.wait != tc.wait {
				t.Errorf("wait = %v, want Retry-After %v", res.wait, tc.wait)
			}
			if tc.wait == 0 && !res.backoff {
				t.Errorf("result = %+v, want backoff", res)
			}
			if r.pending() != 4 {
				t.Errorf("pending = %d, want 4 (kept)", r.pending())
			}
			if tc.stop != "" && r.ship.Status().StopReason != tc.stop {
				t.Errorf("stop = %q, want %q", r.ship.Status().StopReason, tc.stop)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		v    string
		want time.Duration
		ok   bool
	}{
		{"30", 30 * time.Second, true},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{"999999", maxRetryAfter, true},
		{"", 0, false},
		{"soon", 0, false},
	} {
		got, ok := parseRetryAfter(tc.v, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %v,%v want %v,%v", tc.v, got, ok, tc.want, tc.ok)
		}
	}
}

// A closed gate ships nothing (and never posts); consent withdrawn is one
// such closure.
func TestShipper_ClosedGateShipsNothing(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	r.seedEvents(3, 0) // left from a previous session
	r.src.set(func(f *fakeConsent) { f.eff = false })
	r.shipNow()
	if reqs, _ := r.poster.snapshot(); len(reqs) != 0 {
		t.Fatalf("posted %d batches while not effective", len(reqs))
	}
	if r.pending() != 0 {
		t.Fatalf("pending = %d: entering the ml_not_effective closure must purge", r.pending())
	}
	if r.ship.Status().StopReason != ReasonNotEffective {
		t.Errorf("stop = %q", r.ship.Status().StopReason)
	}
}

// Lifecycle: Start is idempotent, the loop ships on its cadence, a pending
// pile-up wakes it early, Pause stops and is restartable, Stop is final.
func TestShipper_Lifecycle(t *testing.T) {
	t.Parallel()
	r := newShipRig(t)
	ctx := context.Background()
	r.gate.Refresh(ctx)
	r.ship.Start(ctx)
	r.ship.Start(ctx)
	if !r.ship.Running() {
		t.Fatal("not running after Start")
	}
	r.seedEvents(2, 0)
	waitFor(t, func() bool { return r.pending() == 0 }, "loop ships")
	r.ship.Pause(ReasonSignedOut)
	if r.ship.Running() || r.ship.Status().StopReason != ReasonSignedOut {
		t.Fatalf("after Pause: running=%v stop=%q", r.ship.Running(), r.ship.Status().StopReason)
	}
	r.seedEvents(1, 0)
	time.Sleep(60 * time.Millisecond)
	if r.pending() != 1 {
		t.Fatal("a paused shipper shipped")
	}
	r.ship.Start(ctx)
	waitFor(t, func() bool { return r.pending() == 0 }, "restarted loop ships")
	r.ship.Stop()
	r.ship.Start(ctx)
	if r.ship.Running() {
		t.Fatal("Start after Stop restarted the loop")
	}
}

func TestShipper_WakesEarlyOnPendingPileUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	src := devConsent()
	gate := NewConsentGate(GateConfig{Source: src, Purge: h.rec.Purge})
	poster := &fakePoster{}
	s := NewShipper(ShipperConfig{Store: h.store, Poster: poster, Gate: gate,
		Interval: time.Hour, WakePending: 10, PollEvery: 5 * time.Millisecond})
	t.Cleanup(s.Stop)
	gate.Refresh(context.Background())
	s.Start(context.Background())
	r := &shipRig{harness: h}
	time.Sleep(30 * time.Millisecond) // first cycle: empty → idle for an hour
	r.seedEvents(3, 0)
	time.Sleep(60 * time.Millisecond)
	if reqs, _ := poster.snapshot(); len(reqs) != 0 {
		t.Fatal("shipped below the wake threshold before the interval")
	}
	r.seedEvents(10, 0)
	waitFor(t, func() bool { reqs, _ := poster.snapshot(); return len(reqs) == 1 }, "early wake at ≥ WakePending")
}

func TestAgentPIDs_WriteAndRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := WriteAgentPIDs(dir, 4242); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(AgentPIDsPath(dir))
	if err != nil || string(b) != "4242\n" {
		t.Fatalf("agent_pids = %q, %v", b, err)
	}
	fi, _ := os.Stat(AgentPIDsPath(dir))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if err := RemoveAgentPIDs(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(AgentPIDsPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("agent_pids still present: %v", err)
	}
	if err := RemoveAgentPIDs(dir); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}
