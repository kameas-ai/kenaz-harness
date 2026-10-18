package mlsidecar

// interop_test.go — the 2026-09-30 cross-repo interop review's pins. Each
// test decodes (or is refused by) the kenaz-ml engine's REAL wire bytes,
// captured verbatim from a live `kenaz-ml serve` at kenaz-ml
// kitty/mission-two-client-engine-01MSK2EN (ca31a94 / 7e419aa), so a shape
// drift on either side fails here rather than silently in production. The
// stub (sidecar_stub_test.go) mirrors the same shapes for the behavioural
// tests; these pin the bytes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	advescalate "github.com/kameas-ai/kenaz-harness/core/advice/kinds/escalatemodel"
	"github.com/kameas-ai/kenaz-harness/core/advice/labels"
)

// realEngineHealth is a /health body from the live engine (local mode),
// exe_path/engine_sha256 shortened.
const realEngineHealth = `{"status":"ok","mode":"local","models":{"stuck":"untrained","activity":"untrained","workflow":"untrained","duration":"untrained","quality":"ready"},"uptime_sec":0.2,"product":"kenaz-ml","sidecar_version":"0.1.0","contract_versions":{"branch_now":["20e0d23994daee80"],"compact_now":["3ad8b0c1fa514476"],"escalate_model":["57d4bdc1bd6a71be"]},"exe_path":"/opt/kenaz-ml/kenaz-ml","engine_sha256":"36984af9f922aec7","model_details":{"stuck":{"status":"untrained","slot":null,"refusal":null},"quality":{"status":"ready","slot":"local","refusal":null}},"device":"cpu","lifecycle_protocol":1}`

// realEngineContracts is a /v1/contracts body from the live engine (one
// kind shown; all three were available=false on day 1).
const realEngineContracts = `{"kinds":{"branch_now":{"features":["turns_since_session_start","turns_since_last_branch","prior_branch_count","edit_resend_precursor","heuristic_signal_count","heuristic_noise_count","last_user_msg_len","tool_call_density_window"],"dtypes":["float64","float64","float64","float64","float64","float64","float64","float64"],"version":"20e0d23994daee80","supported_versions":["20e0d23994daee80"],"available":false,"backend":null,"reason":"kind_not_served","detail":"kind unavailable: no backend serves 'branch_now' (the client's heuristic answers)"}}}`

// realEngineLease is a POST /v1/clients/lease response from the live engine.
const realEngineLease = `{"lifecycle_protocol":1,"sidecar_version":"0.1.0","contract_versions":{"branch_now":["20e0d23994daee80"]},"incompatible_kinds":{"branch_now":"contract deadbeefdeadbeef not supported (engine supports ['20e0d23994daee80'])"},"client":"harness","pid":26344,"live_leases":2,"implicit_lease_sec":90.0,"idle_exit_sec":120.0,"managed":true}`

// realEngineRefusal is a 422 /v1/recommend refusal body from the live engine.
const realEngineRefusal = `{"error":"kind_not_served","refusal":{"kind_id":"branch_now","reason":"kind_not_served","detail":"kind unavailable: no backend serves 'branch_now' (the client's heuristic answers)"}}`

func TestInterop_RealEngineHealth_Decodes(t *testing.T) {
	var h HealthPayload
	if err := json.Unmarshal([]byte(realEngineHealth), &h); err != nil {
		t.Fatalf("the real engine's /health does not decode: %v", err)
	}
	if h.LifecycleProtocol != 1 || h.Product != "kenaz-ml" || h.SidecarVersion != "0.1.0" {
		t.Errorf("identity fields: %+v", h)
	}
	if got := h.ContractVersions["branch_now"]; len(got) != 1 || got[0] != "20e0d23994daee80" {
		t.Errorf("contract_versions[branch_now] = %v", got)
	}
	if h.Models["quality"] != "ready" || h.ModelDetails["quality"].Slot != "local" {
		t.Errorf("models / model_details: %v / %v", h.Models, h.ModelDetails)
	}
	if h.EngineSHA256 == "" {
		t.Error("engine_sha256 not carried")
	}
	if !contractCompatible(h, SupportedContractMajor) {
		t.Error("the real engine's lifecycle protocol is not compatible with this build")
	}
}

