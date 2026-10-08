package fleet

// org_paused_test.go — the staff "pause paid features" org state
// (kenaz-fleet #206). Every fleet consumer that used to classify a 403
// treats 403 org_paused as TRANSIENT: back off on its existing tiers, no
// permanent latch, no not_authorized / not_entitled / signed_out label, and
// the lane reason "org_paused". The capability poll is the recovery signal:
// paused true→false fans out OnOrgUnpaused and every circuit reopens
// without a sign-in.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// pausedFleet is a fake fleet with a central pause gate and an explicit
// allowlist, like #206's. Handler goroutines write, the test body reads:
// every field is mutex-guarded and read through snapshot helpers.
type pausedFleet struct {
	srv *httptest.Server

	mu       sync.Mutex
	category string // "" = not paused
	hits     map[string]int
}

// pausedAllowlist mirrors the data-rights routes fleet keeps open.
var pausedAllowlist = map[string]bool{
	"/api/v1/me/capabilities":   true,
	"/api/v1/memory/forget-all": true,
}

func newPausedFleet(t *testing.T, category string) *pausedFleet {
	t.Helper()
	f := &pausedFleet{category: category, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.hits[r.URL.Path]++
		cat := f.category
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if cat != "" && !pausedAllowlist[r.URL.Path] {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "org_paused",
				"message": "Your organization's paid features are paused. Contact support.",
				"details": map[string]any{"paused_category": cat},
			})
			return
		}
		switch r.URL.Path {
		case "/api/v1/me/capabilities":
			body := map[string]any{"tier": "team", "capabilities": map[string]bool{
				"team_graph_sharing": cat == "", "memory_sync": cat == "", "context_sync": cat == "",
			}}
			if cat != "" {
				body["paused"], body["paused_category"] = true, cat
			}
			_ = json.NewEncoder(w).Encode(body)
		case "/api/v1/configs":
			w.WriteHeader(http.StatusNotModified)
		case "/api/v1/catalog/list":
			_, _ = io.WriteString(w, `{"items":[]}`)
		case "/api/v1/context/pull":
			_, _ = io.WriteString(w, `{"nodes":[],"edges":[],"cursor":"c1"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *pausedFleet) setPaused(category string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.category = category
}

func (f *pausedFleet) hitsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func pausedClient(t *testing.T, f *pausedFleet) *Client {
	t.Helper()
	stubTokens(t, TokenSet{AccessToken: "at-paused", RefreshToken: "rt-paused", ExpiresAt: time.Now().Add(time.Hour)})
	return makeTestClient(t, f.srv.URL)
}

// ── the classifier ──────────────────────────────────────────────────────────

func TestParseOrgPaused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string // "" = not org_paused
	}{
		{"confirmed contract", 403, `{"code":"org_paused","message":"m","details":{"paused_category":"billing_review"}}`, "billing_review"},
		{"pr206 gate shape", 403, `{"code":"org_paused","message":"m","details":{"paused":true,"category":"abuse"}}`, "abuse"},
		{"no details", 403, `{"code":"org_paused","message":"m"}`, "other"},
		{"unknown category", 403, `{"code":"org_paused","details":{"paused_category":"tax"}}`, "other"},
		{"tier, not paused", 403, `{"code":"capability_not_in_tier","message":"m"}`, ""},
		{"wrong status", 409, `{"code":"org_paused"}`, ""},
		{"not json", 403, `forbidden`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pe := ParseOrgPaused(tc.status, []byte(tc.body))
			switch {
			case tc.want == "" && pe != nil:
				t.Fatalf("got %+v, want nil", pe)
			case tc.want != "" && (pe == nil || pe.PausedCategory != tc.want):
				t.Fatalf("got %+v, want category %q", pe, tc.want)
			}
			if pe != nil && (!IsOrgPaused(pe) || errors.Is(pe, ErrCapabilityNotInTier)) {
				t.Fatalf("classification: IsOrgPaused=%v tier=%v", IsOrgPaused(pe), errors.Is(pe, ErrCapabilityNotInTier))
			}
		})
	}
	for _, c := range []string{"billing_review", "security", "abuse", "legal", "other"} {
		if NormalizePausedCategory(c) != c {
			t.Errorf("category %q not preserved", c)
		}
	}
}

func TestClientDo_OrgPaused_TypedErrorAndObserved(t *testing.T) {
	f := newPausedFleet(t, "security")
	c := pausedClient(t, f)
	var changes []OrgPauseStatus
	var mu sync.Mutex
	c.OnOrgPauseChange(func(s OrgPauseStatus) { mu.Lock(); changes = append(changes, s); mu.Unlock() })

	resp, err := c.Get(context.Background(), "/api/v1/context/health")
	if resp != nil || !IsOrgPaused(err) || OrgPausedCategoryOf(err) != "security" {
		t.Fatalf("resp=%v err=%v, want *OrgPausedError(security)", resp, err)
	}
	if st := c.OrgPause(); !st.Paused || st.PausedCategory != "security" {
		t.Fatalf("OrgPause = %+v", st)
	}
	mu.Lock()
	n := len(changes)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnOrgPauseChange fired %d times, want 1", n)
	}
}

func TestClientDo_OtherForbidden_BodyIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"capability_not_in_tier","message":"tier"}`)
	}))
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, srv.URL)
	resp, err := c.Get(context.Background(), "/api/v1/x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 || !strings.Contains(string(b), "capability_not_in_tier") {
		t.Fatalf("status %d body %q — a non-pause 403 must reach the caller unchanged", resp.StatusCode, b)
	}
	if c.OrgPause().Paused {
		t.Fatal("a tier 403 marked the org paused")
	}
}

