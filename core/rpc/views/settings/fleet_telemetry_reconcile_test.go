package settings

// fleet_telemetry_reconcile_test.go — enroll → reconcile → real lifecycle →
// wire bytes.
//
// The unit tests under core/fleet prove each lane in isolation. These prove
// the thing that was actually broken in the product: that a signed-in,
// consented harness ends up EXPORTING, as the right account, and stops when
// it should. Everything between the RPC surface and the socket is real — the
// fleet client, /config.json discovery, POST /api/v1/enroll, the opt-in GET,
// ReconcileTelemetry, the ConversationTracker, the OTLP exporters. Only Fleet
// is fake, and it decodes protobuf the way the receiver does.

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

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const resourceOwnerClaim = "urn:zitadel:iam:user:resourceowner:id"

func jwtFor(sub, zitadelOrg string) string { return jwtForIss(sub, zitadelOrg, "https://issuer.test") }

func jwtForIss(sub, zitadelOrg, iss string) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	claims := map[string]string{"sub": sub, "iss": iss}
	if zitadelOrg != "" {
		claims[resourceOwnerClaim] = zitadelOrg
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) + ".sig"
}

type otlpCapture struct {
	path   string
	bearer string
	raw    []byte
}

type reconcileFleet struct {
	srv *httptest.Server

	mu          sync.Mutex
	captures    []otlpCapture
	enrollCalls int
	enrollFails int // answer this many enrolls with 503 first
	tier        string
	optIns      []fleet.TelemetryOptInItem
	// optInsStatus, when non-zero, is the status the opt-ins GET answers with.
	optInsStatus int
}

func newReconcileFleet(t *testing.T) *reconcileFleet {
	t.Helper()
	f := &reconcileFleet{tier: "enterprise"}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("/api/v1/enroll", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.enrollCalls++
		fail := f.enrollFails > 0
		if fail {
			f.enrollFails--
		}
		tier := f.tier
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"code":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// org_id is Fleet's INTERNAL uuid — deliberately unlike the token's
		// Zitadel resource-owner id, as in production.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"org_id": "f7ab6cc5-d86f-45ed-8b46-c0967a000000", "team_id": "t1",
			"org_name": "Kameas Dogfood", "team_name": "Everyone", "role": "org_owner",
			"user_id": "0e0e0e0e-fleet-internal-user-uuid", "tier": tier,
		})
	})
	mux.HandleFunc("/api/v1/me/telemetry-opt-ins", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		items := append([]fleet.TelemetryOptInItem(nil), f.optIns...)
		status := f.optInsStatus
		f.mu.Unlock()
		if status != 0 {
			http.Error(w, `{"code":"unavailable"}`, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"opt_ins": items})
	})
	mux.HandleFunc("/otlp/", func(w http.ResponseWriter, r *http.Request) {
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
		f.captures = append(f.captures, otlpCapture{
			path:   r.URL.Path,
			bearer: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			raw:    raw,
		})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":1}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *reconcileFleet) setOptIns(items []fleet.TelemetryOptInItem) {
	f.mu.Lock()
	f.optIns = items
	f.mu.Unlock()
}

func (f *reconcileFleet) snapshot() []otlpCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]otlpCapture(nil), f.captures...)
}

func (f *reconcileFleet) count() int { return len(f.snapshot()) }

type wireEvent struct {
	kind     string
	resource map[string]string
	bearer   string
}

func (f *reconcileFleet) events(t *testing.T) []wireEvent {
	t.Helper()
	var out []wireEvent
	for _, c := range f.snapshot() {
		if c.path != "/otlp/v1/logs" {
			continue
		}
		var msg collogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(c.raw, &msg); err != nil {
			t.Fatalf("decode logs: %v", err)
		}
		for _, rl := range msg.GetResourceLogs() {
			res := map[string]string{}
			for _, kv := range rl.GetResource().GetAttributes() {
				res[kv.GetKey()] = kv.GetValue().GetStringValue()
			}
			for _, sl := range rl.GetScopeLogs() {
				for _, lr := range sl.GetLogRecords() {
					kind := ""
					for _, kv := range lr.GetAttributes() {
						if kv.GetKey() == fleet.AttrEventKind {
							kind = kv.GetValue().GetStringValue()
						}
					}
					out = append(out, wireEvent{kind: kind, resource: res, bearer: c.bearer})
				}
			}
		}
	}
	return out
}