func TestInterop_RealEngineContractsAndLease_Decode(t *testing.T) {
	var c ContractsPayload
	if err := json.Unmarshal([]byte(realEngineContracts), &c); err != nil {
		t.Fatalf("/v1/contracts: %v", err)
	}
	k := c.Kinds["branch_now"]
	if k.Version != "20e0d23994daee80" || k.Available || k.Reason != "kind_not_served" || len(k.Features) != 8 {
		t.Errorf("branch_now contract = %+v", k)
	}
	var l LeaseWireResponse
	if err := json.Unmarshal([]byte(realEngineLease), &l); err != nil {
		t.Fatalf("lease: %v", err)
	}
	if l.LifecycleProtocol != 1 || l.IncompatibleKinds["branch_now"] == "" || !l.Managed {
		t.Errorf("lease = %+v", l)
	}
}

func TestInterop_RealEngineRefusal_IsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(realEngineRefusal))
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, nil).Recommend(context.Background(), "branch_now", RecommendRequest{})
	if !errors.Is(err, ErrKindNotServed) {
		t.Fatalf("the engine's real 422 envelope is not ErrKindNotServed: %v", err)
	}
}

// TestInterop_ShutdownTokenIsTheEnginesFilename pins the cross-repo token
// path: kenaz-ml reads <install_root>/lease/shutdown.token.
func TestInterop_ShutdownTokenIsTheEnginesFilename(t *testing.T) {
	l := NewLayout(t.TempDir())
	if got, want := l.TokenFile(), filepath.Join(l.Root, "lease", "shutdown.token"); got != want {
		t.Fatalf("TokenFile() = %q, want %q (the path kenaz-ml's /v1/admin/shutdown reads)", got, want)
	}
}

// TestManager_UnusableHealth_IsPortConflict_NeverSpawns: something answers
// the engine port but not with a decodable /health (a foreign process, or a shape
// drift like the one this review found). Before the fix a decode error
// took the "nothing is listening" branch and spawned a second engine onto
// the occupied port on every reconcile.
func TestManager_UnusableHealth_IsPortConflict_NeverSpawns(t *testing.T) {
	for name, set := range map[string]func(*stubSidecar){
		"undecodable body": func(s *stubSidecar) { s.setHealthRaw([]byte(`{"lifecycle_protocol":"kenaz-ml-lease/1"}`)) },
		"non-2xx":          func(s *stubSidecar) { s.setHealthStatus(http.StatusServiceUnavailable) },
	} {
		t.Run(name, func(t *testing.T) {
			l := NewLayout(t.TempDir())
			setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))
			stub := newStubSidecar()
			defer stub.Close()
			set(stub)
			spawner := &fakeSpawner{}
			m := NewManager(l, NewClient(stub.URL(), nil), spawner, "harness", "0.85.0")
			got := m.Reconcile(context.Background())
			if spawner.called {
				t.Fatal("spawned an engine onto a port something is already answering on")
			}
			if got.State != StateInstalledUnhealthy || got.Reason != ReasonPortConflict {
				t.Fatalf("status = %+v, want installed_unhealthy/port_conflict", got)
			}
		})
	}
}

// ---- label lane pause (interop ruling 2026-09-30: a 409 must not hot-retry) ----

