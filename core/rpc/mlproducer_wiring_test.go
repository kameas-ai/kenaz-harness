package rpc

// mlproducer_wiring_test.go — ml-producer-01MLPRD01 WP03: the producer's
// composition against a fake Fleet. Everything between the recorder and
// the socket is real — settings.API's fleet session, fleet.Client
// (/config.json discovery, POST /api/v1/enroll, GET /api/v1/me/capabilities
// through the capability poller, GET /api/v1/me/ml, POST /otlp/v1/logs),
// the consent-source and poster adapters, the gate, the shipper and the
// outbox over real sqlite (core.New on a temp DataDir). Only Fleet is fake,
// and it decodes protobuf the way its receiver does.
//
// NOT t.Parallel: fleet.SetExternalTokenSource is process-global.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// The dev org's Zitadel resource-owner id (dogfood 2026-10-04 notes):
// what the token carries and kameas.org.id must equal.
const mlTestZitadelOrg = "390451413051827052"

const mlOtherOrg = "f7ab6cc5-d86f-45ed-8b46-c0967a000000"

type otlpReply struct {
	status     int
	body       string
	retryAfter string
}

// mlFleet is a fake Fleet; written from handler goroutines and read from
// the test body: mutex + snapshot accessors.
type mlFleet struct {
	srv *httptest.Server

	mu           sync.Mutex
	enrollOrg    string
	hosted       bool
	paused       bool
	meML         string
	meMLStatus   int
	otlp         func(n int, req *collogs.ExportLogsServiceRequest) otlpReply
	posts        []*collogs.ExportLogsServiceRequest
	contentTypes []string
	enrolls      int
	mlGets       int
}

