package fleet

// usage_lane_e2e_test.go — the usage lanes, asserted on the bytes that leave.
//
// Every test here drives the real pipeline (real OTLP exporters, real SDK
// providers, real HTTP) into a fake Fleet that decodes the protobuf the way
// the receiver does. Nothing is asserted on an in-process fake exporter: the
// question each test answers is "what did the wire carry, and as whom".

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// ── fake Fleet ───────────────────────────────────────────────────────────────

type capturedRequest struct {
	path   string
	bearer string
	raw    []byte
}

type fakeFleet struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []capturedRequest
	// statusFor returns the status to answer the nth (0-based) request on a
	// path with. nil ⇒ always 200.
	statusFor func(path string, n int) int
	seen      map[string]int
}

func newFakeFleet(t *testing.T) *fakeFleet {
	t.Helper()
	f := &fakeFleet{t: t, seen: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFleet) base() string { return f.srv.URL + "/otlp" }

func (f *fakeFleet) handle(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		defer zr.Close()
		body = zr
	}
	raw, _ := io.ReadAll(body)

	f.mu.Lock()
	n := f.seen[r.URL.Path]
	f.seen[r.URL.Path] = n + 1
	status := http.StatusOK
	if f.statusFor != nil {
		status = f.statusFor(r.URL.Path, n)
	}
	f.requests = append(f.requests, capturedRequest{
		path:   r.URL.Path,
		bearer: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		raw:    raw,
	})
	f.mu.Unlock()

	// Fleet answers with its JSON ingestResponse, not an OTLP protobuf ack.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"accepted":1}`))
}

func (f *fakeFleet) snapshot() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeFleet) byPath(path string) []capturedRequest {
	var out []capturedRequest
	for _, r := range f.snapshot() {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

// allBytes concatenates every captured body — the haystack for "this string
// never left the process" assertions.
func (f *fakeFleet) allBytes() []byte {
	var buf bytes.Buffer
	for _, r := range f.snapshot() {
		buf.Write(r.raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// ── decoded views ────────────────────────────────────────────────────────────

type decodedEvent struct {
	kind     string
	body     map[string]any
	resource map[string]string
	attrs    map[string]string
}

func decodeLogs(t *testing.T, reqs []capturedRequest) []decodedEvent {
	t.Helper()
	var out []decodedEvent
	for _, r := range reqs {
		var msg collogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(r.raw, &msg); err != nil {
			t.Fatalf("decode logs request: %v", err)
		}
		for _, rl := range msg.GetResourceLogs() {
			res := kvStrings(rl.GetResource().GetAttributes())
			for _, sl := range rl.GetScopeLogs() {
				for _, lr := range sl.GetLogRecords() {
					attrs := kvStrings(lr.GetAttributes())
					out = append(out, decodedEvent{
						kind:     attrs[AttrEventKind],
						body:     anyToGo(lr.GetBody()).(map[string]any),
						resource: res,
						attrs:    attrs,
					})
				}
			}
		}
	}
	return out
}

type decodedPoint struct {
	name       string
	value      int64
	labelCount int
	delta      bool
	resource   map[string]string
}

func decodeMetrics(t *testing.T, reqs []capturedRequest) []decodedPoint {
	t.Helper()
	var out []decodedPoint
	for _, r := range reqs {
		var msg colmetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(r.raw, &msg); err != nil {
			t.Fatalf("decode metrics request: %v", err)
		}
		for _, rm := range msg.GetResourceMetrics() {
			res := kvStrings(rm.GetResource().GetAttributes())
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					sum := m.GetSum()
					if sum == nil {
						t.Fatalf("metric %q is not a Sum", m.GetName())
					}
					for _, dp := range sum.GetDataPoints() {
						out = append(out, decodedPoint{
							name:       m.GetName(),
							value:      dp.GetAsInt(),
							labelCount: len(dp.GetAttributes()),
							// AGGREGATION_TEMPORALITY_DELTA == 1
							delta:    int32(sum.GetAggregationTemporality()) == 1,
							resource: res,
						})
					}
				}
			}
		}
	}
	return out
}

func sumByName(points []decodedPoint) map[string]int64 {
	out := map[string]int64{}
	for _, p := range points {
		out[p.name] += p.value
	}
	return out
}

func kvStrings(kvs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	return out
}

func anyToGo(av *commonpb.AnyValue) any {
	switch v := av.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_BoolValue:
		return v.BoolValue
	case *commonpb.AnyValue_IntValue:
		return v.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return v.DoubleValue
	case *commonpb.AnyValue_KvlistValue:
		m := map[string]any{}
		for _, kv := range v.KvlistValue.GetValues() {
			m[kv.GetKey()] = anyToGo(kv.GetValue())
		}
		return m
	default:
		return nil
	}
}

// ── fixtures ─────────────────────────────────────────────────────────────────

// fakeJWT builds an unsigned compact JWT whose payload carries sub. The
// pipeline only decodes the payload; signature verification is Fleet's job.
func fakeJWT(sub string) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(map[string]string{"sub": sub}) + ".sig"
}

// tokenBox is a swappable bearer source.
type tokenBox struct {
	mu  sync.Mutex
	tok string
}

func (b *tokenBox) set(tok string) { b.mu.Lock(); b.tok = tok; b.mu.Unlock() }
func (b *tokenBox) provider() BearerProvider {
	return func() (string, error) {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.tok, nil
	}
}

type staticConsent struct {
	mu    sync.Mutex
	level ConsentLevel
}

func (c *staticConsent) EffectiveLevel() ConsentLevel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.level
}
func (c *staticConsent) set(l ConsentLevel) { c.mu.Lock(); c.level = l; c.mu.Unlock() }