func (f *reconcileFleet) counterSums(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range f.snapshot() {
		if c.path != "/otlp/v1/metrics" {
			continue
		}
		var msg colmetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(c.raw, &msg); err != nil {
			t.Fatalf("decode metrics: %v", err)
		}
		for _, rm := range msg.GetResourceMetrics() {
			for _, sm := range rm.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					for _, dp := range m.GetSum().GetDataPoints() {
						out[m.GetName()] += dp.GetAsInt()
					}
				}
			}
		}
	}
	return out
}

// reconcileRig is a settings.API wired the way rpc.New wires it, against a
// fake Fleet, with a swappable external token source (the workbench shape).
type reconcileRig struct {
	api      *API
	fleet    *reconcileFleet
	pipeline *fleet.FleetOTLPPipeline
	consent  *fleet.TelemetryConsent

	tokMu sync.Mutex
	tok   string
}

func (r *reconcileRig) setToken(tok string) { r.tokMu.Lock(); r.tok = tok; r.tokMu.Unlock() }
func (r *reconcileRig) token() string       { r.tokMu.Lock(); defer r.tokMu.Unlock(); return r.tok }

func (r *reconcileRig) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r.pipeline.Flush(ctx)
}

func newReconcileRig(t *testing.T, level fleet.ConsentLevel) *reconcileRig {
	t.Helper() // deliberately NOT t.Parallel: SetExternalTokenSource is process-global
	f := newReconcileFleet(t)
	f.setOptIns(fleet.TierOptInUpdates(level)) // what the tier push would have written

	r := &reconcileRig{fleet: f, tok: jwtFor("sub-alice", "zitadel-org-111")}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })

	dataDir := t.TempDir()
	r.api = &API{}
	r.api.SetFleetClient(fleet.NewClientForTesting(f.srv.URL), dataDir)
	t.Cleanup(r.api.StopFleetBackground)

	consent, err := fleet.NewTelemetryConsent(dataDir, fleet.TierReaderFunc(r.api.FleetOrgTier))
	if err != nil {
		t.Fatalf("NewTelemetryConsent: %v", err)
	}
	r.consent = consent

	r.pipeline = fleet.NewFleetOTLPPipeline(nil)
	r.pipeline.SetExportCadence(time.Hour, time.Hour) // tests flush explicitly
	r.api.SetFleetOTLPPipeline(r.pipeline, nil, nil, consent)

	if level != fleet.ConsentNone {
		// SetLevel tier-checks against FleetOrgTier, which has no tier until
		// an enroll has happened — so enroll first, exactly as a user must
		// be signed in before the panel lets them pick a paid tier.
		if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
			t.Fatalf("enroll: %v", err)
		}
		if err := consent.SetLevel(level); err != nil {
			t.Fatalf("SetLevel(%s): %v", level, err)
		}
		r.api.ReconcileTelemetry(context.Background())
	}
	return r
}

// work simulates a little everyday activity through the production tracker.
func (r *reconcileRig) work(t *testing.T, sessionID string) {
	t.Helper()
	tr := r.api.FleetUsageTracker()
	if tr == nil {
		t.Fatal("no usage tracker wired")
	}
	ctx := context.Background()
	tr.TurnStarted(ctx, sessionID, "anthropic")
	tr.LLMResponse(ctx, sessionID, 1200, 300, 0.02)
	tr.ToolInvoked(ctx, sessionID, "kenaz__bash", 40*time.Millisecond, true)
	tr.ToolInvoked(ctx, sessionID, "mcp__acme-payroll__export", 90*time.Millisecond, false)
}

func TestReconcile_EnrolledAndConsented_ExportsAsTheTokenIdentity(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	if !r.pipeline.Active() {
		t.Fatal("enrolled + full consent, but the pipeline is not active")
	}
	r.work(t, "local-session-1")
	r.flush()

	events := r.fleet.events(t)
	if len(events) == 0 {
		t.Fatal("no events reached Fleet")
	}
	for _, e := range events {
		if e.resource["kameas.user.id"] != "sub-alice" {
			t.Errorf("kameas.user.id = %q, want the JWT sub", e.resource["kameas.user.id"])
		}
		// The receiver compares this to the token's resource-owner claim and
		// 401s the batch otherwise. The enroll response's org_id is Fleet's
		// internal UUID and must NOT be what is sent.
		if e.resource["kameas.org.id"] != "zitadel-org-111" {
			t.Errorf("kameas.org.id = %q, want the token's Zitadel resource-owner id", e.resource["kameas.org.id"])
		}
		if e.resource["kameas.machine.id"] == "" {
			t.Error("kameas.machine.id is empty; Fleet requires it")
		}
	}
	wire := bytes.Join(func() [][]byte {
		var bs [][]byte
		for _, c := range r.fleet.snapshot() {
			bs = append(bs, c.raw)
		}
		return bs
	}(), nil)
	for _, planted := range []string{"local-session-1", "acme-payroll", "f7ab6cc5", "fleet-internal-user"} {
		if bytes.Contains(wire, []byte(planted)) {
			t.Errorf("%q reached the wire", planted)
		}
	}
}

