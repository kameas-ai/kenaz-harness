package settings

// ml_test.go — ml-producer-01MLPRD01 WP01: the Cloud ML panel view.
// Golden notice text (both contract variants), the ack 409 re-read path,
// fail-closed reads, the capability gate on the read, and the WP03
// shipping-status seam. Runs a real fleet.Client against a fake Fleet with
// tokens from the external token source (no keychain).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestRenderMLNotice_Golden(t *testing.T) {
	cases := []struct {
		name   string
		org    string
		days   int
		retain bool
		want   string
	}{
		{
			name: "delete on withdrawal (default)",
			org:  "Acme Corp", days: 90, retain: false,
			want: "Acme Corp has turned on hosted inference. Kenaz will upload your activity on this device to " +
				"Fleet: files you open and edit (paths, not contents), terminal commands, window titles, " +
				"browser URLs, and summaries of your coding tasks (repo, branch, test and commit counts). " +
				"It is kept for 90 days. Your organization controls this setting. If it is turned off, " +
				"uploads stop and your uploaded data is deleted within 72 hours.",
		},
		{
			name: "retain_on_withdrawal variant",
			org:  "Acme Corp", days: 30, retain: true,
			want: "Acme Corp has turned on hosted inference. Kenaz will upload your activity on this device to " +
				"Fleet: files you open and edit (paths, not contents), terminal commands, window titles, " +
				"browser URLs, and summaries of your coding tasks (repo, branch, test and commit counts). " +
				"It is kept for 30 days. Your organization controls this setting. If it is turned off, " +
				"uploads stop. Your organization has instructed that data already uploaded is kept until " +
				"the 30 days have passed.",
		},
		{
			name: "no org name falls back",
			org:  "  ", days: 7, retain: false,
			want: "Your organization has turned on hosted inference. Kenaz will upload your activity on this device to " +
				"Fleet: files you open and edit (paths, not contents), terminal commands, window titles, " +
				"browser URLs, and summaries of your coding tasks (repo, branch, test and commit counts). " +
				"It is kept for 7 days. Your organization controls this setting. If it is turned off, " +
				"uploads stop and your uploaded data is deleted within 72 hours.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderMLNotice(tc.org, tc.days, tc.retain); got != tc.want {
				t.Errorf("notice mismatch\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// mlFakeFleet is a fake Fleet for the ML routes. Written from the handler
// goroutine, read from the test body: mutex + accessors.
type mlFakeFleet struct {
	srv *httptest.Server

	mu        sync.Mutex
	state     string // body served by GET /me/ml
	getStatus int
	ackStatus int
	ackBody   string
	gets      int
	acks      []int
	ackRaw    []string // raw notice-ack request bodies
	optIns    []string
	// Pending-approvals hub (WP06). hubStatus 0 = 404 (an older Fleet).
	hubStatus int
	hubBody   string
	hubGets   int
	// hubPosts records every POST to a hub approve endpoint other than
	// notice-ack (which is recorded in acks/ackRaw): "path body".
	hubPosts   []string
	hubPostSt  int
	hubPostRes string
}

func newMLFakeFleet(t *testing.T) *mlFakeFleet {
	t.Helper()
	f := &mlFakeFleet{getStatus: 200, ackStatus: 200}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("/api/v1/me/ml", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.gets++
		st, body := f.getStatus, f.state
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/api/v1/me/ml/notice-ack", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			NoticeVersion int `json:"notice_version"`
		}
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.acks = append(f.acks, req.NoticeVersion)
		f.ackRaw = append(f.ackRaw, string(raw))
		st, body := f.ackStatus, f.ackBody
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/api/v1/me/pending-approvals", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hubGets++
		st, body := f.hubStatus, f.hubBody
		f.mu.Unlock()
		if st == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	})
	hubPost := func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.hubPosts = append(f.hubPosts, r.URL.Path+" "+string(raw))
		st, body := f.hubPostSt, f.hubPostRes
		f.mu.Unlock()
		if st == 0 {
			st, body = 200, `{}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, body)
	}
	mux.HandleFunc("/api/v1/me/legal-acceptances", hubPost)
	mux.HandleFunc("/api/v1/me/ml/exclusions-seen", hubPost)
	mux.HandleFunc("/api/v1/me/telemetry-opt-ins", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.optIns = append(f.optIns, string(b))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *mlFakeFleet) set(fn func(*mlFakeFleet)) { f.mu.Lock(); fn(f); f.mu.Unlock() }
func (f *mlFakeFleet) snapshot() (gets int, acks []int, optIns []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets, append([]int(nil), f.acks...), append([]string(nil), f.optIns...)
}

func (f *mlFakeFleet) ackBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ackRaw...)
}

// withTextRevisions adds the Fleet PR #225 optional keys to an mlBody.
func withTextRevisions(body string, notice, acked int) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	m["notice_text_revision"] = notice
	m["acked_text_revision"] = acked
	b, _ := json.Marshal(m)
	return string(b)
}

func mlBody(offload bool, policy string, optedIn, ackRequired, effective bool, version int, ackedAt string, days int, retain bool) string {
	m := map[string]any{
		"org_offload_enabled": offload, "org_policy": policy, "user_workflow_events_opted_in": optedIn,
		"notice_ack_required": ackRequired, "effective": effective, "notice_version": version,
		"notice_acked_at": nil, "retention_days": days, "retain_on_withdrawal": retain,
		"exclusions":         map[string]any{"paths": []string{}, "commands": []string{}, "exclude_browser": false},
		"exclusions_version": 1, "legacy_exclusion_notes": []string{},
	}
	if ackedAt != "" {
		m["notice_acked_at"] = ackedAt
	}
	b, _ := json.Marshal(m)
	return string(b)
}

type mlRig struct {
	api   *API
	fleet *mlFakeFleet
}

func newMLRig(t *testing.T, entitled bool) *mlRig {
	t.Helper() // NOT t.Parallel: SetExternalTokenSource is process-global
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1: rig requires fleet enabled")
	}
	f := newMLFakeFleet(t)
	tok := jwtFor("sub-alice", "zitadel-org-1")
	fleet.SetExternalTokenSource(func() string { return tok })
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	dir := t.TempDir()
	if err := fleet.SaveIdentity(dir, fleet.Identity{OrgID: "o1", OrgName: "Acme Corp"}); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	api := &API{}
	api.SetFleetClient(fleet.NewClientForTesting(f.srv.URL), dir)
	t.Cleanup(api.StopFleetBackground)
	api.CapabilityPoller().ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "team", Enabled: map[fleet.Capability]bool{fleet.CapHostedInference: entitled},
		FetchedAt: time.Now(), Source: "fleet",
	})
	return &mlRig{api: api, fleet: f}
}

func TestFleetMLStatus_NotWired(t *testing.T) {
	v, err := (&API{}).FleetMLStatus(context.Background())
	if err != nil {
		t.Fatalf("FleetMLStatus: %v", err)
	}
	if v.SignedIn || v.Entitled || v.Effective || v.Loaded || v.Shipping != nil {
		t.Errorf("unwired view = %+v", v)
	}
	if _, err := (&API{}).FleetMLAckNotice(context.Background(), 1); err == nil {
		t.Error("ack on an unwired API succeeded")
	}
	if _, err := (&API{}).FleetSetWorkflowEventsOptIn(context.Background(), true); err == nil {
		t.Error("opt-in on an unwired API succeeded")
	}
}

func TestFleetMLStatus_NotEntitled_NoRead(t *testing.T) {
	r := newMLRig(t, false)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = mlBody(true, "on", false, false, true, 1, "2026-10-09T00:00:00Z", 90, false)
	})
	v, err := r.api.FleetMLStatus(context.Background())
	if err != nil {
		t.Fatalf("FleetMLStatus: %v", err)
	}
	if !v.SignedIn || v.Entitled || v.Effective || v.Loaded {
		t.Errorf("view = %+v, want signed in, not entitled, nothing loaded", v)
	}
	if gets, _, _ := r.fleet.snapshot(); gets != 0 {
		t.Errorf("/me/ml read %d times without hosted_inference, want 0", gets)
	}
}

func TestFleetMLStatus_States(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantEffective bool
		wantNotice    bool
		wantPolicy    string
	}{
		{"offload off", mlBody(false, "member_choice", false, false, false, 1, "", 90, false), false, false, "member_choice"},
		{"member_choice not opted in", mlBody(true, "member_choice", false, false, false, 2, "", 90, false), false, false, "member_choice"},
		{"notice required", mlBody(true, "on", false, true, false, 3, "", 60, true), false, true, "on"},
		{"effective", mlBody(true, "member_choice", true, false, true, 3, "2026-10-09T12:00:00Z", 60, false), true, false, "member_choice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newMLRig(t, true)
			r.fleet.set(func(f *mlFakeFleet) { f.state = tc.body })
			v, err := r.api.FleetMLStatus(context.Background())
			if err != nil {
				t.Fatalf("FleetMLStatus: %v", err)
			}
			if !v.Loaded || v.FleetError != "" {
				t.Fatalf("not loaded: %+v", v)
			}
			if v.Effective != tc.wantEffective || v.NoticeAckRequired != tc.wantNotice || v.OrgPolicy != tc.wantPolicy {
				t.Errorf("view = %+v", v)
			}
			if v.OrgName != "Acme Corp" || v.NoticeText != RenderMLNotice("Acme Corp", v.RetentionDays, v.RetainOnWithdrawal) {
				t.Errorf("notice/org = %q / %q", v.OrgName, v.NoticeText)
			}
		})
	}
}

// WP05: the org's typed exclusions and legacy notes reach the panel view
// read-only; with none configured the lists are [] (never null).
func TestFleetMLStatus_Exclusions(t *testing.T) {
	r := newMLRig(t, true)
	var m map[string]any
	_ = json.Unmarshal([]byte(mlBody(true, "on", false, false, true, 3, "2026-10-09T12:00:00Z", 60, false)), &m)
	m["exclusions"] = map[string]any{"paths": []string{"hr/**", "~/private/**"}, "commands": []string{"ssh"}, "exclude_browser": true}
	m["exclusions_version"] = 6
	m["legacy_exclusion_notes"] = []string{"Nothing from the HR share"}
	b, _ := json.Marshal(m)
	r.fleet.set(func(f *mlFakeFleet) { f.state = string(b) })
	v, err := r.api.FleetMLStatus(context.Background())
	if err != nil || !v.Loaded {
		t.Fatalf("FleetMLStatus: %+v %v", v, err)
	}
	if len(v.ExclusionPaths) != 2 || v.ExclusionPaths[1] != "~/private/**" || len(v.ExclusionCommands) != 1 ||
		!v.ExcludeBrowser || v.ExclusionsVersion != 6 || len(v.LegacyExclusionNotes) != 1 {
		t.Errorf("exclusions view = %+v", v)
	}

	r.fleet.set(func(f *mlFakeFleet) { f.state = mlBody(true, "on", false, false, false, 1, "", 90, false) })
	v, _ = r.api.FleetMLStatus(context.Background())
	raw, _ := json.Marshal(v)
	for _, k := range []string{`"exclusionPaths":[]`, `"exclusionCommands":[]`, `"legacyExclusionNotes":[]`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("wire view missing %s: %s", k, raw)
		}
	}
	// Not loaded (unwired): still [] on the wire.
	raw, _ = json.Marshal(func() MLStatusView { v, _ := (&API{}).FleetMLStatus(context.Background()); return v }())
	if !strings.Contains(string(raw), `"exclusionPaths":[]`) {
		t.Errorf("unloaded wire view: %s", raw)
	}
}

func TestFleetMLStatus_ErrorsFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"staff 403", 403, `{"code":"staff_not_permitted","message":"staff"}`},
		{"500", 500, `{"code":"internal_error"}`},
		{"undecodable effective", 200, `{"effective":true}`},
		{"malformed exclusions", 200, strings.Replace(mlBody(true, "on", false, false, true, 1, "2026-10-09T12:00:00Z", 90, false),
			`"paths":[]`, `"paths":"hr/**"`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newMLRig(t, true)
			r.fleet.set(func(f *mlFakeFleet) { f.getStatus, f.state = tc.status, tc.body })
			v, err := r.api.FleetMLStatus(context.Background())
			if err != nil {
				t.Fatalf("FleetMLStatus: %v", err)
			}
			if v.Effective || v.Loaded || v.FleetError == "" || v.NoticeText != "" {
				t.Errorf("error path view = %+v, want not effective, not loaded, FleetError set", v)
			}
		})
	}
}

func TestFleetMLAckNotice_PostsShownVersion(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = mlBody(true, "on", false, true, false, 4, "", 90, false)
		f.ackBody = mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false)
	})
	v, err := r.api.FleetMLAckNotice(context.Background(), 4)
	if err != nil {
		t.Fatalf("FleetMLAckNotice: %v", err)
	}
	if !v.Effective || v.NoticeChanged || v.NoticeAckedAt != "2026-10-09T13:00:00Z" {
		t.Errorf("view = %+v", v)
	}
	if _, acks, _ := r.fleet.snapshot(); len(acks) != 1 || acks[0] != 4 {
		t.Errorf("acks = %v, want [4]", acks)
	}
	// An older Fleet (no notice_text_revision) gets the original body.
	if bodies := r.fleet.ackBodies(); len(bodies) != 1 || bodies[0] != `{"notice_version":4}` {
		t.Errorf("ack bodies = %v, want [{\"notice_version\":4}] (no text_revision)", bodies)
	}
}

// Fleet PR #225 rev-1 server: the harness acks the text revision it
// rendered (localNoticeTextRevision = 1).
func TestFleetMLAckNotice_Rev1ServerSendsTextRevision(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = withTextRevisions(mlBody(true, "on", false, true, false, 4, "", 90, false), 1, 0)
		f.ackBody = withTextRevisions(mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false), 1, 1)
	})
	v, err := r.api.FleetMLAckNotice(context.Background(), 4)
	if err != nil {
		t.Fatalf("FleetMLAckNotice: %v", err)
	}
	if !v.Effective || v.NoticeNeedsDashboard || v.NoticeTextRevision != 1 || v.AckedTextRevision != 1 {
		t.Errorf("view = %+v", v)
	}
	if bodies := r.fleet.ackBodies(); len(bodies) != 1 || bodies[0] != `{"notice_version":4,"text_revision":1}` {
		t.Errorf("ack bodies = %v, want [{\"notice_version\":4,\"text_revision\":1}]", bodies)
	}
}

// Fleet PR #225 rev-2 server: the harness renders only rev 1, so it must
// NOT acknowledge rev 2 — no POST, no local notice text, dashboard routing.
func TestFleetMLAckNotice_Rev2ServerRoutesToDashboardWithoutPost(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = withTextRevisions(mlBody(true, "on", false, true, false, 4, "", 90, false), 2, 1)
		f.ackBody = mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false)
	})
	v, err := r.api.FleetMLAckNotice(context.Background(), 4)
	if err != nil {
		t.Fatalf("FleetMLAckNotice: %v", err)
	}
	if !v.NoticeNeedsDashboard || v.NoticeText != "" || v.Effective || !v.NoticeAckRequired || v.NoticeTextRevision != 2 {
		t.Errorf("view = %+v, want NoticeNeedsDashboard, empty NoticeText, not effective", v)
	}
	if want := r.fleet.srv.URL + "/settings#hosted-inference"; v.NoticeDashboardURL != want {
		t.Errorf("NoticeDashboardURL = %q, want %q", v.NoticeDashboardURL, want)
	}
	if _, acks, _ := r.fleet.snapshot(); len(acks) != 0 {
		t.Errorf("acks = %v, want none (rev 2 was never shown)", acks)
	}
	// The status read reports the same routing.
	s, err := r.api.FleetMLStatus(context.Background())
	if err != nil || !s.NoticeNeedsDashboard || s.NoticeText != "" || s.NoticeDashboardURL == "" {
		t.Errorf("status = %+v, %v", s, err)
	}
}

// A rev-2 server whose notice is already acknowledged (no ack required)
// needs no dashboard trip: NoticeNeedsDashboard is false.
func TestFleetMLStatus_Rev2NoAckRequiredIsNotDashboard(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = withTextRevisions(mlBody(true, "on", false, false, true, 4, "2026-10-09T13:00:00Z", 90, false), 2, 2)
	})
	v, err := r.api.FleetMLStatus(context.Background())
	if err != nil || v.NoticeNeedsDashboard || v.NoticeDashboardURL != "" || !v.Effective {
		t.Errorf("view = %+v, %v", v, err)
	}
}

func TestMLDashboardConsentURL(t *testing.T) {
	cases := map[string]string{
		"https://dev.fleet.kameas.ai":  "https://dev.fleet.kameas.ai/settings#hosted-inference",
		"https://dev.fleet.kameas.ai/": "https://dev.fleet.kameas.ai/settings#hosted-inference",
		"":                             "",
		"  ":                           "",
		"dev.fleet.kameas.ai":          "",
		"javascript:alert(1)":          "",
	}
	for in, want := range cases {
		if got := mlDashboardConsentURL(in); got != want {
			t.Errorf("mlDashboardConsentURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFleetMLAckNotice_409ReReadsAndReShows(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.ackStatus = 409
		f.ackBody = `{"code":"policy_changed","message":"the org notice changed since it was fetched; re-read /api/v1/me/ml"}`
		// The re-read shows a NEW version that still needs an ack, now with
		// the retention variant.
		f.state = mlBody(true, "on", false, true, false, 5, "", 45, true)
	})
	v, err := r.api.FleetMLAckNotice(context.Background(), 4)
	if err != nil {
		t.Fatalf("FleetMLAckNotice: %v", err)
	}
	if !v.NoticeChanged || !v.NoticeAckRequired || v.Effective || v.NoticeVersion != 5 {
		t.Errorf("view = %+v, want noticeChanged + required + version 5", v)
	}
	if v.NoticeText != RenderMLNotice("Acme Corp", 45, true) {
		t.Errorf("re-shown notice = %q", v.NoticeText)
	}
	// One pre-ack read (text-revision check), the ack, then the re-read.
	if gets, acks, _ := r.fleet.snapshot(); gets != 2 || len(acks) != 1 {
		t.Errorf("gets=%d acks=%v, want pre-read + one ack + one re-read", gets, acks)
	}
}

func TestFleetSetWorkflowEventsOptIn_PutsThenReReads(t *testing.T) {
	r := newMLRig(t, true)
	r.fleet.set(func(f *mlFakeFleet) {
		f.state = mlBody(true, "member_choice", true, true, false, 2, "", 90, false)
	})
	v, err := r.api.FleetSetWorkflowEventsOptIn(context.Background(), true)
	if err != nil {
		t.Fatalf("FleetSetWorkflowEventsOptIn: %v", err)
	}
	if !v.UserWorkflowEventsOptedIn || !v.NoticeAckRequired {
		t.Errorf("view = %+v", v)
	}
	gets, _, optIns := r.fleet.snapshot()
	if len(optIns) != 1 || optIns[0] != `{"updates":[{"class":"workflow_events","opted_in":true}]}` || gets != 1 {
		t.Errorf("optIns=%v gets=%d", optIns, gets)
	}
}

func TestFleetMLStatus_ShippingProviderSeam(t *testing.T) {
	a := &API{}
	if v, _ := a.FleetMLStatus(context.Background()); v.Shipping != nil {
		t.Fatalf("shipping = %+v before a provider is installed, want nil", v.Shipping)
	}
	a.SetMLShippingStatusProvider(MLShippingStatusFunc(func() MLShippingStatusView {
		return MLShippingStatusView{LastBatchAt: "2026-10-09T14:00:00Z", Accepted: 7, Duplicates: 1, Rejected: 2, StopReason: "ml_not_effective"}
	}))
	v, _ := a.FleetMLStatus(context.Background())
	if v.Shipping == nil || v.Shipping.Accepted != 7 || v.Shipping.Rejected != 2 || v.Shipping.StopReason != "ml_not_effective" {
		t.Errorf("shipping = %+v", v.Shipping)
	}
	a.SetMLShippingStatusProvider(nil)
	if v, _ := a.FleetMLStatus(context.Background()); v.Shipping != nil {
		t.Errorf("shipping after uninstall = %+v", v.Shipping)
	}
}
