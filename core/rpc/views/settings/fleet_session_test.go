package settings

// fleet_session_test.go — fleet-session-truth-01DOGF0A WP01.
//
// The FleetSession snapshot is the one answer every surface reads. These pin
// the state derivation against a real fleet.Client talking to a fake Fleet
// (enroll + /config.json), with tokens supplied through the external token
// source so no test here ever touches a keychain — real or mock.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// sessionFleet is a fake Fleet whose enroll answer is switchable. Written
// from the httptest handler goroutine, read from the test body: mutex +
// snapshot accessors (CLAUDE.md race-safe fakes).
type sessionFleet struct {
	srv *httptest.Server

	mu          sync.Mutex
	mode        string // "ok" | "server_error" | "not_provisioned" | "expired"
	email       string
	role        string
	enrollCalls int
	versions    []string
}

func newSessionFleet(t *testing.T) *sessionFleet {
	t.Helper()
	f := &sessionFleet{mode: "ok", email: "alice@example.com", role: "org_owner"}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("/api/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.enrollCalls++
		f.versions = append(f.versions, body.Version)
		mode, email, role := f.mode, f.email, f.role
		f.mu.Unlock()
		switch mode {
		case "server_error":
			// A 4xx that is not a sentinel: a plain server-side failure.
			http.Error(w, `{"code":"boom"}`, http.StatusConflict)
			return
		case "not_provisioned":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"user_not_provisioned","message":"finish signup"}`))
			return
		case "expired":
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"org_id": "org-uuid", "team_id": "t1", "org_name": "Kameas Dogfood",
			"team_name": "Everyone", "role": role, "tier": "enterprise", "email": email,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *sessionFleet) setMode(m string) { f.mu.Lock(); f.mode = m; f.mu.Unlock() }
func (f *sessionFleet) setEmail(e string) {
	f.mu.Lock()
	f.email = e
	f.mu.Unlock()
}
func (f *sessionFleet) calls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.enrollCalls }
func (f *sessionFleet) sentVersions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.versions...)
}

// sessionRig is a settings.API wired to a fake Fleet with a swappable token.
type sessionRig struct {
	api     *API
	fleet   *sessionFleet
	dataDir string

	tokMu sync.Mutex
	tok   string
}

func (r *sessionRig) setToken(tok string) { r.tokMu.Lock(); r.tok = tok; r.tokMu.Unlock() }
func (r *sessionRig) token() string       { r.tokMu.Lock(); defer r.tokMu.Unlock(); return r.tok }

func newSessionRig(t *testing.T) *sessionRig {
	t.Helper() // NOT t.Parallel: SetExternalTokenSource is process-global
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1: session rig requires fleet enabled")
	}
	r := &sessionRig{fleet: newSessionFleet(t), dataDir: t.TempDir()}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.api = &API{}
	r.api.SetFleetClient(fleet.NewClientForTesting(r.fleet.srv.URL), r.dataDir)
	t.Cleanup(r.api.StopFleetBackground)
	return r
}

func snap(t *testing.T, a *API) FleetSessionView {
	t.Helper()
	v, err := a.FleetSession(context.Background())
	if err != nil {
		t.Fatalf("FleetSession: %v", err)
	}
	return v
}

func TestFleetSession_NoClient_IsDisabled(t *testing.T) {
	v := snap(t, &API{})
	if v.State != FleetSessionDisabled {
		t.Fatalf("state = %q, want disabled", v.State)
	}
	if v.Capabilities.Source != "default-deny" {
		t.Fatalf("capabilities.source = %q, want default-deny", v.Capabilities.Source)
	}
}

func TestFleetSession_NoTokens_IsSignedOut_NoIdentity(t *testing.T) {
	r := newSessionRig(t)
	// A cached identity from a previous session must NOT surface while
	// signed out (spec §6: no stale identity after sign-out).
	_ = fleet.SaveIdentity(r.dataDir, fleet.Identity{OrgName: "Stale Org", Email: "old@example.com"})
	v := snap(t, r.api)
	if v.State != FleetSessionSignedOut {
		t.Fatalf("state = %q, want signed_out", v.State)
	}
	if v.Identity != nil {
		t.Fatalf("identity = %+v, want nil while signed out", v.Identity)
	}
	if v.Profile == nil {
		t.Fatalf("profile missing from a fleet-enabled snapshot")
	}
}

func TestFleetSession_EnrollOK_IsSignedIn_WithIdentityAndClaims(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("FleetRefreshIdentity: %v", err)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedIn {
		t.Fatalf("state = %q (reason %q), want signed_in", v.State, v.Reason)
	}
	if v.Identity == nil || v.Identity.OrgName != "Kameas Dogfood" || v.IdentitySource != "enroll" {
		t.Fatalf("identity = %+v source=%q, want enroll identity", v.Identity, v.IdentitySource)
	}
	if !v.Claims.HasSubject || !v.Claims.HasOrgClaim {
		t.Fatalf("claims = %+v, want subject + org claim", v.Claims)
	}
	if !v.AutoRetry {
		t.Fatalf("autoRetry = false on a healthy session")
	}
}

// P-1 (backend half): tokens valid + enroll fails → degraded with a reason,
// the cached identity still shown — never signed_out.
func TestFleetSession_EnrollFails_WithValidTokens_IsDegradedNotSignedOut(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	_ = fleet.SaveIdentity(r.dataDir, fleet.Identity{OrgName: "Cached Org", Tier: "enterprise"})
	r.fleet.setMode("server_error")
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err == nil {
		t.Fatalf("expected enroll error")
	}
	v := snap(t, r.api)
	if v.State != FleetSessionDegraded {
		t.Fatalf("state = %q, want degraded (FR-3: a failed refresh is not signed out)", v.State)
	}
	if v.Reason != FleetReasonServerError || v.Message == "" {
		t.Fatalf("reason = %q message = %q, want server_error + message", v.Reason, v.Message)
	}
	if v.Identity == nil || v.Identity.OrgName != "Cached Org" || v.IdentitySource != "cache" {
		t.Fatalf("identity = %+v source=%q, want cached identity shown while degraded", v.Identity, v.IdentitySource)
	}
	if !v.AutoRetry || v.NextRetryAt == "" {
		t.Fatalf("autoRetry=%v nextRetryAt=%q, want bounded automatic retry scheduled", v.AutoRetry, v.NextRetryAt)
	}

	// Recovery: the next successful enroll clears the degraded state.
	r.fleet.setMode("ok")
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("recovery enroll: %v", err)
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn || v.Reason != "" {
		t.Fatalf("after recovery state=%q reason=%q, want signed_in", v.State, v.Reason)
	}
}

// FR-4: not-provisioned stops automatic retries but stays degraded (with a
// recovery path), never signed out.
func TestFleetSession_NotProvisioned_DegradedAutoRetryStopped(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.fleet.setMode("not_provisioned")
	_, _ = r.api.FleetRefreshIdentity(context.Background())
	v := snap(t, r.api)
	if v.State != FleetSessionDegraded || v.Reason != FleetReasonNotProvisioned {
		t.Fatalf("state=%q reason=%q, want degraded/not_provisioned", v.State, v.Reason)
	}
	if v.AutoRetry {
		t.Fatalf("autoRetry = true after not-provisioned; the production fix requires a stop")
	}
	// Explicit retry is a recovery path: once fleet provisions the account,
	// the same RPC brings the session back and re-arms automatic refresh.
	r.fleet.setMode("ok")
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("explicit retry: %v", err)
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn || !v.AutoRetry {
		t.Fatalf("after explicit retry state=%q autoRetry=%v, want signed_in + auto retry re-armed", v.State, v.AutoRetry)
	}
}

func TestFleetSession_ExpiredSessionServerSide_IsSignedOut(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.fleet.setMode("expired")
	_, _ = r.api.FleetRefreshIdentity(context.Background())
	v := snap(t, r.api)
	if v.State != FleetSessionSignedOut || v.Reason != FleetReasonSessionExpired {
		t.Fatalf("state=%q reason=%q, want signed_out/session_expired", v.State, v.Reason)
	}
	if v.Identity != nil {
		t.Fatalf("identity shown on an expired session: %+v", v.Identity)
	}
}

func TestFleetSession_TokenWithoutOrgClaim_IsNamedFact(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "")) // no resource-owner claim
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedIn {
		t.Fatalf("state = %q, want signed_in (enrolled) — the claim gap is a separate fact", v.State)
	}
	if !v.Claims.HasSubject || v.Claims.HasOrgClaim {
		t.Fatalf("claims = %+v, want subject without org claim", v.Claims)
	}
}

func TestFleetSession_SignOut_ClearsIdentityAndCache(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	_ = fleet.SaveIdentity(r.dataDir, fleet.Identity{OrgName: "Cached Org"})
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	// The external source has nothing to clear; emulate the host session
	// ending alongside the RPC.
	r.setToken("")
	if err := r.api.FleetSignOut(context.Background()); err != nil {
		t.Fatalf("FleetSignOut: %v", err)
	}
	if _, err := os.Stat(fleet.IdentityFilePath(r.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("identity.json survives sign-out (stat err=%v)", err)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedOut || v.Identity != nil {
		t.Fatalf("after sign-out state=%q identity=%+v, want signed_out with no identity", v.State, v.Identity)
	}
}

func TestClassifyEnrollError(t *testing.T) {
	cases := []struct {
		err     error
		reason  string
		expired bool
	}{
		{fleet.ErrUserNotProvisioned, FleetReasonNotProvisioned, false},
		{fleet.ErrFleetUnreachable, FleetReasonNetwork, false},
		{fleet.ErrTokenExpired, FleetReasonSessionExpired, true},
		{fleet.ErrNotSignedIn, FleetReasonSessionExpired, true},
		{context.DeadlineExceeded, FleetReasonNetwork, false},
		{fleet.ErrFleetAPINotRouted, FleetReasonServerError, false},
	}
	for _, c := range cases {
		reason, expired := classifyEnrollError(c.err)
		if reason != c.reason || expired != c.expired {
			t.Errorf("classify(%v) = (%q,%v), want (%q,%v)", c.err, reason, expired, c.reason, c.expired)
		}
	}
}

func TestEnrollBackoff_BoundedExponential(t *testing.T) {
	prev := enrollBackoff(1)
	for i := 2; i < 20; i++ {
		d := enrollBackoff(i)
		if d < prev {
			t.Fatalf("backoff not monotonic at %d: %v < %v", i, d, prev)
		}
		prev = d
	}
	if prev > 30*60*1e9 {
		t.Fatalf("backoff unbounded: %v", prev)
	}
}