func TestLabelPush_409PausesTheKind_NoHotRetry_CursorKept(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "escalate_model", "h1", 1000)
	h.insert(t, "branch_now", "h2", 1001)

	now := time.Unix(1_000_000, 0)
	p := newPusher(stub, h.store, true)
	p.Now = func() time.Time { return now }

	stub.setLabelsFail(http.StatusConflict)
	if _, err := p.PushOnce(ctx); err == nil {
		t.Fatal("a 409 returned no error")
	}
	st := p.LaneStatus()
	if len(st) != 2 || st["escalate_model"].Reason != PauseContractMismatch || st["escalate_model"].Code != "contract_mismatch" {
		t.Fatalf("LaneStatus = %+v, want both kinds paused on contract_mismatch", st)
	}
	posts := stub.labelPostCount()

	// Every label write nudges; while paused, NO request may leave.
	for i := 0; i < 5; i++ {
		res, err := p.PushOnce(ctx)
		if err != nil || len(res.Paused) != 2 {
			t.Fatalf("paused PushOnce = %+v, %v", res, err)
		}
	}
	if stub.labelPostCount() != posts {
		t.Fatalf("hot retry: %d POSTs while paused", stub.labelPostCount()-posts)
	}
	if cur, _ := h.store.LoadCursor(ctx, "sidecar", "escalate_model"); (cur != labels.PushCursor{}) {
		t.Fatalf("cursor moved on a 409: %+v", cur)
	}

	// Backoff elapses, engine still mismatched: re-probed once, backoff doubles.
	now = now.Add(labelPauseBaseBackoff + time.Second)
	_, _ = p.PushOnce(ctx)
	if b := p.LaneStatus()["escalate_model"].Backoff; b != 2*labelPauseBaseBackoff {
		t.Fatalf("second pause backoff = %v, want doubled", b)
	}

	// Skew resolved: after the backoff the kind flows and the pause clears.
	stub.setLabelsFail(0)
	now = now.Add(labelPauseMaxBackoff)
	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 2 {
		t.Fatalf("after the fix = %+v, %v; want both rows delivered", res, err)
	}
	if len(p.LaneStatus()) != 0 {
		t.Fatalf("pause not cleared: %+v", p.LaneStatus())
	}
}

func TestLabelPush_404UnknownKind_Pauses(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	h.insert(t, "some_new_kind", "h1", 1000)
	p := newPusher(stub, h.store, true)
	stub.setLabelsFail(http.StatusNotFound)
	_, _ = p.PushOnce(context.Background())
	if r := p.LaneStatus()["some_new_kind"].Reason; r != PauseUnknownKind {
		t.Fatalf("pause reason = %q, want %q", r, PauseUnknownKind)
	}
}

func TestLabelPush_503IsTransient_NotPaused(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	h.insert(t, "branch_now", "h1", 1000)
	p := newPusher(stub, h.store, true)
	stub.setLabelsFail(http.StatusServiceUnavailable)
	if _, err := p.PushOnce(context.Background()); err == nil {
		t.Fatal("503 returned no error")
	}
	if len(p.LaneStatus()) != 0 {
		t.Fatalf("a transient 503 paused the lane: %+v", p.LaneStatus())
	}
}

// TestLabelPush_PermanentRowRefusal_AckedPast_LoggedNotAnError is design
// Amendment A4: the engine acks PAST a permanently-refused middle row and
// reports it in refusals. The pusher advances to the ack (the last row),
// counts the refusal, returns no error and does not pause — one bad row
// no longer wedges the kind's lane.
func TestLabelPush_PermanentRowRefusal_AckedPast_LoggedNotAnError(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	for i, hash := range []string{"h1", "h2", "h3"} {
		h.insert(t, "branch_now", hash, int64(1000+i))
	}
	rows, _ := h.store.PendingSince(ctx, "branch_now", 0, 10)
	stub.setLabelsRefuseRevision(rows[1].Revision, "features_invalid")
	p := newPusher(stub, h.store, true)
	res, err := p.PushOnce(ctx)
	if err != nil {
		t.Fatalf("a permanent per-row refusal surfaced as an error: %v", err)
	}
	if res.Pushed != 2 || res.RowsRefused != 1 {
		t.Fatalf("PushOnce = %+v, want 2 pushed + 1 refused", res)
	}
	cur, _ := h.store.LoadCursor(ctx, "sidecar", "branch_now")
	if cur.Revision != rows[2].Revision {
		t.Fatalf("cursor = %+v, want past the refused row to revision %d", cur, rows[2].Revision)
	}
	if len(p.LaneStatus()) != 0 {
		t.Fatalf("a permanent row refusal paused the lane: %+v", p.LaneStatus())
	}
	posts := stub.labelPostCount()
	if res, err := p.PushOnce(ctx); err != nil || res.Batches != 0 {
		t.Fatalf("second PushOnce = %+v, %v; want nothing left to send", res, err)
	}
	if stub.labelPostCount() != posts {
		t.Fatal("the refused row was re-sent")
	}
}