// ── capability poll: parse, snapshot, recovery fan-out ─────────────────────

func TestCapabilityPoll_PausedThenUnpaused_FansOut(t *testing.T) {
	f := newPausedFleet(t, "billing_review")
	c := pausedClient(t, f)
	p := NewCapabilityPoller(c, t.TempDir())
	var mu sync.Mutex
	unpaused := 0
	c.OnOrgUnpaused(func() { mu.Lock(); unpaused++; mu.Unlock() })

	caps, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !caps.Paused || caps.PausedCategory != "billing_review" || caps.Tier != "team" || caps.Has(CapMemorySync) {
		t.Fatalf("paused caps = %+v", caps)
	}
	if st := c.OrgPause(); !st.Paused || st.PausedCategory != "billing_review" {
		t.Fatalf("client pause = %+v", st)
	}
	f.setPaused("")
	caps, err = p.Refresh(context.Background())
	if err != nil || caps.Paused || caps.PausedCategory != "" || !caps.Has(CapMemorySync) {
		t.Fatalf("unpaused caps = %+v err=%v", caps, err)
	}
	mu.Lock()
	n := unpaused
	mu.Unlock()
	if n != 1 || c.OrgPause().Paused {
		t.Fatalf("OnOrgUnpaused fired %d times (want 1); still paused=%v", n, c.OrgPause().Paused)
	}
	// A second unpaused poll is not a transition.
	_, _ = p.Refresh(context.Background())
	mu.Lock()
	n = unpaused
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnOrgUnpaused fired again without a transition (%d)", n)
	}
}

// ── append breaker (the bug this fixes: a PERMANENT not_authorized latch) ───

func TestAppendBreaker_OrgPaused_TransientThenReopenedByUnpausePoll(t *testing.T) {
	f := newPausedFleet(t, "abuse")
	es := makeTestStream(t, f.srv.URL, "sess:paused")
	c := es.client
	lanes := NewSyncLanes()
	b := NewAppendBreaker(lanes)
	// Production wiring: settings.onOrgUnpaused → appendBreaker.ResetOrgPaused.
	c.OnOrgUnpaused(b.ResetOrgPaused)
	p := NewCapabilityPoller(c, t.TempDir())

	appendOnce := func() error {
		return b.Do(context.Background(), "s1", func(ctx context.Context) error {
			return es.Append(ctx, []Event{{Payload: []byte("x")}})
		})
	}
	if err := appendOnce(); !IsOrgPaused(err) {
		t.Fatalf("append err = %v, want org_paused", err)
	}
	if reason, permanent := classifyAppendError(&OrgPausedError{PausedCategory: "abuse"}); reason != ReasonOrgPaused || permanent {
		t.Fatalf("classify = (%q, %v), want (org_paused, transient)", reason, permanent)
	}
	lane := lanes.Snapshot(LaneContextSync)
	if lane.Status != LaneDegraded || lane.Reason != ReasonOrgPaused || len(lane.Sessions) != 1 {
		t.Fatalf("lane = %+v, want degraded/org_paused", lane)
	}
	if s := lane.Sessions[0]; s.Open || s.Reason == "not_authorized" {
		t.Fatalf("session = %+v — org_paused must not open (latch) the circuit", s)
	}

	// The pause lifts; the capability poll is the only signal. No sign-in,
	// no toggle, no restart: the very next append is posted and succeeds.
	f.setPaused("")
	if _, err := p.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	before := f.hitsFor("/api/v1/context/append")
	if err := appendOnce(); err != nil {
		t.Fatalf("append after unpause: %v", err)
	}
	if got := f.hitsFor("/api/v1/context/append"); got != before+1 {
		t.Fatalf("append hits %d → %d, want one new post (circuit reopened)", before, got)
	}
	if lane := lanes.Snapshot(LaneContextSync); lane.Status != LaneOK {
		t.Fatalf("lane after recovery = %+v, want ok", lane)
	}
}