func TestReconcile_AggregateYieldsCountsAndNoLogRecords(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentAggregate)
	r.work(t, "s1")
	r.api.FlushFleetTelemetryForShutdown() // ends the segment, flushes both lanes

	if n := len(r.fleet.events(t)); n != 0 {
		t.Fatalf("aggregate sent %d log record(s)", n)
	}
	got := r.fleet.counterSums(t)
	for name, want := range map[string]int64{
		"harness.conversations.started": 1,
		"harness.conversations.ended":   1,
		"harness.tool.invocations":      2,
		"harness.tokens.input":          1200,
		"harness.tokens.output":         300,
	} {
		if got[name] != want {
			t.Errorf("%s = %d, want %d (all: %v)", name, got[name], want, got)
		}
	}
}

func TestReconcile_DefaultConsentNone_NeverActivates(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentNone)
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if r.pipeline.Active() {
		t.Fatal("pipeline activated with consent none")
	}
	r.work(t, "s1")
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("consent none produced %d OTLP request(s)", n)
	}
}

func TestReconcile_OptingInAfterSignInActivatesWithoutReEnroll(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentNone)
	ctx := context.Background()
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	enrollsBefore := r.fleet.enrollCalls

	// The user opens Settings and picks Full. (fleetview.Impl does exactly
	// this pair: SetLevel, push the vector, then OnConsentChanged.)
	if err := r.consent.SetLevel(fleet.ConsentFull); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	r.api.AdoptTelemetryOptIns(fleet.TierOptInUpdates(fleet.ConsentFull))
	r.api.ReconcileTelemetry(ctx)

	if !r.pipeline.Active() {
		t.Fatal("opting in did not activate export; the user would have to restart the app")
	}
	if r.fleet.enrollCalls != enrollsBefore {
		t.Errorf("reconcile re-enrolled (%d → %d); it must reuse the enrolled identity", enrollsBefore, r.fleet.enrollCalls)
	}
	r.work(t, "s1")
	r.flush()
	if len(r.fleet.events(t)) == 0 {
		t.Error("no events after opting in")
	}
}

func TestReconcile_OptingOutStopsExportAndDiscardsQueue(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	r.work(t, "s1") // queued, not flushed

	if err := r.consent.SetLevel(fleet.ConsentNone); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	r.api.ReconcileTelemetry(context.Background())

	if r.pipeline.Active() {
		t.Fatal("pipeline still active after opting out")
	}
	r.work(t, "s2")
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) after opting out — the queue must be discarded, not flushed", n)
	}
	if open := r.api.FleetUsageTracker().OpenSegments(); open != 1 {
		// s2's segment is tracked locally (unreported); s1's was dropped.
		t.Errorf("open segments = %d, want only the post-opt-out local one", open)
	}
}

func TestReconcile_TierDowngradeFailsClosed(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	ctx := context.Background()

	// The org is downgraded; the next enroll carries the new tier.
	r.fleet.mu.Lock()
	r.fleet.tier = "free"
	r.fleet.mu.Unlock()
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("re-enroll: %v", err)
	}

	if r.pipeline.Active() {
		t.Fatal("stored consent is full, but the tier no longer allows it — export must be off")
	}
	r.work(t, "s1")
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) after a tier downgrade", n)
	}
}

func TestReconcile_SignOutStopsExport(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	r.work(t, "s1")

	r.api.StopFleetBackground() // what FleetSignOut runs

	if r.pipeline.Active() {
		t.Fatal("pipeline still active after sign-out")
	}
	r.work(t, "s2")
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) after sign-out", n)
	}
	st, _ := r.api.FleetTelemetryStatus(context.Background())
	if st.Enrolled || st.Pipeline.Active {
		t.Errorf("status after sign-out = %+v, want not enrolled + inactive", st)
	}
}

func TestReconcile_BrokerSessionEnds_Deactivates(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	r.work(t, "s1")

	r.setToken("") // host signed out; the broker session cleared its token
	r.api.ReconcileTelemetry(context.Background())

	if r.pipeline.Active() {
		t.Fatal("pipeline still active with no token")
	}
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) after the session ended", n)
	}
}