// TestLabelPush_PreA4NullAck_PausesInsteadOfHotRetry is the defensive
// path: an older engine that answers acked:null when a refused row leads
// the batch. The pusher advances to the real ack of the prefix, then
// parks the kind rather than re-sending the poison row on every write.
func TestLabelPush_PreA4NullAck_PausesInsteadOfHotRetry(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	rows, _ := h.store.PendingSince(ctx, "branch_now", 0, 10)
	stub.setLabelsNullAck(true)
	stub.setLabelsRefuseRevision(rows[0].Revision, "features_invalid")
	p := newPusher(stub, h.store, true)
	if _, err := p.PushOnce(ctx); err == nil {
		t.Fatal("a null ack returned no error")
	}
	if cur, _ := h.store.LoadCursor(ctx, "sidecar", "branch_now"); (cur != labels.PushCursor{}) {
		t.Fatalf("cursor moved on a null ack: %+v", cur)
	}
	if r := p.LaneStatus()["branch_now"].Reason; r != PauseRowsRefused {
		t.Fatalf("pause reason = %q, want %q", r, PauseRowsRefused)
	}
	posts := stub.labelPostCount()
	_, _ = p.PushOnce(ctx)
	if stub.labelPostCount() != posts {
		t.Fatal("the poison row was hot-retried")
	}
}

// TestRevision_ConcurrentWritersNeverShareARevision drives the
// MAX(revision)+1 stamp from many goroutines through the PRODUCTION
// storage.Open handle (its real pool, WAL and busy handling — not a
// single-connection test DB): SQLite takes the write lock at the start of
// each autocommit INSERT/UPDATE, before the subquery reads MAX, so no two
// writes can observe the same MAX. The push cursor's total order depends
// on it.
func TestRevision_ConcurrentWritersNeverShareARevision(t *testing.T) {
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	const writers, per = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*per*2)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			sess := fmt.Sprintf("s%d", w)
			for i := 0; i < per; i++ {
				hash := fmt.Sprintf("w%d-%d", w, i)
				if err := h.store.Insert(ctx, labels.Row{KindID: "branch_now", PromptVersion: "v1", FeaturesHash: hash,
					FeaturesJSON: `{}`, FeaturesComplete: true, ModelID: "m", Rung: "heuristic", Confidence: 50,
					UserAction: labels.ActionIgnored, SessionID: sess, CreatedAt: time.UnixMilli(int64(1000 + i)).UTC()}); err != nil {
					errs <- err
					continue
				}
				if i%2 == 0 {
					if err := h.store.UpdateAction(ctx, sess, "branch_now", hash, labels.ActionAccepted); err != nil {
						errs <- err
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write: %v", err)
	}
	rows, err := h.store.PendingSince(ctx, "branch_now", 0, writers*per*2)
	if err != nil || len(rows) != writers*per {
		t.Fatalf("rows = %d, err %v", len(rows), err)
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.Revision] {
			t.Fatalf("revision %d stamped on two rows", r.Revision)
		}
		seen[r.Revision] = true
	}
}

