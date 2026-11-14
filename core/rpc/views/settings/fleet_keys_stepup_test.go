package settings

// fleet_keys_stepup_test.go — kenaz-fleet ER-40 (step-up on device-key
// replacement), settings half: the "Sign in again" action.
//
//	403 reauth_required → snapshot shows reauth_required → user signs in
//	again (max_age=0 flow, injected) → ONE retry with X-Kenaz-Step-Up-Token.
//	Cancel keeps the state visible; a second refusal never loops; the token
//	is gone afterwards; a fresh regular sign-in's ID token covers its enroll.
//
// A real fleet.Client talks to the fake Fleet below; tokens come from the
// external token source (no keychain). The browser flows are injected.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type keysStepUpReq struct {
	Method, Path, StepUp string
}

// keysStepUpFleet enforces FLEET_NODE_KEYS_STEP_UP=enforce: every key call
// counts as a replacement and is satisfied only by acceptToken. Race-safe.
type keysStepUpFleet struct {
	srv *httptest.Server

	mu     sync.Mutex
	reqs   []keysStepUpReq
	accept string
}

func newKeysStepUpFleet(t *testing.T) *keysStepUpFleet {
	t.Helper()
	f := &keysStepUpFleet{}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	handle := func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get(fleet.StepUpHeader)
		f.mu.Lock()
		f.reqs = append(f.reqs, keysStepUpReq{Method: r.Method, Path: r.URL.Path, StepUp: hdr})
		ok := hdr != "" && hdr == f.accept
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/enroll" {
			out := map[string]any{"org_id": "org-uuid", "team_id": "t1", "role": "org_member", "tier": "team"}
			if !ok {
				out["keys_reauth_required"] = true
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_user_authentication", max_age="600"`)
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "reauth_required", "message": "Sign in again to change this device's keys, then retry.",
				"details": map[string]any{"max_age_seconds": 600, "step_up_header": fleet.StepUpHeader},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
	}
	mux.HandleFunc("/api/v1/enroll", handle)
	mux.HandleFunc("/api/v1/me/nodes/", handle)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *keysStepUpFleet) setAccept(tok string) { f.mu.Lock(); f.accept = tok; f.mu.Unlock() }
func (f *keysStepUpFleet) snapshot() []keysStepUpReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]keysStepUpReq(nil), f.reqs...)
}

// puts returns the PUT keys requests only.
func (f *keysStepUpFleet) puts() []keysStepUpReq {
	var out []keysStepUpReq
	for _, r := range f.snapshot() {
		if r.Method == http.MethodPut {
			out = append(out, r)
		}
	}
	return out
}

func idTokenFor(sub string, authTime time.Time) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(map[string]any{
		"sub": sub, "azp": "test", "amr": []string{"pwd"},
		"auth_time": authTime.Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}) + ".sig"
}

type keysStepUpRig struct {
	api     *API
	fleet   *keysStepUpFleet
	client  *fleet.Client
	dataDir string
	broker  *fakeSessionBroker
	nodeID  string

	tokMu sync.Mutex
	tok   string
}

// signInAs switches the access token's account (the external token source).
func (r *keysStepUpRig) signInAs(sub string) {
	r.tokMu.Lock()
	r.tok = jwtFor(sub, "zitadel-org-1")
	r.tokMu.Unlock()
}