var (
	identityA = IdentityAttrs{UserID: "sub-alice", OrgID: "org-alpha", MachineID: "machine-01"}
	identityB = IdentityAttrs{UserID: "sub-bob", OrgID: "org-beta", MachineID: "machine-01"}
)

func optIns(classes ...string) []TelemetryOptInItem {
	out := make([]TelemetryOptInItem, 0, len(classes))
	for _, c := range classes {
		out = append(out, TelemetryOptInItem{Class: c, OptedIn: true})
	}
	return out
}

// activeRig is a pipeline activated against a fake Fleet.
type activeRig struct {
	fleet    *fakeFleet
	pipeline *FleetOTLPPipeline
	consent  *staticConsent
	emitter  *UsageEmitter
	tokens   *tokenBox
}

func newActiveRig(t *testing.T, level ConsentLevel, id IdentityAttrs, snapshot []TelemetryOptInItem) *activeRig {
	t.Helper()
	f := newFakeFleet(t)
	p := NewFleetOTLPPipeline(nil)
	// Long cadences: tests flush explicitly, so nothing races the assertions.
	p.SetExportCadence(time.Hour, time.Hour)
	p.SetTelemetryOptIns(snapshot)
	p.SetLogLaneEnabled(level == ConsentFull)
	tokens := &tokenBox{tok: fakeJWT(id.UserID)}
	if err := p.Activate(context.Background(), f.base(), nil, id, tokens.provider(), nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { p.Deactivate(context.Background()) })
	consent := &staticConsent{level: level}
	return &activeRig{fleet: f, pipeline: p, consent: consent, emitter: NewUsageEmitter(p, consent), tokens: tokens}
}

func (r *activeRig) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.pipeline.Flush(ctx)
}

var usageClasses = []string{"harness.usage_counts", "harness.tool_calls", "harness.errors"}

// ── tests ────────────────────────────────────────────────────────────────────