// TestEvaluateAdoption_EngineBareHexDigest_A5 is design Amendment A5's
// bug pin: the real engine reports /health engine_sha256 as BARE hex while
// install.json carries "sha256:<hex>". A raw string compare refused every
// real adoption; the normalized compare adopts the matching digest and
// still refuses a different one.
func TestEvaluateAdoption_EngineBareHexDigest_A5(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))
	bare := strings.TrimPrefix(sha, "sha256:")
	if bare == sha {
		t.Fatalf("fixture digest %q carries no sha256: prefix; the pin needs the prefixed record form", sha)
	}
	health := HealthPayload{SidecarVersion: "1.0.0", ExePath: exePath, EngineSHA256: strings.ToUpper(bare), LifecycleProtocol: 1}
	if d, err := EvaluateAdoption(l, health, nil); err != nil || d.Action != AdoptAccept {
		t.Fatalf("bare-hex self-report of the RIGHT digest = %+v, %v; want adopted", d, err)
	}
	health.EngineSHA256 = strings.Repeat("0", len(bare))
	if d, _ := EvaluateAdoption(l, health, nil); d.Action != AdoptRefuseUnverified {
		t.Fatalf("bare-hex self-report of a WRONG digest = %q; want refused", d.Action)
	}
}

// TestEvaluateAdoption_SidecarVersionIsNotAGate_A5 is Amendment A5(4)'s
// relaxation: sidecar_version is a noted detail, never a refusal — engine
// builds report 0.1.0 everywhere today and Kenaz labels are
// "<semver>+<sha12>". Record-vs-directory-label consistency is what is
// enforced (VerifyInstalled).
func TestEvaluateAdoption_SidecarVersionIsNotAGate_A5(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "0.2.0+1a2b3c4d5e6f", []byte("engine binary bytes"))
	health := HealthPayload{SidecarVersion: "0.2.0", ExePath: exePath, EngineSHA256: sha, LifecycleProtocol: 1}
	if d, err := EvaluateAdoption(l, health, nil); err != nil || d.Action != AdoptAccept || strings.Contains(d.Detail, "note:") {
		t.Fatalf("label 0.2.0+build vs report 0.2.0 = %+v, %v; want adopted, no note", d, err)
	}
	health.SidecarVersion = "0.1.0"
	d, err := EvaluateAdoption(l, health, nil)
	if err != nil || d.Action != AdoptAccept || !strings.Contains(d.Detail, "note:") {
		t.Fatalf("report 0.1.0 vs label 0.2.0+build = %+v, %v; want adopted WITH a note", d, err)
	}
}

// engineEscalateModelContract is kenaz-ml's escalate_model contract
// (advice/contracts.py ESCALATE_MODEL_FEATURES), verbatim.
var engineEscalateModelContract = []string{
	"consecutive_tool_failures", "retries_in_window", "turn_latency_trend", "current_rung",
	"error_kind_auth", "error_kind_transient", "error_kind_cancelled", "error_kind_budget", "error_kind_unknown",
	"budget_remaining_fraction",
}

// insertEscalate captures one escalate_model row the way the capture
// bridge does: features from the REAL kind's Extract, the kind's
// registered PromptVersion.
func insertEscalate(t *testing.T, h *pushHarness, promptVersion, featuresJSON, hash string, ts int64) {
	t.Helper()
	if err := h.store.Insert(context.Background(), labels.Row{KindID: advescalate.KindID, PromptVersion: promptVersion,
		FeaturesHash: hash, FeaturesJSON: featuresJSON, FeaturesComplete: false, ModelID: advescalate.ModelID,
		Rung: "heuristic", Confidence: 0, UserAction: labels.ActionIgnored, SessionID: "sess",
		CreatedAt: time.UnixMilli(ts).UTC()}); err != nil {
		t.Fatal(err)
	}
}