func mlEffectiveBody(offload bool, policy string, optedIn, acked bool) string {
	ships := offload && (policy == "on" || (policy == "member_choice" && optedIn))
	m := map[string]any{
		"org_offload_enabled": offload, "org_policy": policy, "user_workflow_events_opted_in": optedIn,
		"notice_ack_required": ships && !acked, "effective": ships && acked, "notice_version": 2,
		"notice_acked_at": nil, "retention_days": 90, "retain_on_withdrawal": false,
	}
	if acked {
		m["notice_acked_at"] = "2026-10-09T12:00:00Z"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func newMLFleet(t *testing.T, enrollOrg string) *mlFleet {
	t.Helper()
	f := &mlFleet{enrollOrg: enrollOrg, hosted: true, meMLStatus: 200, meML: mlEffectiveBody(true, "on", false, true)}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("/api/v1/enroll", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.enrolls++
		org := f.enrollOrg
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"org_id": org, "team_id": "t1", "org_name": "Kameas Dev", "role": "org_owner",
			"user_id": "0e0e0e0e-fleet-internal-user", "tier": "team",
		})
	})
	mux.HandleFunc("/api/v1/me/telemetry-opt-ins", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"opt_ins":[]}`)
	})
	mux.HandleFunc("/api/v1/me/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		hosted, paused := f.hosted, f.paused
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier": "team", "capabilities": map[string]bool{string(corefleet.CapHostedInference): hosted},
			"fetched_at": time.Now().UTC(), "paused": paused,
		})
	})
	mux.HandleFunc("/api/v1/me/ml", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.mlGets++
		st, body := f.meMLStatus, f.meML
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/otlp/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req collogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(raw, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.posts = append(f.posts, &req)
		f.contentTypes = append(f.contentTypes, r.Header.Get("Content-Type"))
		n, fn := len(f.posts), f.otlp
		f.mu.Unlock()
		rep := otlpReply{status: 200}
		if fn != nil {
			rep = fn(n, &req)
		}
		if rep.status == 200 && rep.body == "" {
			c := otlpRecordCount(&req)
			rep.body = fmt.Sprintf(`{"accepted":%d,"ml":{"accepted":%d,"duplicates":0,"rejected":0,"rejections":{}}}`, c, c)
		}
		if rep.retryAfter != "" {
			w.Header().Set("Retry-After", rep.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *mlFleet) set(fn func(*mlFleet)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *mlFleet) snapshot() (posts []*collogs.ExportLogsServiceRequest, cts []string, enrolls, mlGets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*collogs.ExportLogsServiceRequest(nil), f.posts...), append([]string(nil), f.contentTypes...), f.enrolls, f.mlGets
}

func otlpRecordCount(req *collogs.ExportLogsServiceRequest) int {
	n := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			n += len(sl.GetLogRecords())
		}
	}
	return n
}

func mlTestJWT(sub, zitadelOrg string) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return enc(map[string]string{"alg": "none"}) + "." +
		enc(map[string]string{"sub": sub, "iss": "https://issuer.test", "urn:zitadel:iam:user:resourceowner:id": zitadelOrg}) + ".sig"
}

type mlWiringRig struct {
	t       *testing.T
	fleet   *mlFleet
	dataDir string
	c       *core.Core
	api     *settings.API
	w       *mlProducerWiring
}

// newMLWiringRig: a signed-in, enrolled (as enrollOrg), entitled harness
// whose /me/ml is effective, with the real producer wiring over it.
func newMLWiringRig(t *testing.T, enrollOrg string, beforeCore func(dataDir string)) *mlWiringRig {
	t.Helper()
	return newMLWiringRigWithStore(t, enrollOrg, beforeCore, nil)
}

// newMLWiringRigWithStore is newMLWiringRig over a settings API backed by
// store (nil: the zero settings.API, as the WP03 tests use). WP04's
// acceptance turn needs a real store so the fs write dial can be on.
func newMLWiringRigWithStore(t *testing.T, enrollOrg string, beforeCore func(dataDir string), store settings.SettingsStore) *mlWiringRig {
	t.Helper()
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1: the rig needs fleet enabled")
	}
	sandboxUserConfigDir(t)
	f := newMLFleet(t, enrollOrg)
	tok := mlTestJWT("sub-alice", mlTestZitadelOrg)
	corefleet.SetExternalTokenSource(func() string { return tok })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dataDir := t.TempDir()
	if beforeCore != nil {
		beforeCore(dataDir)
	}
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := &settings.API{}
	if store != nil {
		api = settings.NewAPI(store)
	}
	api.SetFleetClient(corefleet.NewClientForTesting(f.srv.URL), dataDir)
	t.Cleanup(api.StopFleetBackground)
	ctx := context.Background()
	if _, err := api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if _, err := api.CapabilityPoller().Refresh(ctx); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	w := newMLProducerWiring(c, api, nil)
	if w == nil {
		t.Fatal("newMLProducerWiring returned nil over a real DataDir")
	}
	t.Cleanup(w.shutdown)
	return &mlWiringRig{t: t, fleet: f, dataDir: dataDir, c: c, api: api, w: w}
}

func (r *mlWiringRig) toolCall(session, tool, args, result string) {
	r.w.rec.ToolCallCompleted(coreag.ToolCallRecord{
		Ctx: context.Background(), SessionID: session, ToolName: tool,
		Outcome: coreag.ToolOutcomeOK, Duration: 10 * time.Millisecond, RawArgs: args, ResultContent: result,
	})
}

func (r *mlWiringRig) outbox() []mlstore.Record {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.w.rec.Flush(ctx); err != nil {
		r.t.Fatalf("Flush: %v", err)
	}
	recs, err := r.w.store.ReadBatch(ctx, 0, 10000)
	if err != nil {
		r.t.Fatal(err)
	}
	return recs
}

func (r *mlWiringRig) shipNow() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.w.shipper.ShipNow(ctx); err != nil {
		r.t.Fatalf("ShipNow: %v", err)
	}
}

// Every row of the contract's effective-rule table, plus the capability /
// pause / read-failure conditions, through the real fleet client.
func TestMLWiring_GateMatrixOverContractEffectiveTable(t *testing.T) {
	r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
	ctx := context.Background()
	rows := []struct {
		name   string
		body   string
		status int
		hosted bool
		paused bool
		open   bool
		reason string
	}{
		{"offload false, policy on", mlEffectiveBody(false, "on", true, true), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload false, member_choice opted in", mlEffectiveBody(false, "member_choice", true, true), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload true, policy off, member opted in", mlEffectiveBody(true, "off", true, true), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload true, member_choice, not opted in", mlEffectiveBody(true, "member_choice", false, true), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload true, member_choice, opted in, notice not acked", mlEffectiveBody(true, "member_choice", true, false), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload true, member_choice, opted in, acked", mlEffectiveBody(true, "member_choice", true, true), 200, true, false, true, ""},
		{"offload true, policy on, notice not acked", mlEffectiveBody(true, "on", false, false), 200, true, false, false, mlproducer.ReasonNotEffective},
		{"offload true, policy on, acked", mlEffectiveBody(true, "on", false, true), 200, true, false, true, ""},
		{"effective but hosted_inference off", mlEffectiveBody(true, "on", false, true), 200, false, false, false, mlproducer.ReasonNotEntitled},
		{"effective but org paused", mlEffectiveBody(true, "on", false, true), 200, true, true, false, mlproducer.ReasonOrgPaused},
		{"/me/ml 503", `{"code":"unavailable"}`, 503, true, false, false, mlproducer.ReasonConsentReadError},
		{"/me/ml contradicts itself", `{"org_offload_enabled":true,"org_policy":"on","user_workflow_events_opted_in":false,"notice_ack_required":false,"effective":true,"notice_version":1,"notice_acked_at":null,"retention_days":90,"retain_on_withdrawal":false}`, 200, true, false, false, mlproducer.ReasonConsentReadError},
	}
	for _, row := range rows {
		r.fleet.set(func(f *mlFleet) {
			f.meML, f.meMLStatus, f.hosted, f.paused = row.body, row.status, row.hosted, row.paused
		})
		if _, err := r.api.CapabilityPoller().Refresh(ctx); err != nil {
			t.Fatalf("%s: capabilities: %v", row.name, err)
		}
		d := r.w.gate.Refresh(ctx)
		if d.Open != row.open || d.Reason != row.reason {
			t.Errorf("%s: decision open=%v reason=%q, want open=%v reason=%q", row.name, d.Open, d.Reason, row.open, row.reason)
		}
	}
}

// The dev-org guard (spec §12 A-11): enrolled into any other org, nothing
// is recorded and nothing ships, with Fleet's consent fully effective.
func TestMLWiring_DevOrgGuard_OtherOrgRecordsAndShipsNothing(t *testing.T) {
	r := newMLWiringRig(t, mlOtherOrg, nil)
	ctx := context.Background()
	d := r.w.gate.Refresh(ctx)
	if d.Open || d.Reason != mlproducer.ReasonOrgNotAllowed {
		t.Fatalf("gate = %+v, want closed / %s", d, mlproducer.ReasonOrgNotAllowed)
	}
	r.toolCall("s1", "kenaz__read_file", `{"path":"a.go"}`, "x")
	if recs := r.outbox(); len(recs) != 0 {
		t.Fatalf("recorded %d rows for org %s", len(recs), mlOtherOrg)
	}
	// Seed the outbox directly (as if left behind) — the shipper still
	// sends nothing.
	if _, err := r.w.store.Commit(ctx, mlstore.Write{Events: []mlstore.EventDraft{{CreatedAt: 1, Body: func(int64) ([]byte, error) { return []byte(`{}`), nil }}}}); err != nil {
		t.Fatal(err)
	}
	r.shipNow()
	if posts, _, _, mlGets := r.fleet.snapshot(); len(posts) != 0 || mlGets != 0 {
		t.Fatalf("posts=%d /me/ml reads=%d for a non-dev org, want 0 and 0", len(posts), mlGets)
	}
	if v := r.w.shippingStatus(); v.StopReason != mlproducer.ReasonOrgNotAllowed {
		t.Errorf("panel stop reason = %q", v.StopReason)
	}
}

// What reaches Fleet: protobuf, the token's resource-owner org, the
// enrolled node id, and the five kameas.ml.* attributes per record.
func TestMLWiring_WireShapeThroughRealClient(t *testing.T) {
	r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
	ctx := context.Background()
	if !r.w.gate.Refresh(ctx).Open {
		t.Fatalf("gate closed: %+v", r.w.gate.Last())
	}
	r.toolCall("s1", "kenaz__write_file", `{"path":"/Users/alice/secret/plan.go","content":"x"}`, `{"bytes_written":1}`)
	r.toolCall("s1", "kenaz__bash", `{"command":"go test ./internal/secret/..."}`, `{"stdout":"","stderr":"","exit_code":1,"truncated":false}`)
	if n := len(r.outbox()); n == 0 {
		t.Fatal("nothing recorded with the gate open")
	}
	r.shipNow()
	posts, cts, _, _ := r.fleet.snapshot()
	if len(posts) != 1 || cts[0] != mlproducer.OTLPContentType {
		t.Fatalf("posts=%d content-types=%v, want 1 × %s", len(posts), cts, mlproducer.OTLPContentType)
	}
	res := map[string]string{}
	for _, kv := range posts[0].GetResourceLogs()[0].GetResource().GetAttributes() {
		res[kv.GetKey()] = kv.GetValue().GetStringValue()
	}
	node := corefleet.ReadNodeID(r.dataDir)
	if node == "" || res["kameas.node.id"] != node || res["kameas.org.id"] != mlTestZitadelOrg ||
		res["service.name"] != "kenaz-harness" || len(res) != 3 {
		t.Fatalf("resource attrs = %v (node_id.txt %q)", res, node)
	}
	raw, _ := proto.Marshal(posts[0])
	for _, leak := range []string{"/Users/alice", "secret/plan", "./internal", mlproducer.DevOrgID} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("request bytes contain %q", leak)
		}
	}
	if left := len(r.outbox()); left != 0 {
		t.Errorf("outbox after a 2xx = %d rows, want 0", left)
	}
	if v := r.w.shippingStatus(); v.Accepted == 0 || v.LastBatchAt == "" {
		t.Errorf("panel status = %+v", v)
	}
}

// The spec §6 response table, through fleet.Client.Post.
func TestMLWiring_ResponseTableThroughRealClient(t *testing.T) {
	seed := func(r *mlWiringRig, n int) {
		for i := 0; i < n; i++ {
			r.toolCall("s1", "kenaz__read_file", `{"path":"f`+strconv.Itoa(i)+`.go"}`, "x")
		}
		if got := len(r.outbox()); got < n {
			r.t.Fatalf("seeded %d rows, want ≥ %d", got, n)
		}
	}
	open := func(r *mlWiringRig) {
		if !r.w.gate.Refresh(context.Background()).Open {
			r.t.Fatalf("gate closed: %+v", r.w.gate.Last())
		}
	}

	t.Run("2xx advances; rejections counted", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 3)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
				return otlpReply{status: 200, body: `{"accepted":3,"ml":{"accepted":3,"duplicates":1,"rejected":1,"rejections":{"timestamp_in_future":1}}}`}
			}
		})
		r.shipNow()
		if left := len(r.outbox()); left != 0 {
			t.Fatalf("outbox = %d after 2xx", left)
		}
		if v := r.w.shippingStatus(); v.Accepted != 3 || v.Duplicates != 1 || v.Rejected != 1 {
			t.Fatalf("status = %+v", v)
		}
	})

	t.Run("403 ml_not_effective stops, purges, re-reads", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 3)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
				return otlpReply{status: 403, body: `{"code":"ml_not_effective","message":"x"}`}
			}
		})
		r.shipNow()
		if left := len(r.outbox()); left != 0 {
			t.Fatalf("outbox = %d after ml_not_effective, want purged", left)
		}
		if r.w.gate.Recording() {
			t.Fatal("still recording after ml_not_effective")
		}
		_, _, _, before := r.fleet.snapshot()
		r.w.gate.Refresh(context.Background())
		if _, _, _, after := r.fleet.snapshot(); after != before+1 {
			t.Fatalf("/me/ml not re-read (%d → %d)", before, after)
		}
	})

	t.Run("403 ml_node_not_enrolled holds and re-enrolls", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 2)
		var refuse atomic.Bool
		refuse.Store(true)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
				if refuse.Load() {
					return otlpReply{status: 403, body: `{"code":"ml_node_not_enrolled","message":"x"}`}
				}
				return otlpReply{status: 200}
			}
		})
		_, _, enrollsBefore, _ := r.fleet.snapshot()
		r.shipNow()
		waitForCond(t, func() bool { _, _, e, _ := r.fleet.snapshot(); return e > enrollsBefore }, "re-enroll POST /api/v1/enroll")
		refuse.Store(false)
		waitForCond(t, func() bool {
			r.shipNow()
			return len(r.outbox()) == 0
		}, "resend after enrolment changed")
		posts, _, _, _ := r.fleet.snapshot()
		if len(posts) != 2 {
			t.Fatalf("posts = %d, want 2 (refused once, resent once after re-enroll)", len(posts))
		}
	})

	t.Run("400 unsupported_schema_version stops: update the harness", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 2)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
				return otlpReply{status: 400, body: `{"code":"unsupported_schema_version","message":"x"}`}
			}
		})
		r.shipNow()
		r.shipNow()
		if posts, _, _, _ := r.fleet.snapshot(); len(posts) != 1 {
			t.Fatalf("posts = %d, want 1", len(posts))
		}
		if v := r.w.shippingStatus(); !strings.HasPrefix(v.StopReason, mlproducer.StopUpdateHarness) {
			t.Fatalf("stop reason = %q", v.StopReason)
		}
	})

	t.Run("413 ml_batch_too_large splits", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 40)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(_ int, req *collogs.ExportLogsServiceRequest) otlpReply {
				if otlpRecordCount(req) > 15 {
					return otlpReply{status: 413, body: `{"code":"ml_batch_too_large","message":"x"}`}
				}
				return otlpReply{status: 200}
			}
		})
		r.shipNow()
		if left := len(r.outbox()); left != 0 {
			t.Fatalf("outbox = %d after split resends", left)
		}
	})

	t.Run("429 waits Retry-After, keeps records", func(t *testing.T) {
		r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
		open(r)
		seed(r, 2)
		r.fleet.set(func(f *mlFleet) {
			f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
				return otlpReply{status: 429, body: `{"code":"rate_limited","message":"x"}`, retryAfter: "42"}
			}
		})
		r.shipNow()
		if left := len(r.outbox()); left == 0 {
			t.Fatal("records dropped on 429")
		}
		if posts, _, _, _ := r.fleet.snapshot(); len(posts) != 1 {
			t.Fatalf("posts = %d, want 1 (no hot retry)", len(posts))
		}
	})
}

// Lifecycle: agent_pids at construction; start → the gate opens → the
// shipper runs; a session reset (sign-out) purges and pauses it with a
// stop reason; shutdown drains and removes agent_pids.
func TestMLWiring_LifecycleAndAgentPIDs(t *testing.T) {
	r := newMLWiringRig(t, mlproducer.DevOrgID, nil)
	pidPath := mlproducer.AgentPIDsPath(r.dataDir)
	b, err := os.ReadFile(pidPath)
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("agent_pids = %q, %v; want this pid", b, err)
	}
	if fi, _ := os.Stat(pidPath); fi.Mode().Perm() != 0o600 {
		t.Errorf("agent_pids mode = %v", fi.Mode().Perm())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.w.start(ctx)
	r.w.start(ctx) // idempotent
	waitForCond(t, r.w.shipper.Running, "shipper starts once the gate opens")

	r.toolCall("s1", "kenaz__read_file", `{"path":"a.go"}`, "x")
	_ = r.w.rec.Flush(context.Background())
	r.w.shipper.Nudge() // skip the 30 s idle cadence
	waitForCond(t, func() bool { posts, _, _, _ := r.fleet.snapshot(); return len(posts) >= 1 }, "loop ships")

	// Sign-out: the settings API clears the enrolled identity and runs its
	// session-reset hooks; onSessionReset is the hook this wiring registered.
	r.toolCall("s1", "kenaz__read_file", `{"path":"b.go"}`, "x")
	_ = r.w.rec.Flush(context.Background())
	r.api.StopFleetBackground()
	r.w.onSessionReset()
	waitForCond(t, func() bool { return !r.w.shipper.Running() }, "shipper paused after sign-out")
	if left := len(r.outbox()); left != 0 {
		t.Fatalf("outbox = %d after a session reset, want purged", left)
	}
	if v := r.w.shippingStatus(); v.StopReason != mlproducer.ReasonNotEnrolled {
		t.Errorf("stop reason after sign-out = %q, want %q", v.StopReason, mlproducer.ReasonNotEnrolled)
	}

	r.w.shutdown()
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("agent_pids survives shutdown: %v", err)
	}
	r.w.shutdown() // idempotent
}

// The end-to-end wiring proof: a real chat turn through newLLMStack's chat
// runner over newGraphManagerWithDeps' kernel. With the gate open the
// agent's tool call and turn become outbox rows, and the bash child sees
// KENAZ_ACTOR=agent; with the gate closed the same turn writes nothing.
func TestMLWiring_ChatRunnerToolCallReachesOutbox(t *testing.T) {
	const cmd = "printenv KENAZ_ACTOR"
	r := newMLWiringRig(t, mlproducer.DevOrgID, func(dataDir string) {
		pattern := corebash.DerivePattern(corebash.FirstSegmentArgv(cmd))
		polDir := filepath.Join(dataDir, cedar.PolicyDir)
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			t.Fatal(err)
		}
		grant := "permit(\n  principal,\n  action == Action::\"run_bash_command\",\n  resource == BashCommand::\"" + pattern + "\"\n);\n"
		if err := os.WriteFile(filepath.Join(polDir, "zz_ml_allow.cedar"), []byte(grant), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	cedarEngine := buildCedarEngineOrNil(r.dataDir, nil)
	memStore := openMemoryStore(r.c)
	if cedarEngine == nil || memStore == nil {
		t.Fatal("cedar engine / memory store unavailable over a real DataDir")
	}
	bashStore := corebash.NewStore()
	graphMgr, _, _, _ := newGraphManagerWithDeps(r.c, nil, nil, memStore, nil, bashStore, nil, cedarEngine, nil, nil, r.w)

	var reqN int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&reqN, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		var frames []string
		if n%2 == 1 {
			args, _ := json.Marshal(map[string]string{"command": cmd})
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"zz-ml-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_` + strconv.Itoa(int(n)) + `","name":"` + corebash.Name + `","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + strconv.Quote(string(args)) + `}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		} else {
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_2","role":"assistant","model":"zz-ml-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	stack := newLLMStack(r.c, NewStreamBroker(NewMultiEmitter()), newPersonalStore(r.c), nil, nil, func() bool { return false },
		nil, nil, nil, bashStore, nil, graphMgr, nil, nil, nil, nil,
		nil, nil, nil, nil, confirmAuditEmitter{}, nil, cedarEngine, nil, nil, nil, nil, r.w)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))
	t.Setenv("ZZ_ML_WIRING_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{ID: "zz-ml-wiring", Kind: anthropic.Kind, Model: "default",
		Cred: corellm.CredentialReference{Kind: "env", Locator: "ZZ_ML_WIRING_KEY"}}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	ctx := context.Background()
	turn := func(name string) (sessionID, toolOutput string) {
		t.Helper()
		rec, err := r.c.SessionManager().Create(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		row, err := r.c.SessionManager().AppendMessage(ctx, rec.ID, session.Message{Role: session.RoleUser, Content: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stack.chatRunner.StartStream(ctx, prof.ID, rec.ID, "", chat.UserTurn{MessageID: row.ID, Text: "go", Announce: true}); err != nil {
			t.Fatalf("StartStream: %v", err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			msgs, _ := stack.historyAdapter.ListMessages(ctx, rec.ID)
			done := false
			for _, m := range msgs {
				if m.Role == string(session.RoleTool) && strings.Contains(m.Content, `"stdout"`) {
					toolOutput = m.Content
				}
				if m.Role == string(session.RoleAssistant) && strings.Contains(m.Content, "done") {
					done = true
				}
			}
			if done {
				return rec.ID, toolOutput
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("turn %s did not finish", name)
		return "", ""
	}

	// Gate open.
	if !r.w.gate.Refresh(ctx).Open {
		t.Fatalf("gate closed: %+v", r.w.gate.Last())
	}
	_, out := turn("zz-ml-open")
	if !strings.Contains(out, "agent") {
		t.Errorf("bash output %q: KENAZ_ACTOR=agent did not reach the agent's process (bash EnvProvider unwired?)", out)
	}
	var kinds []string
	waitForCond(t, func() bool {
		kinds = kinds[:0]
		for _, rec := range r.outbox() {
			if rec.Table == mlstore.TableEvents {
				var ev struct {
					Kind string `json:"kind"`
				}
				_ = json.Unmarshal(rec.Body, &ev)
				kinds = append(kinds, ev.Kind)
			}
		}
		return strings.Contains(strings.Join(kinds, ","), mlproducer.KindTurn)
	}, "agent.turn row (chat TurnUsage fan-out)")
	if joined := strings.Join(kinds, ","); !strings.Contains(joined, mlproducer.KindTerminal) {
		t.Fatalf("outbox kinds = %s, want the bash call as %s (EnvDeps.ToolCalls unwired?)", joined, mlproducer.KindTerminal)
	}

	// Gate closed: consent withdrawn → purge, and the next turn records
	// nothing.
	r.fleet.set(func(f *mlFleet) { f.meML = mlEffectiveBody(true, "off", false, true) })
	if r.w.gate.Refresh(ctx).Open {
		t.Fatal("gate still open after withdrawal")
	}
	if n := len(r.outbox()); n != 0 {
		t.Fatalf("outbox = %d rows after withdrawal, want purged", n)
	}
	turn("zz-ml-closed")
	time.Sleep(100 * time.Millisecond)
	if n := len(r.outbox()); n != 0 {
		t.Fatalf("a turn with the gate closed recorded %d rows", n)
	}
}

// rpc.New builds the producer (agent_pids, a running recorder) and
// API.Shutdown drains it: the recorder goroutine exits and agent_pids goes.
func TestMLWiring_NewAndShutdown(t *testing.T) {
	const recorderFrame = "core/mlproducer.(*Recorder).run"
	baseline := countGoroutineFrames(recorderFrame)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	if api.mlProducer == nil {
		t.Fatal("rpc.New over a real DataDir built no ML producer")
	}
	if got := countGoroutineFrames(recorderFrame); got != baseline+1 {
		t.Fatalf("recorder goroutines = %d, want baseline %d + 1", got, baseline)
	}
	pidPath := mlproducer.AgentPIDsPath(c.DataDir())
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("agent_pids not written by rpc.New: %v", err)
	}
	api.Shutdown()
	waitForCond(t, func() bool { return countGoroutineFrames(recorderFrame) == baseline }, "recorder goroutine exits on Shutdown")
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("agent_pids survives Shutdown: %v", err)
	}
}

type countingTurnObs struct{ started, failed, ended atomic.Int32 }

func (o *countingTurnObs) TurnStarted(context.Context, string, string)      { o.started.Add(1) }
func (o *countingTurnObs) TurnFailed(context.Context, string, string, bool) { o.failed.Add(1) }
func (o *countingTurnObs) TurnEnded(context.Context, string, string, int, int, time.Duration) {
	o.ended.Add(1)
}

// The chat runner has ONE TurnUsage slot; the fan-out feeds both the fleet
// usage observer and the ML recorder, and never yields a typed nil.
func TestFanOutTurnUsage(t *testing.T) {
	t.Parallel()
	if fanOutTurnUsage(nil, nil) != nil {
		t.Fatal("all-nil fan-out is not nil")
	}
	a, b := &countingTurnObs{}, &countingTurnObs{}
	if got := fanOutTurnUsage(nil, a); got != chat.TurnUsageObserver(a) {
		t.Fatal("single live observer not returned as itself")
	}
	f := fanOutTurnUsage(a, nil, b)
	ctx := context.Background()
	f.TurnStarted(ctx, "s", "p")
	f.TurnFailed(ctx, "s", "k", true)
	f.TurnEnded(ctx, "s", "completed", 1, 1, time.Second)
	for i, o := range []*countingTurnObs{a, b} {
		if o.started.Load() != 1 || o.failed.Load() != 1 || o.ended.Load() != 1 {
			t.Errorf("observer %d saw started=%d failed=%d ended=%d", i, o.started.Load(), o.failed.Load(), o.ended.Load())
		}
	}
	var w *mlProducerWiring
	if w.turnObserver() != nil || w.toolCallObserver() != nil || w.parentLinker() != nil || w.bashEnvProvider(nil) != nil {
		t.Fatal("a nil wiring hands out a non-nil observer")
	}
	w.start(ctx)
	w.shutdown()
}

func waitForCond(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