func TestUsageLane_FullConsent_SendsIdentityStampedEvents(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	ctx := context.Background()

	if !r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", "anthropic") {
		t.Fatal("conversation_started not accepted under full consent")
	}
	r.emitter.ToolInvoked(ctx, "kenaz__bash", 120*time.Millisecond, true)
	r.emitter.ConversationEnded(ctx, "11111111-2222-4333-8444-555555555555", 42*time.Second, 1500, 800, 0.042)
	r.emitter.Error(ctx, ErrorCategoryTransient, true)
	r.flush()

	logReqs := r.fleet.byPath("/otlp/v1/logs")
	if len(logReqs) == 0 {
		t.Fatalf("no request reached /otlp/v1/logs; paths seen: %v", pathsOf(r.fleet.snapshot()))
	}
	for _, req := range logReqs {
		if req.bearer != fakeJWT(identityA.UserID) {
			t.Errorf("log export carried the wrong bearer")
		}
	}

	events := decodeLogs(t, logReqs)
	kinds := map[string]decodedEvent{}
	for _, e := range events {
		kinds[e.kind] = e
		// Resource identity: exactly what Fleet's validateResourceAttrs checks.
		if e.resource["kameas.user.id"] != identityA.UserID ||
			e.resource["kameas.org.id"] != identityA.OrgID ||
			e.resource["kameas.machine.id"] != identityA.MachineID {
			t.Errorf("event %s resource = %v, want identity %+v", e.kind, e.resource, identityA)
		}
		// Attribute budget: Fleet declares exactly one log-record attribute.
		if len(e.attrs) != 1 {
			t.Errorf("event %s carries attributes %v; the budget allows only %s", e.kind, e.attrs, AttrEventKind)
		}
	}
	for _, want := range []string{
		"harness.conversation_started", "harness.conversation_ended",
		"harness.tool_invoked", "harness.error",
	} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("kind %s did not reach Fleet; got %v", want, keysOf(kinds))
		}
	}

	// Bodies must match Fleet's closed structs exactly (DisallowUnknownFields
	// on the far side turns one stray key into a dropped event).
	assertBodyKeys(t, kinds["harness.conversation_started"], "conversation_id", "model_provider")
	assertBodyKeys(t, kinds["harness.conversation_ended"], "conversation_id", "duration_ms", "token_in", "token_out", "cost_usd")
	assertBodyKeys(t, kinds["harness.tool_invoked"], "tool_name", "latency_ms", "success")
	assertBodyKeys(t, kinds["harness.error"], "category", "recoverable")

	if got := kinds["harness.conversation_ended"].body["token_in"]; got != int64(1500) {
		t.Errorf("token_in = %v, want 1500", got)
	}

	// Exclusive routing: full consent must not ALSO increment the counters,
	// or Fleet's rollup counts every conversation twice.
	if pts := decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics")); len(pts) != 0 {
		t.Errorf("full consent exported counters %v; events and counters must be mutually exclusive", sumByName(pts))
	}
}

func TestUsageLane_AggregateConsent_SendsLabellessDeltaCountersAndNoLogRecords(t *testing.T) {
	r := newActiveRig(t, ConsentAggregate, identityA, optIns(usageClasses...))
	ctx := context.Background()

	r.emitter.ConversationStarted(ctx, "ignored-under-aggregate", "anthropic")
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.emitter.ToolInvoked(ctx, "mcp__acme-prod-db__query", time.Second, false)
	r.emitter.ToolInvoked(ctx, "kenaz__read_file", time.Second, true)
	r.emitter.Error(ctx, ErrorCategoryAuth, false)
	r.emitter.ConversationEnded(ctx, "ignored-under-aggregate", time.Minute, 1500, 800, 0.5)
	r.flush()

	// The Aggregate tier's user-facing promise.
	if reqs := r.fleet.byPath("/otlp/v1/logs"); len(reqs) != 0 {
		t.Fatalf("aggregate consent sent %d log request(s); it promises no log records", len(reqs))
	}

	points := decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics"))
	if len(points) == 0 {
		t.Fatalf("no counters reached /otlp/v1/metrics; paths seen: %v", pathsOf(r.fleet.snapshot()))
	}
	for _, p := range points {
		if p.labelCount != 0 {
			t.Errorf("counter %s carries %d label(s); the metric budget declares none", p.name, p.labelCount)
		}
		if !p.delta {
			t.Errorf("counter %s is not delta temporality; Fleet sums data points, so cumulative would over-count", p.name)
		}
		if p.resource["kameas.user.id"] != identityA.UserID || p.resource["kameas.org.id"] != identityA.OrgID ||
			p.resource["kameas.machine.id"] != identityA.MachineID {
			t.Errorf("counter %s resource = %v, want identity %+v", p.name, p.resource, identityA)
		}
		if _, declared := UsageCounterClass(UsageCounter(p.name)); !declared {
			t.Errorf("undeclared metric %q left the process", p.name)
		}
	}
	got := sumByName(points)
	want := map[string]int64{
		"harness.conversations.started": 1,
		"harness.conversations.ended":   1,
		"harness.tool.invocations":      3,
		"harness.errors":                1,
		"harness.tokens.input":          1500,
		"harness.tokens.output":         800,
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s = %d, want %d (all: %v)", name, got[name], n, got)
		}
	}
}

func TestUsageLane_DeltaCountersDoNotResendPreviousIntervals(t *testing.T) {
	r := newActiveRig(t, ConsentAggregate, identityA, optIns(usageClasses...))
	ctx := context.Background()

	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.flush()
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.flush()
	r.flush() // an empty interval must contribute nothing

	got := sumByName(decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics")))
	if got["harness.tool.invocations"] != 3 {
		t.Errorf("sum over all exported points = %d, want exactly 3 — Fleet adds points up",
			got["harness.tool.invocations"])
	}
}

func TestUsageLane_ConsentNone_SendsNothing(t *testing.T) {
	r := newActiveRig(t, ConsentNone, identityA, optIns(usageClasses...))
	ctx := context.Background()

	if r.emitter.ConversationStarted(ctx, "id", "anthropic") {
		t.Error("conversation_started accepted under consent none")
	}
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
	r.emitter.Error(ctx, ErrorCategoryAuth, false)
	r.emitter.ConversationEnded(ctx, "id", time.Minute, 1, 1, 1)
	r.flush()

	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("consent none produced %d request(s): %v", len(reqs), pathsOf(reqs))
	}
}