func TestAppendBreaker_ResetOrgPaused_LeavesOtherCircuits(t *testing.T) {
	b := NewAppendBreaker(NewSyncLanes())
	_ = b.Do(context.Background(), "missing", func(context.Context) error { return &AppendStatusError{Status: 404} })
	_ = b.Do(context.Background(), "paused", func(context.Context) error { return &OrgPausedError{PausedCategory: "legal"} })
	b.ResetOrgPaused()
	b.mu.Lock()
	_, missing := b.sessions["missing"]
	_, paused := b.sessions["paused"]
	b.mu.Unlock()
	if !missing || paused {
		t.Fatalf("after ResetOrgPaused: missing-context kept=%v (want true), paused kept=%v (want false)", missing, paused)
	}
}

// ── audit archiver ──────────────────────────────────────────────────────────

type pausedPoster struct {
	mu     sync.Mutex
	paused bool
	posts  int
}

func (p *pausedPoster) setPaused(v bool) { p.mu.Lock(); p.paused = v; p.mu.Unlock() }
func (p *pausedPoster) count() int       { p.mu.Lock(); defer p.mu.Unlock(); return p.posts }

func (p *pausedPoster) Post(_ context.Context, _, _ string, body io.Reader) (*http.Response, error) {
	_, _ = io.Copy(io.Discard, body)
	p.mu.Lock()
	p.posts++
	paused := p.paused
	p.mu.Unlock()
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	if paused {
		rec.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(rec, `{"code":"org_paused","message":"m","details":{"paused_category":"legal"}}`)
	} else {
		rec.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(rec, `{}`)
	}
	return rec.Result(), nil
}