// TestLabelPush_EscalateModelV2_ClearsTheContractPause is the 2026-09-30
// ruling's un-pause proof. A v1-era corpus row (WP06's improvised
// doom_loop_* / string current_rung shape) parks the lane on the engine's
// 409 names_mismatch exactly as it did live. With the v2 catalog Features
// and the superseded-version skip wired as production wires it, the
// paused lane — once its backoff elapses — steps past the v1 row, pushes
// the v2 row, and clears the pause. The v1 row stays in the harness DB.
func TestLabelPush_EscalateModelV2_ClearsTheContractPause(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	stub.setLabelsContract(advescalate.KindID, engineEscalateModelContract)
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()

	v1 := `{"consecutive_tool_failures":0,"retries_in_window":0,"doom_loop_repeat_count":0,"doom_loop_threshold":3,"current_rung":"","budget_remaining_fraction":0}`
	insertEscalate(t, h, "v1", v1, "old", 1000)

	now := time.Unix(1_000_000, 0)
	p := newPusher(stub, h.store, true)
	p.Now = func() time.Time { return now }
	if _, err := p.PushOnce(ctx); err == nil || p.LaneStatus()[advescalate.KindID].Reason != PauseContractMismatch {
		t.Fatalf("the v1 row did not reproduce the live 409 pause: %v / %+v", err, p.LaneStatus())
	}

	// The release lands: v2 Features, PromptVersion v2, and the
	// production superseded-version skip.
	k, ok := advice.Get(advescalate.KindID)
	if !ok || k.PromptVersion != "v2" {
		t.Fatalf("escalate_model registered as %+v, want PromptVersion v2", k)
	}
	f, err := advescalate.Extract(advescalate.Snapshot{ConsecutiveToolFailures: 1, CurrentRung: 2,
		ErrorKindCounts: map[string]int{"transient": 1}, FeaturesIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := labels.MarshalFeatures(f)
	insertEscalate(t, h, k.PromptVersion, v2, "new", 2000)
	p.CurrentPromptVersion = func(kind string) (string, bool) {
		kk, ok := advice.Get(kind)
		return kk.PromptVersion, ok
	}

	posts := stub.labelPostCount()
	if res, _ := p.PushOnce(ctx); len(res.Paused) != 1 || stub.labelPostCount() != posts {
		t.Fatalf("inside the backoff: %+v, %d new POSTs; want still paused and silent", res, stub.labelPostCount()-posts)
	}

	now = now.Add(labelPauseBaseBackoff + time.Second)
	res, err := p.PushOnce(ctx)
	if err != nil || res.Pushed != 1 {
		t.Fatalf("after the backoff = %+v, %v; want the v2 row delivered", res, err)
	}
	if len(p.LaneStatus()) != 0 {
		t.Fatalf("the contract pause did not clear: %+v", p.LaneStatus())
	}
	mirror := stub.mirrorSnapshot()
	if len(mirror) != 1 {
		t.Fatalf("engine mirror = %d rows, want only the v2 row", len(mirror))
	}
	for _, row := range mirror {
		if row.PromptVersion != "v2" || row.FeaturesHash != "new" {
			t.Errorf("mirrored %+v, want the v2 row", row)
		}
	}
	all, _ := h.store.PendingSince(ctx, advescalate.KindID, 0, 10)
	cur, _ := h.store.LoadCursor(ctx, "sidecar", advescalate.KindID)
	if len(all) != 2 || cur.Revision != all[1].Revision {
		t.Fatalf("local rows = %d, cursor = %+v; want both rows kept and the cursor past both", len(all), cur)
	}
}

// TestLabelPush_SupersededOnlyBatch_StepsCursorWithoutARequest: a batch
// made entirely of superseded-version rows sends nothing and still
// advances the cursor.
func TestLabelPush_SupersededOnlyBatch_StepsCursorWithoutARequest(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	insertEscalate(t, h, "v1", `{}`, "a", 1000)
	insertEscalate(t, h, "v1", `{}`, "b", 1001)
	p := newPusher(stub, h.store, true)
	p.CurrentPromptVersion = func(string) (string, bool) { return "v2", true }
	if _, err := p.PushOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.labelPostCount() != 0 {
		t.Fatalf("%d POSTs for superseded-only rows", stub.labelPostCount())
	}
	all, _ := h.store.PendingSince(context.Background(), advescalate.KindID, 0, 10)
	if cur, _ := h.store.LoadCursor(context.Background(), "sidecar", advescalate.KindID); cur.Revision != all[1].Revision {
		t.Fatalf("cursor = %+v, want past both superseded rows", cur)
	}
}