func newKeysStepUpRig(t *testing.T) *keysStepUpRig {
	t.Helper() // NOT t.Parallel: SetExternalTokenSource is process-global
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	t.Setenv("KENAZ_HARNESS_ENV", "local") // a configured profile
	r := &keysStepUpRig{fleet: newKeysStepUpFleet(t), dataDir: t.TempDir(), broker: &fakeSessionBroker{}}
	r.signInAs("user-1")
	fleet.SetExternalTokenSource(func() string { r.tokMu.Lock(); defer r.tokMu.Unlock(); return r.tok })
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	if err := fleet.StoreContextSeed(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	r.client = fleet.NewClientForTestingWithDataDir(r.fleet.srv.URL, r.dataDir)
	r.api = &API{}
	r.api.SetFleetClient(r.client, r.dataDir)
	r.api.SetLockdownBroker(r.broker)
	t.Cleanup(r.api.StopFleetBackground)
	id, err := fleet.NodeID(r.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	r.nodeID = id
	return r
}

func (r *keysStepUpRig) setFlow(fn func(context.Context, fleet.EnvProfile) (string, error)) {
	r.api.fleet.mu.Lock()
	r.api.fleet.stepUpFlow = fn
	r.api.fleet.mu.Unlock()
}

// refuse puts the device into reauth_required the way a recovery-code
// import does (PUT keys refused).
func (r *keysStepUpRig) refuse(t *testing.T) {
	t.Helper()
	if err := r.client.RegisterDeviceKeys(context.Background(), r.nodeID); !errors.Is(err, fleet.ErrKeysReauthRequired) {
		t.Fatalf("setup PUT err = %v, want ErrKeysReauthRequired", err)
	}
	dk := snap(t, r.api).DeviceKeys
	if dk == nil || dk.Status != fleet.KeyRegReauthRequired || !strings.Contains(dk.Message, "Sign in again") {
		t.Fatalf("snapshot deviceKeys = %+v, want reauth_required with sign-in copy", dk)
	}
}

// 403 reauth_required → prompt → fresh sign-in → retry carries the token
// and succeeds; the snapshot flips to registered and is pushed.
func TestFleetDeviceKeysReauth_RetrySucceeds(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)

	tok := idTokenFor("user-1", time.Now())
	r.fleet.setAccept(tok)
	var flows atomic.Int32
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) {
		flows.Add(1)
		return tok, nil
	})
	if err := r.api.FleetDeviceKeysReauth(context.Background()); err != nil {
		t.Fatalf("FleetDeviceKeysReauth: %v", err)
	}
	if n := flows.Load(); n != 1 {
		t.Fatalf("step-up flows = %d, want 1", n)
	}
	puts := r.fleet.puts()
	if len(puts) != 2 || puts[0].StepUp != "" || puts[1].StepUp != tok {
		t.Fatalf("PUTs = %+v, want the refused one then ONE retry carrying the step-up token", puts)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegRegistered {
		t.Fatalf("after retry deviceKeys = %+v, want registered", dk)
	}
	if ev := last(r.broker.sessionEvents()); ev == nil || ev.DeviceKeys == nil || ev.DeviceKeys.Status != fleet.KeyRegRegistered {
		t.Fatalf("no session push with the registered state: %+v", ev)
	}

	// The token is gone: the next key call carries no header.
	_ = r.client.RegisterDeviceKeys(context.Background(), r.nodeID)
	puts = r.fleet.puts()
	if got := puts[len(puts)-1].StepUp; got != "" {
		t.Fatal("the step-up token outlived its retry")
	}
}

// Fleet still refuses after the fresh sign-in → error, ONE retry, no second
// browser flow, state stays visible.
func TestFleetDeviceKeysReauth_StillRefused_NoLoop(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)
	r.fleet.setAccept("a-different-token")
	var flows atomic.Int32
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) {
		flows.Add(1)
		return idTokenFor("user-1", time.Now()), nil
	})
	err := r.api.FleetDeviceKeysReauth(context.Background())
	if !errors.Is(err, fleet.ErrStepUpRefused) {
		t.Fatalf("err = %v, want ErrStepUpRefused", err)
	}
	if n := flows.Load(); n != 1 {
		t.Fatalf("browser flows = %d, want exactly 1", n)
	}
	if n := len(r.fleet.puts()); n != 2 {
		t.Fatalf("PUTs = %d, want 2 (refused + one retry)", n)
	}
	dk := snap(t, r.api).DeviceKeys
	if dk == nil || dk.Status != fleet.KeyRegReauthRefused || dk.Message != "Your organization's sign-in can't confirm this change yet. Contact your administrator." {
		t.Fatalf("deviceKeys = %+v, want reauth_refused with the admin copy", dk)
	}

	// Survives a following tokenless enroll (app start / identity refresh).
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegReauthRefused {
		t.Fatalf("a tokenless enroll downgraded the refused state: %+v", dk)
	}

	// Cleared on a successful registration (e.g. once Fleet's setting is on).
	tok := idTokenFor("user-1", time.Now())
	r.fleet.setAccept(tok)
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) { return tok, nil })
	if err := r.api.FleetDeviceKeysReauth(context.Background()); err != nil {
		t.Fatalf("retry after the fix: %v", err)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegRegistered {
		t.Fatalf("deviceKeys = %+v, want registered", dk)
	}
}

// reauth_refused belongs to the account that was refused: after signing in
// as someone else it is gone, and that account starts from its own state.
func TestFleetDeviceKeysReauth_Refused_ClearedOnAccountSwitch(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)
	r.fleet.setAccept("a-different-token")
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) {
		return idTokenFor("user-1", time.Now()), nil
	})
	if err := r.api.FleetDeviceKeysReauth(context.Background()); !errors.Is(err, fleet.ErrStepUpRefused) {
		t.Fatalf("err = %v", err)
	}
	r.signInAs("user-2")
	if dk := snap(t, r.api).DeviceKeys; dk != nil {
		t.Fatalf("another account's refusal leaked to the new account: %+v", dk)
	}
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegReauthRequired {
		t.Fatalf("new account deviceKeys = %+v, want its own reauth_required", dk)
	}
}