func TestUsageLane_PerClassOptOutIsHonouredOnBothLanes(t *testing.T) {
	// Only usage_counts is opted in: tool calls and errors must not leave.
	t.Run("full", func(t *testing.T) {
		r := newActiveRig(t, ConsentFull, identityA, optIns("harness.usage_counts"))
		ctx := context.Background()
		r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", "openai")
		if r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true) {
			t.Error("tool_invoked accepted while harness.tool_calls is opted out")
		}
		if r.emitter.Error(ctx, ErrorCategoryAuth, false) {
			t.Error("harness.error accepted while harness.errors is opted out")
		}
		r.flush()
		for _, e := range decodeLogs(t, r.fleet.byPath("/otlp/v1/logs")) {
			if e.kind != "harness.conversation_started" {
				t.Errorf("opted-out kind %s left the process", e.kind)
			}
		}
	})
	t.Run("aggregate", func(t *testing.T) {
		r := newActiveRig(t, ConsentAggregate, identityA, optIns("harness.usage_counts"))
		ctx := context.Background()
		r.emitter.ConversationStarted(ctx, "x", "openai")
		r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
		r.emitter.Error(ctx, ErrorCategoryAuth, false)
		r.flush()
		got := sumByName(decodeMetrics(t, r.fleet.byPath("/otlp/v1/metrics")))
		if got["harness.tool.invocations"] != 0 || got["harness.errors"] != 0 {
			t.Errorf("opted-out counters left the process: %v", got)
		}
		if got["harness.conversations.started"] != 1 {
			t.Errorf("opted-in counter missing: %v", got)
		}
	})
}

func TestUsageLane_NoOptInSnapshotSendsNothing(t *testing.T) {
	for _, level := range []ConsentLevel{ConsentAggregate, ConsentFull} {
		t.Run(string(level), func(t *testing.T) {
			r := newActiveRig(t, level, identityA, nil)
			ctx := context.Background()
			r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", "openai")
			r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)
			r.flush()
			if reqs := r.fleet.snapshot(); len(reqs) != 0 {
				t.Fatalf("an absent opt-in snapshot must admit nothing; saw %v", pathsOf(reqs))
			}
		})
	}
}

func TestUsageLane_SensitiveStringsNeverLeave(t *testing.T) {
	const (
		secretTool     = "mcp__acme-payroll-prod__dump_salaries"
		secretProvider = "acme-internal-llm-gateway"
		secretCategory = "open /Users/alice/work/secret-project/main.go: permission denied sk-ant-api03-LEAKED"
	)
	for _, level := range []ConsentLevel{ConsentAggregate, ConsentFull} {
		t.Run(string(level), func(t *testing.T) {
			r := newActiveRig(t, level, identityA, optIns(usageClasses...))
			ctx := context.Background()
			r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", secretProvider)
			r.emitter.ToolInvoked(ctx, secretTool, time.Second, true)
			r.emitter.Error(ctx, ErrorCategory(secretCategory), false)
			r.flush()

			wire := r.fleet.allBytes()
			for _, planted := range []string{
				secretTool, "acme-payroll", "dump_salaries",
				secretProvider,
				"/Users/alice", "secret-project", "sk-ant-api03", "permission denied",
			} {
				if bytes.Contains(wire, []byte(planted)) {
					t.Errorf("planted string %q reached the wire under %s consent", planted, level)
				}
			}
			if level == ConsentFull {
				events := decodeLogs(t, r.fleet.byPath("/otlp/v1/logs"))
				for _, e := range events {
					switch e.kind {
					case "harness.tool_invoked":
						if e.body["tool_name"] != ExternalToolName {
							t.Errorf("tool_name = %v, want %s", e.body["tool_name"], ExternalToolName)
						}
					case "harness.conversation_started":
						if e.body["model_provider"] != OtherModelProvider {
							t.Errorf("model_provider = %v, want %s", e.body["model_provider"], OtherModelProvider)
						}
					case "harness.error":
						if e.body["category"] != string(ErrorCategoryUnknown) {
							t.Errorf("category = %v, want unknown", e.body["category"])
						}
					}
				}
			}
		})
	}
}