func TestReconcile_AccountChange_ReattributesAndDropsThePreviousAccountsWork(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	ctx := context.Background()
	r.work(t, "s1") // Alice's, queued

	// Host signs out and back in as Bob. The supervisor re-enrolls, which
	// reconciles.
	r.setToken(jwtFor("sub-bob", "zitadel-org-222"))
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("re-enroll as Bob: %v", err)
	}
	if got := r.pipeline.ActiveIdentity(); got.UserID != "sub-bob" || got.OrgID != "zitadel-org-222" {
		t.Fatalf("active identity = %+v, want Bob", got)
	}

	r.work(t, "s1") // same local session, now Bob's
	r.flush()

	events := r.fleet.events(t)
	if len(events) == 0 {
		t.Fatal("nothing exported as Bob")
	}
	var started int
	for _, e := range events {
		if e.resource["kameas.user.id"] != "sub-bob" || e.resource["kameas.org.id"] != "zitadel-org-222" {
			t.Errorf("event %s attributed to %v, want Bob", e.kind, e.resource)
		}
		if e.bearer != jwtFor("sub-bob", "zitadel-org-222") {
			t.Errorf("event %s sent under a non-Bob token", e.kind)
		}
		if e.kind == "harness.conversation_started" {
			started++
		}
	}
	// Alice's open segment was dropped, so Bob's first turn opens a NEW
	// conversation rather than continuing (and later "ending") Alice's.
	if started != 1 {
		t.Errorf("conversation_started = %d as Bob, want 1", started)
	}
}

func TestReconcile_TokenWithoutResourceOwnerClaim_DoesNotActivate(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentNone)
	ctx := context.Background()
	r.setToken(jwtFor("sub-alice", "")) // no org claim: Fleet would 401 every batch
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if err := r.consent.SetLevel(fleet.ConsentFull); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}
	r.api.AdoptTelemetryOptIns(fleet.TierOptInUpdates(fleet.ConsentFull))
	r.api.ReconcileTelemetry(ctx)
	if r.pipeline.Active() {
		t.Fatal("activated without a resource-owner claim; every export would be refused")
	}
}

func TestReconcile_ServedBootRace_TierComesFromEnrollWhenThePollerHasNone(t *testing.T) {
	// In a workbench the capability poller's first refresh runs before the
	// broker token source exists, fails, and backs off. At enroll time it
	// still has no tier. Consent must clamp against the ENROLLED tier rather
	// than "free", or export is skipped for the whole session.
	r := newReconcileRig(t, fleet.ConsentNone)
	if got := r.api.FleetOrgTier(); got != "free" {
		t.Fatalf("pre-enroll tier = %q, want free", got)
	}
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if got := r.api.FleetOrgTier(); got != "enterprise" {
		t.Fatalf("post-enroll tier = %q, want the enrolled tier while the poller has none", got)
	}
	if err := r.consent.SetLevel(fleet.ConsentFull); err != nil {
		t.Fatalf("full consent refused on an enterprise org: %v", err)
	}
}

func TestReconcile_SameSubjectDifferentOrgOrIssuer_Reattributes(t *testing.T) {
	for name, tok := range map[string]string{
		"org":    jwtFor("sub-alice", "zitadel-org-999"),
		"issuer": jwtForIss("sub-alice", "zitadel-org-111", "https://other-realm.test"),
	} {
		t.Run(name, func(t *testing.T) {
			r := newReconcileRig(t, fleet.ConsentFull)
			before := r.pipeline.ActiveIdentity()
			r.work(t, "s1") // queued under the first identity

			r.setToken(tok)
			r.flush() // before reconcile: the bound bearer must refuse
			if n := r.fleet.count(); n != 0 {
				t.Fatalf("%d request(s) sent under a token for a different %s", n, name)
			}

			r.api.ReconcileTelemetry(context.Background())
			after := r.pipeline.ActiveIdentity()
			if after == before {
				t.Fatalf("identity not re-stamped after the %s changed: %+v", name, after)
			}
			r.work(t, "s1")
			r.flush()
			for _, e := range r.fleet.events(t) {
				if e.bearer != tok || e.resource["kameas.org.id"] != after.OrgID {
					t.Errorf("event %s: resource org %q / bearer mismatch after %s change", e.kind, e.resource["kameas.org.id"], name)
				}
			}
			if len(r.fleet.events(t)) == 0 {
				t.Error("nothing exported under the new identity")
			}
		})
	}
}