func TestAuditArchiver_OrgPaused_TransientNoLatch_ResumesOnUnpause(t *testing.T) {
	p := &pausedPoster{paused: true}
	a := newArchiverWithEvents(t, p)
	if err := a.flushOnce(context.Background()); !IsOrgPaused(err) || errors.Is(err, ErrEndpointUnsupported) {
		t.Fatalf("flush err = %v, want org_paused (not unsupported)", err)
	}
	if a.TooLarge() || a.Unsupported() || a.ChainBreakDetected() {
		t.Fatal("org_paused set a permanent latch")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Stop()
	waitFor(t, func() bool { return a.OrgPaused() }, "archiver to record the org pause")
	if !a.IsRunning() {
		t.Fatal("archiver loop exited on org_paused — it must back off, not stop")
	}

	// Unpause: the fan-out wakes the loop out of its (30s) backoff and the
	// batch is accepted.
	p.setPaused(false)
	a.ResumeAfterOrgUnpause()
	waitFor(t, func() bool { return !a.LastArchivedAt().IsZero() }, "archival to resume after unpause")
	if a.OrgPaused() {
		t.Fatal("OrgPaused still true after a successful flush")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ── config poll ─────────────────────────────────────────────────────────────

func TestConfigPoller_OrgPaused_TransientReasonAndResume(t *testing.T) {
	f := newPausedFleet(t, "other")
	p := newPollerForTest(t, f.srv, &fakeApplier{}, t.TempDir())
	err := p.poll(context.Background())
	if !IsOrgPaused(err) {
		t.Fatalf("poll err = %v, want org_paused", err)
	}
	st := p.Status()
	if !st.OrgPaused || st.LastError != ReasonOrgPaused || st.SigningKeyUnknown {
		t.Fatalf("status = %+v, want orgPaused + lastError org_paused", st)
	}
	f.setPaused("")
	p.ResumeAfterOrgUnpause()
	if st := p.Status(); st.OrgPaused || st.LastError != "" {
		t.Fatalf("status after resume = %+v", st)
	}
	select {
	case <-p.wake:
	default:
		t.Fatal("ResumeAfterOrgUnpause did not wake the poll loop")
	}
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll after unpause: %v", err)
	}
}

// ── memory sync ─────────────────────────────────────────────────────────────

func TestMemorySync_OrgPaused_RefusalIsTransientAndResumes(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("dev-a", 0)
	d.enable()
	d.add("m1", "global", "fact one")
	d.sync()

	w.fleet.setPaused("billing_review")
	d.ms.RunOnce(context.Background())
	lane := d.lanes.Snapshot(LaneMemorySync)
	if lane.Status != LaneOff || lane.Reason != ReasonOrgPaused {
		t.Fatalf("lane = %+v, want off/org_paused (not push_failed, not not_entitled)", lane)
	}
	d.ms.mu.Lock()
	wait := d.ms.nextAttempt.Sub(w.wall.now())
	d.ms.mu.Unlock()
	if wait <= 0 || wait >= time.Hour {
		t.Fatalf("backoff = %s, want the normal (sub-hour) tiers", wait)
	}

	// Lifting the pause (the capability poll reporting paused:false, which
	// fans out OnOrgUnpaused — production wires the lane's resume there)
	// clears the org_paused backoff: the next cycle runs at once and the
	// lane is healthy again, with no sign-in.
	w.fleet.setPaused("")
	d.ms.cfg.Client.OnOrgUnpaused(d.ms.ResumeAfterOrgUnpause)
	d.ms.cfg.Client.ObserveCapabilitiesPause(false, "")
	d.sync()
}

func TestMemorySync_PausedCapabilities_LaneSaysOrgPausedNotEntitlement(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("dev-a", 0)
	d.caps = &Capabilities{Enabled: map[Capability]bool{}, FetchedAt: time.Now(), Paused: true, PausedCategory: "security"}
	d.ms.RunOnce(context.Background())
	if lane := d.lanes.Snapshot(LaneMemorySync); lane.Status != LaneOff || lane.Reason != ReasonOrgPaused {
		t.Fatalf("lane = %+v, want off/org_paused", lane)
	}
	st := d.ms.Status(context.Background())
	if !st.OrgPaused || st.PausedCategory != "security" || st.Entitled {
		t.Fatalf("status = %+v", st)
	}
}

// The data-rights route stays callable while paused: forget-all (the
// "delete from Fleet" half of turning sync off) succeeds even though the
// settings PUT before it is refused 403 org_paused.
func TestMemorySync_ForgetAll_SucceedsWhilePaused(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("dev-a", 0)
	d.enable()
	d.add("m1", "global", "fact one")
	d.add("m2", "long_term", "fact two")
	d.sync()
	if w.fleet.live("m1") == nil {
		t.Fatal("precondition: m1 not on fleet")
	}

	w.fleet.setPaused("legal")
	d.caps = &Capabilities{Enabled: map[Capability]bool{}, FetchedAt: time.Now(), Paused: true, PausedCategory: "legal"}
	n, err := d.ms.Disable(context.Background(), true, "forget-all")
	if err != nil {
		t.Fatalf("Disable(forget-all) while paused: %v", err)
	}
	if n != 2 || w.fleet.live("m1") != nil || w.fleet.live("m2") != nil {
		t.Fatalf("erased %d; m1 live=%v m2 live=%v — forget-all must reach fleet while paused", n, w.fleet.live("m1") != nil, w.fleet.live("m2") != nil)
	}
	st := d.ms.state()
	if st.Enabled || st.ForgetAllPending || !st.DisablePending {
		t.Fatalf("state = %+v: want local off, erase confirmed, the refused settings PUT still pending", st)
	}
}

// ── revoke sweep ────────────────────────────────────────────────────────────

func TestRevocationSweep_OrgPaused_ReasonAndBackoffReset(t *testing.T) {
	f := newSweepFixture(t)
	f.srv.set(http.StatusForbidden, `{"code":"org_paused","message":"m","details":{"paused_category":"abuse"}}`)
	f.install(t, slashcmd.Skill{ID: "sk1", Trigger: "sk1", Source: slashcmd.SkillSourceCatalog, CatalogID: "cat-1", Version: "1"})

	if _, err := f.sw.Sweep(context.Background()); !IsOrgPaused(err) {
		t.Fatalf("sweep err = %v, want org_paused", err)
	}
	lane := f.lanes.Snapshot(LaneCatalogRevocation)
	if lane.Reason != ReasonOrgPaused {
		t.Fatalf("lane reason = %q, want org_paused (not not_entitled)", lane.Reason)
	}
	if !f.has("sk1") {
		t.Fatal("a paused list uninstalled something")
	}
	f.sw.mu.Lock()
	skip := f.sw.skip
	f.sw.mu.Unlock()
	if skip == 0 {
		t.Fatal("org_paused did not back off")
	}
	f.sw.ResetBackoff()
	f.sw.mu.Lock()
	skip = f.sw.skip
	f.sw.mu.Unlock()
	if skip != 0 {
		t.Fatal("ResetBackoff (the unpause fan-out) left the skip in place")
	}
}

// ── catalog client / team handoff / sites ───────────────────────────────────

func TestCatalogUnpublish_OrgPaused_NotTier(t *testing.T) {
	f := newPausedFleet(t, "billing_review")
	c := pausedClient(t, f)
	err := c.Unpublish(context.Background(), "cat-1")
	if !IsOrgPaused(err) || errors.Is(err, ErrCapabilityNotInTier) {
		t.Fatalf("Unpublish err = %v, want org_paused and NOT capability_not_in_tier", err)
	}
}

func TestHandoffTransportError_OrgPaused(t *testing.T) {
	err := handoffTransportError(&OrgPausedError{PausedCategory: "security"})
	var he *HandoffError
	if !errors.As(err, &he) || he.Code != CodeOrgPaused || !IsOrgPaused(err) {
		t.Fatalf("err = %#v, want HandoffError{org_paused}", err)
	}
	if strings.Contains(strings.ToLower(he.Error()), "plan") || strings.Contains(he.Error(), "unavailable") {
		t.Fatalf("copy %q mislabels a pause", he.Error())
	}
}

func TestHandoffSend_OrgPaused_FromFleet(t *testing.T) {
	f := newPausedFleet(t, "security")
	withExternalToken(t, "tok-paused")
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = t.TempDir()
	h := NewHandoffHandler(c, &fakeEmitter{}, nil)
	_, err := h.ShareSession(context.Background(), "sess_p", "user-b", plainEvents(1))
	if !IsOrgPaused(err) {
		t.Fatalf("ShareSession err = %v, want org_paused", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "plan") {
		t.Fatalf("share error %q carries tier/upsell copy", err.Error())
	}
}

func TestMapSiteError_OrgPaused_NotTier(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(rec, `{"code":"org_paused","message":"m","details":{"paused_category":"other"}}`)
	err := mapSiteError(rec.Result())
	if !IsOrgPaused(err) || errors.Is(err, ErrCapabilityNotInTier) {
		t.Fatalf("mapSiteError = %v", err)
	}
}

// ── context graph sync / unit poll ──────────────────────────────────────────

func TestContextGraphPull_OrgPaused_ReasonNotTier(t *testing.T) {
	f := newPausedFleet(t, "legal")
	c := pausedClient(t, f)
	p := NewCapabilityPoller(c, t.TempDir())
	p.ForceSetCurrentForTesting(Capabilities{Enabled: map[Capability]bool{CapSharedTeamGraph: true}, FetchedAt: time.Now(), Source: "fleet"})
	s := NewContextGraphSyncer(c, t.TempDir(), p)
	_, err := s.PullDelta(context.Background())
	if !IsOrgPaused(err) {
		t.Fatalf("PullDelta err = %v, want org_paused", err)
	}
	if got := s.Status().LastPullErr; got != ReasonOrgPaused {
		t.Fatalf("LastPullErr = %q, want org_paused", got)
	}
}

func TestUnitPoll_OrgPaused_LaneOffWithReason(t *testing.T) {
	f := newPausedFleet(t, "abuse")
	c := pausedClient(t, f)
	p := NewCapabilityPoller(c, t.TempDir())
	s := NewUnitSyncer(c, newUnitTestManager(), NewUnitMapper("team-1"), p, t.TempDir())
	lanes := NewSyncLanes()
	s.SetLanes(lanes)

	s.reportPoll(&OrgPausedError{PausedCategory: "abuse"}, 1, time.Now().Add(time.Minute))
	if l := lanes.Snapshot(LaneUnitPoll); l.Status != LaneOff || l.Reason != ReasonOrgPaused {
		t.Fatalf("lane = %+v, want off/org_paused (not server_error)", l)
	}
	// A paused snapshot makes the un-entitled no-op pull read as paused,
	// not as a healthy "ok".
	p.ForceSetCurrentForTesting(Capabilities{Enabled: map[Capability]bool{}, FetchedAt: time.Now(), Source: "fleet", Paused: true, PausedCategory: "abuse"})
	s.reportPoll(nil, 0, time.Now().Add(time.Minute))
	if l := lanes.Snapshot(LaneUnitPoll); l.Status != LaneOff || l.Reason != ReasonOrgPaused {
		t.Fatalf("lane = %+v, want off/org_paused", l)
	}
	p.ForceSetCurrentForTesting(Capabilities{Enabled: map[Capability]bool{CapSharedTeamGraph: true}, FetchedAt: time.Now(), Source: "fleet"})
	s.reportPoll(nil, 0, time.Now().Add(time.Minute))
	if l := lanes.Snapshot(LaneUnitPoll); l.Status != LaneOK {
		t.Fatalf("lane after unpause = %+v, want ok", l)
	}
}