func TestUsageLane_DeactivateDiscardsQueuedDataAndStopsExport(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	ctx := context.Background()

	// Queued, never flushed.
	r.emitter.ConversationStarted(ctx, "11111111-2222-4333-8444-555555555555", "openai")
	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true)

	r.pipeline.Deactivate(ctx) // sign-out

	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("Deactivate flushed %d request(s) %v; sign-out must discard, not send", len(reqs), pathsOf(reqs))
	}
	if r.pipeline.Active() {
		t.Error("pipeline still active after Deactivate")
	}
	if r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true) {
		t.Error("an event was accepted after Deactivate")
	}
	r.flush()
	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("export continued after Deactivate: %v", pathsOf(reqs))
	}
	if st := r.pipeline.Status(); st.EventsDroppedInactive == 0 {
		t.Error("status does not account for the event dropped while inactive")
	}
}

func TestUsageLane_AccountChange_NeverSendsUnderTheOtherAccountsToken(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	ctx := context.Background()

	r.emitter.ToolInvoked(ctx, "kenaz__bash", time.Second, true) // queued as Alice

	// The host account changes underneath us before anything re-activates.
	r.tokens.set(fakeJWT(identityB.UserID))
	r.flush()

	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("Alice's batch was POSTed with Bob's token (%d request(s)); the bound bearer must refuse", len(reqs))
	}
	if st := r.pipeline.Status(); st.ExportsIdentityMism == 0 {
		t.Error("identity-mismatch refusal is not reflected in Status")
	}

	// Supervisor catches up: re-activate as Bob. Alice's queue is discarded.
	if err := r.pipeline.Activate(ctx, r.fleet.base(), nil, identityB, r.tokens.provider(), nil); err != nil {
		t.Fatalf("re-Activate: %v", err)
	}
	r.emitter.ToolInvoked(ctx, "kenaz__read_file", time.Second, true) // as Bob
	r.flush()

	events := decodeLogs(t, r.fleet.byPath("/otlp/v1/logs"))
	if len(events) != 1 {
		t.Fatalf("got %d event(s) after the account change, want exactly Bob's one", len(events))
	}
	if events[0].resource["kameas.user.id"] != identityB.UserID || events[0].resource["kameas.org.id"] != identityB.OrgID {
		t.Errorf("post-change event attributed to %v, want Bob", events[0].resource)
	}
	if events[0].body["tool_name"] != "kenaz__read_file" {
		t.Errorf("Alice's queued event survived the account change: %v", events[0].body)
	}
	for _, req := range r.fleet.snapshot() {
		if req.bearer != fakeJWT(identityB.UserID) {
			t.Errorf("a request carried a non-Bob bearer after the change")
		}
	}
}

func TestUsageLane_SignedOutTokenSourceSendsNothing(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	r.emitter.ToolInvoked(context.Background(), "kenaz__bash", time.Second, true)
	r.tokens.set("") // broker session cleared its token
	r.flush()
	if reqs := r.fleet.snapshot(); len(reqs) != 0 {
		t.Fatalf("export attempted without a bearer: %v", pathsOf(reqs))
	}
}

func TestUsageLane_RetriesTransientFailureThenDelivers(t *testing.T) {
	r := newActiveRig(t, ConsentFull, identityA, optIns(usageClasses...))
	r.fleet.mu.Lock()
	r.fleet.statusFor = func(path string, n int) int {
		if n == 0 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}
	r.fleet.mu.Unlock()

	r.emitter.ToolInvoked(context.Background(), "kenaz__bash", time.Second, true)
	r.flush()

	reqs := r.fleet.byPath("/otlp/v1/logs")
	if len(reqs) < 2 {
		t.Fatalf("got %d request(s); a 503 must be retried", len(reqs))
	}
	// The retry carries the same record — delivered once it is accepted.
	last := decodeLogs(t, reqs[len(reqs)-1:])
	if len(last) != 1 || last[0].kind != "harness.tool_invoked" {
		t.Errorf("retried request did not carry the event: %+v", last)
	}
	st := r.pipeline.Status()
	if st.ExportsFailed == 0 || st.ExportsOK == 0 {
		t.Errorf("status = %+v, want both a failed and an ok export recorded", st)
	}
}