// An IdP that ignored the fresh-sign-in request (old auth_time) is refused
// locally with the admin copy; nothing is sent.
func TestFleetDeviceKeysReauth_StaleSignIn_RefusedLocally(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) {
		return idTokenFor("user-1", time.Now().Add(-30*time.Minute)), nil
	})
	if err := r.api.FleetDeviceKeysReauth(context.Background()); !errors.Is(err, fleet.ErrStepUpRefused) {
		t.Fatalf("err = %v, want ErrStepUpRefused", err)
	}
	if n := len(r.fleet.puts()); n != 1 {
		t.Fatalf("PUTs = %d, want only the original", n)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegReauthRefused {
		t.Fatalf("deviceKeys = %+v, want reauth_refused", dk)
	}
}

// Cancel (the UI's Cancel = FleetSignInCancel) ends the flow with
// context.Canceled; no key call; reauth_required stays on the snapshot.
func TestFleetDeviceKeysReauth_Cancel_KeepsStateVisible(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)
	started := make(chan struct{})
	r.setFlow(func(ctx context.Context, _ fleet.EnvProfile) (string, error) {
		close(started)
		<-ctx.Done() // the user never finishes in the browser
		return "", ctx.Err()
	})
	errCh := make(chan error, 1)
	go func() { errCh <- r.api.FleetDeviceKeysReauth(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("step-up flow never started")
	}
	if err := r.api.FleetSignInCancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the step-up")
	}
	if n := len(r.fleet.puts()); n != 1 {
		t.Fatalf("PUTs = %d, want only the original refused one", n)
	}
	v := snap(t, r.api)
	if v.DeviceKeys == nil || v.DeviceKeys.Status != fleet.KeyRegReauthRequired {
		t.Fatalf("deviceKeys = %+v, want reauth_required after cancel", v.DeviceKeys)
	}
	if v.State != FleetSessionSignedIn && v.State != FleetSessionDegraded {
		// The session itself is untouched by a cancelled step-up.
		t.Fatalf("session state = %q after a cancelled step-up", v.State)
	}
}

// A different account in the browser is refused locally with a clear error
// and nothing is sent.
func TestFleetDeviceKeysReauth_WrongAccount(t *testing.T) {
	r := newKeysStepUpRig(t)
	r.refuse(t)
	r.setFlow(func(context.Context, fleet.EnvProfile) (string, error) {
		return idTokenFor("someone-else", time.Now()), nil
	})
	if err := r.api.FleetDeviceKeysReauth(context.Background()); !errors.Is(err, fleet.ErrStepUpWrongAccount) {
		t.Fatalf("err = %v, want ErrStepUpWrongAccount", err)
	}
	if n := len(r.fleet.puts()); n != 1 {
		t.Fatalf("PUTs = %d, want no retry", n)
	}
}

// A regular sign-in whose ID token is fresh covers the enroll that follows
// (no second prompt), and the token is dropped afterwards.
func TestFleetSignIn_FreshIDToken_CoversEnrollKeyReplacement(t *testing.T) {
	r := newKeysStepUpRig(t)
	tok := idTokenFor("user-1", time.Now())
	r.fleet.setAccept(tok)
	r.api.fleet.mu.Lock()
	r.api.fleet.signInFlow = func(context.Context, fleet.EnvProfile) (fleet.TokenSet, error) {
		return fleet.TokenSet{AccessToken: jwtFor("user-1", "zitadel-org-1"), IDToken: tok, ExpiresAt: time.Now().Add(time.Hour)}, nil
	}
	r.api.fleet.mu.Unlock()
	if _, err := r.api.FleetSignIn(context.Background()); err != nil {
		t.Fatalf("FleetSignIn: %v", err)
	}
	var enrolls []keysStepUpReq
	for _, q := range r.fleet.snapshot() {
		if q.Path == "/api/v1/enroll" {
			enrolls = append(enrolls, q)
		}
	}
	if len(enrolls) == 0 || enrolls[0].StepUp != tok {
		t.Fatalf("sign-in enroll must carry the fresh ID token as step-up: %+v", enrolls)
	}
	if dk := snap(t, r.api).DeviceKeys; dk == nil || dk.Status != fleet.KeyRegRegistered {
		t.Fatalf("deviceKeys = %+v, want registered", dk)
	}
	// Dropped after the sign-in's enroll.
	_ = r.client.RegisterDeviceKeys(context.Background(), r.nodeID)
	puts := r.fleet.puts()
	if len(puts) == 0 || puts[len(puts)-1].StepUp != "" {
		t.Fatal("the sign-in ID token outlived the sign-in enroll")
	}
}