// A preference changed in Fleet's web UI must reach a RUNNING harness.
func TestRefreshPreferences_FleetWebToggleReachesARunningSession(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	ctx := context.Background()
	toolEvents := func() int {
		n := 0
		for _, e := range r.fleet.events(t) {
			if e.kind == "harness.tool_invoked" {
				n++
			}
		}
		return n
	}

	// Revoke tool_calls in Fleet while the session stays signed in.
	var narrowed []fleet.TelemetryOptInItem
	for _, item := range fleet.TierOptInUpdates(fleet.ConsentFull) {
		if item.Class == "harness.tool_calls" {
			item.OptedIn = false
		}
		narrowed = append(narrowed, item)
	}
	r.fleet.setOptIns(narrowed)
	r.api.RefreshTelemetryPreferences(ctx) // the supervisor's slow tick

	r.work(t, "s1")
	r.flush()
	if n := toolEvents(); n != 0 {
		t.Fatalf("%d tool_invoked event(s) after the class was revoked in Fleet", n)
	}
	st, _ := r.api.FleetTelemetryStatus(ctx)
	for _, c := range st.OptedInClasses {
		if c == "harness.tool_calls" {
			t.Errorf("status still lists the revoked class: %v", st.OptedInClasses)
		}
	}
	if st.PreferencesFetchedAt == "" {
		t.Error("status does not say when preferences were last confirmed")
	}

	// Re-enable in Fleet: takes effect on the next tick, no restart.
	r.fleet.setOptIns(fleet.TierOptInUpdates(fleet.ConsentFull))
	r.api.RefreshTelemetryPreferences(ctx)
	r.work(t, "s1")
	r.flush()
	if toolEvents() == 0 {
		t.Error("re-enabling the class in Fleet did not resume tool events")
	}
}

// Fleet unreachable: keep the last confirmed snapshot for a bounded time, then
// fail closed. Never broaden.
func TestRefreshPreferences_FetchFailure_BoundedCacheThenFailClosed(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	ctx := context.Background()
	r.fleet.mu.Lock()
	r.fleet.optInsStatus = http.StatusServiceUnavailable
	r.fleet.mu.Unlock()

	r.api.RefreshTelemetryPreferences(ctx)
	st, _ := r.api.FleetTelemetryStatus(ctx)
	if len(st.OptedInClasses) == 0 {
		t.Fatal("a single failed fetch dropped a fresh snapshot")
	}

	// Age the snapshot past the bound.
	r.api.fleet.mu.Lock()
	r.api.fleet.optInsFetchedAt = time.Now().Add(-TelemetryPreferencesMaxAge - time.Minute)
	r.api.fleet.mu.Unlock()
	r.api.RefreshTelemetryPreferences(ctx)

	st, _ = r.api.FleetTelemetryStatus(ctx)
	if len(st.OptedInClasses) != 0 {
		t.Fatalf("stale snapshot still admits %v", st.OptedInClasses)
	}
	r.work(t, "s1")
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) on a stale, unconfirmable preference snapshot", n)
	}
}

// Review follow-up 2, settings half: ending the session leaves nothing of the
// old account behind even when the next enroll fails.
func TestSessionEnded_ThenFailedEnroll_LeavesNothingOfTheOldAccount(t *testing.T) {
	r := newReconcileRig(t, fleet.ConsentFull)
	ctx := context.Background()
	r.work(t, "s1") // alice, queued

	r.api.FleetSessionEnded(ctx) // supervisor: identity changed
	r.setToken(jwtFor("sub-bob", "zitadel-org-222"))
	r.fleet.mu.Lock()
	r.fleet.enrollFails = 1
	r.fleet.mu.Unlock()
	if _, err := r.api.FleetRefreshIdentity(ctx); err == nil {
		t.Fatal("fixture: bob's enroll should have failed")
	}

	st, _ := r.api.FleetTelemetryStatus(ctx)
	if st.Enrolled || st.Pipeline.Active || len(st.OptedInClasses) != 0 {
		t.Fatalf("after a failed switch: %+v — alice's state must be gone", st)
	}
	r.flush()
	if n := r.fleet.count(); n != 0 {
		t.Fatalf("%d OTLP request(s) after the failed switch", n)
	}

	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := r.pipeline.ActiveIdentity(); got.UserID != "sub-bob" {
		t.Fatalf("active identity = %+v, want only bob", got)
	}
	r.work(t, "s1")
	r.flush()
	for _, e := range r.fleet.events(t) {
		if e.resource["kameas.user.id"] != "sub-bob" {
			t.Errorf("event attributed to %q", e.resource["kameas.user.id"])
		}
	}
}