func TestUsageLane_UnauthorizedExportNotifiesAuthLayer(t *testing.T) {
	f := newFakeFleet(t)
	f.statusFor = func(string, int) int { return http.StatusUnauthorized }

	p := NewFleetOTLPPipeline(nil)
	p.SetExportCadence(time.Hour, time.Hour)
	p.SetTelemetryOptIns(optIns(usageClasses...))
	p.SetLogLaneEnabled(true)
	notified := make(chan struct{}, 4)
	p.SetOnUnauthorized(func() {
		select {
		case notified <- struct{}{}:
		default:
		}
	})
	tokens := &tokenBox{tok: fakeJWT(identityA.UserID)}
	if err := p.Activate(context.Background(), f.base(), nil, identityA, tokens.provider(), nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	defer p.Deactivate(context.Background())

	e := NewUsageEmitter(p, &staticConsent{level: ConsentFull})
	e.ToolInvoked(context.Background(), "kenaz__bash", time.Second, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Flush(ctx)

	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		t.Fatal("a 401 from Fleet did not reach the OnUnauthorized callback (broker renewal would never be nudged)")
	}
	if st := p.Status(); st.ExportsUnauthorized == 0 || st.LastExportCode != http.StatusUnauthorized {
		t.Errorf("status = %+v, want the 401 recorded", st)
	}
}

func TestUsageLane_InactivePipelineAcceptsNothing(t *testing.T) {
	p := NewFleetOTLPPipeline(nil)
	p.SetTelemetryOptIns(optIns(usageClasses...))
	p.SetLogLaneEnabled(true)
	e := NewUsageEmitter(p, &staticConsent{level: ConsentFull})
	if e.ConversationStarted(context.Background(), "11111111-2222-4333-8444-555555555555", "openai") {
		t.Error("an un-activated pipeline accepted an event; there is no identity to attribute it to")
	}
}

func TestProjectToolName(t *testing.T) {
	cases := map[string]string{
		"kenaz__bash":                       "kenaz__bash",
		"kenaz__read_file":                  "kenaz__read_file",
		"mcp__github__create_issue":         ExternalToolName,
		"acme-internal-tool":                ExternalToolName,
		"":                                  ExternalToolName,
		"kenaz__" + strings.Repeat("a", 80): ExternalToolName,
		"kenaz__has space":                  ExternalToolName,
		"kenaz__/etc/passwd":                ExternalToolName,
	}
	for in, want := range cases {
		if got := ProjectToolName(in); got != want {
			t.Errorf("ProjectToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── small helpers ────────────────────────────────────────────────────────────

func pathsOf(reqs []capturedRequest) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.path)
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func assertBodyKeys(t *testing.T, e decodedEvent, want ...string) {
	t.Helper()
	if e.body == nil {
		return // absence already reported by the caller
	}
	if len(e.body) != len(want) {
		t.Errorf("%s body keys = %v, want exactly %v", e.kind, keysOf(e.body), want)
		return
	}
	for _, k := range want {
		if _, ok := e.body[k]; !ok {
			t.Errorf("%s body is missing %q (has %v)", e.kind, k, keysOf(e.body))
		}
	}
}

// TestSpanLane_PostsToTheFleetOTLPRoute pins the span exporter's request path.
//
// Activate used to build the exporter with WithEndpointURL(<api>/otlp) AND
// WithURLPath("/v1/traces"). WithURLPath REPLACES the URL's path, so spans
// went to <api>/v1/traces — a route Fleet does not serve — and never landed.
func TestSpanLane_PostsToTheFleetOTLPRoute(t *testing.T) {
	f := newFakeFleet(t)
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	p := NewFleetOTLPPipeline(nil)
	tokens := &tokenBox{tok: fakeJWT(identityA.UserID)}
	if err := p.Activate(context.Background(), f.base(), nil, identityA, tokens.provider(), tp); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	_, span := tp.Tracer("t").Start(context.Background(), "harness.task.create")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = tp.ForceFlush(ctx)

	if got := f.byPath("/otlp/v1/traces"); len(got) == 0 {
		t.Fatalf("no span export reached /otlp/v1/traces; paths seen: %v", pathsOf(f.snapshot()))
	}

	// And sign-out stops it: a span ended after Deactivate must not leave.
	before := len(f.snapshot())
	p.Deactivate(ctx)
	_, span = tp.Tracer("t").Start(context.Background(), "harness.task.create")
	span.End()
	_ = tp.ForceFlush(ctx)
	if after := len(f.snapshot()); after != before {
		t.Errorf("span export continued after Deactivate (%d → %d requests)", before, after)
	}
}
