package fleet

// device_keys_stepup_test.go — kenaz-fleet ER-40: step-up on device-key
// replacement (contract §10.1 "Device-key changes in ADR 0001 phase 0").
//
// The fake fleet answers with the exact shapes kenaz-fleet writes
// (service/device_keys_stepup.go writeReauthRequired, api_types.go
// EnrollResponse.KeysReauthRequired):
//
//	PUT keys, replacement without a fresh sign-in:
//	  403 {"code":"reauth_required","message":"Sign in again to change this device's keys, then retry.",
//	       "details":{"max_age_seconds":600,"step_up_header":"X-Kenaz-Step-Up-Token"}}
//	  WWW-Authenticate: Bearer error="insufficient_user_authentication", ..., max_age="600"
//	POST /enroll, same case: 200 {..., "keys_reauth_required": true}
//
// It accepts exactly one step-up token value (acceptToken) and records every
// request's header under a mutex (race-safe: tests read via snapshot()).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type stepUpReq struct {
	Method string
	Path   string
	StepUp string
	Body   map[string]any
}

type stepUpFleet struct {
	srv *httptest.Server

	mu          sync.Mutex
	reqs        []stepUpReq
	enforce     bool   // replacement gated
	acceptToken string // the one step-up token fleet accepts ("" = none)
}

func newStepUpFleet(t *testing.T) *stepUpFleet {
	t.Helper()
	f := &stepUpFleet{enforce: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		hdr := r.Header.Get(StepUpHeader)
		f.mu.Lock()
		f.reqs = append(f.reqs, stepUpReq{Method: r.Method, Path: r.URL.Path, StepUp: hdr, Body: body})
		enforce, accept := f.enforce, f.acceptToken
		f.mu.Unlock()
		satisfied := !enforce || (hdr != "" && hdr == accept)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/enroll":
			resp := map[string]any{"org_id": "org-1", "team_id": "t1", "role": "org_member", "tier": "team", "user_id": "u-1"}
			if !satisfied {
				resp["keys_reauth_required"] = true
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/me/nodes/"):
			if !satisfied {
				w.Header().Set("WWW-Authenticate",
					`Bearer error="insufficient_user_authentication", error_description="recent sign-in required to replace device keys", max_age="600"`)
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": "reauth_required", "message": "Sign in again to change this device's keys, then retry.",
					"details": map[string]any{"max_age_seconds": 600, "step_up_header": StepUpHeader},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{
				{"purpose": "handoff", "fingerprint": "sha256:aa", "created_at": time.Now().UTC()},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stepUpFleet) accept(tok string) {
	f.mu.Lock()
	f.acceptToken = tok
	f.mu.Unlock()
}

func (f *stepUpFleet) snapshot() []stepUpReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]stepUpReq(nil), f.reqs...)
}

// stepUpJWT builds an unsigned three-part JWT with the given claims (the
// harness never verifies ID tokens; Fleet does).
func stepUpJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(b) + ".c2ln"
}

func stepUpIDToken(t *testing.T, sub string, authTime time.Time) string {
	return stepUpJWT(t, map[string]any{
		"sub": sub, "azp": "test", "aud": []string{"test"}, "amr": []string{"pwd"},
		"auth_time": authTime.Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
}

// stepUpClient is a signed-in client whose access token's sub is "user-1".
func stepUpClient(t *testing.T, f *stepUpFleet) *Client {
	t.Helper()
	at := stepUpJWT(t, map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()})
	stubTokens(t, TokenSet{AccessToken: at, RefreshToken: "rt-stepup", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = t.TempDir()
	if err := StoreContextSeed(seq32()); err != nil {
		t.Fatal(err)
	}
	return c
}

// 403 reauth_required on PUT → typed error, recoverable visible state, ONE
// request (it is a 403, never refresh-and-retry).
func TestRegisterDeviceKeys_ReauthRequired_NoRetryLoop(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)

	err := c.RegisterDeviceKeys(t.Context(), "node-1")
	if !errors.Is(err, ErrKeysReauthRequired) {
		t.Fatalf("err = %v, want ErrKeysReauthRequired", err)
	}
	reqs := f.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want exactly 1 (no refresh / retry on 403)", len(reqs))
	}
	if reqs[0].StepUp != "" {
		t.Fatalf("no token held, yet %s was sent", StepUpHeader)
	}
	kr := c.KeyRegistration()
	if kr.Status != KeyRegReauthRequired || !strings.Contains(kr.Message, "Sign in again") {
		t.Fatalf("key registration = %+v, want reauth_required with sign-in copy", kr)
	}
	if kr.HandoffFingerprint != "" {
		t.Fatalf("an unregistered key must not show a fingerprint: %+v", kr)
	}
}

// Header sent on replace: with a held step-up token the PUT carries it and
// succeeds; the state flips to registered.
func TestRegisterDeviceKeys_HeldStepUpToken_SentAndAccepted(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)

	// 1. refused without a token.
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); !errors.Is(err, ErrKeysReauthRequired) {
		t.Fatalf("first PUT err = %v", err)
	}
	// 2. the user signs in again → token held → retry succeeds.
	tok := stepUpIDToken(t, "user-1", time.Now())
	f.accept(tok)
	if err := c.HoldStepUpToken(tok); err != nil {
		t.Fatalf("HoldStepUpToken: %v", err)
	}
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); err != nil {
		t.Fatalf("retry PUT: %v", err)
	}
	reqs := f.snapshot()
	if len(reqs) != 2 || reqs[1].StepUp != tok {
		t.Fatalf("retry must carry %s; requests=%+v", StepUpHeader, reqs)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegRegistered || kr.HandoffFingerprint == "" {
		t.Fatalf("after retry key registration = %+v, want registered", kr)
	}
}

// A token that was sent and STILL refused is dropped so nothing can retry
// with it; the error says the new sign-in was not accepted.
func TestRegisterDeviceKeys_RefusedToken_IsDropped(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	f.accept("some-other-token")
	if err := c.HoldStepUpToken(stepUpIDToken(t, "user-1", time.Now())); err != nil {
		t.Fatal(err)
	}
	err := c.RegisterDeviceKeys(t.Context(), "node-1")
	if !errors.Is(err, ErrStepUpRefused) || !errors.Is(err, ErrKeysReauthRequired) {
		t.Fatalf("err = %v, want ErrStepUpRefused (wrapping ErrKeysReauthRequired)", err)
	}
	if c.heldStepUpToken() != "" {
		t.Fatal("a refused step-up token must be dropped")
	}
	kr := c.KeyRegistration()
	if kr.Status != KeyRegReauthRefused || !strings.Contains(kr.Message, "Contact your administrator") {
		t.Fatalf("state = %+v, want reauth_refused with the admin copy", kr)
	}
	// Sticky: a later tokenless refusal (app start, identity refresh)
	// does not downgrade it to plain reauth_required.
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.99.0"); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); !errors.Is(err, ErrKeysReauthRequired) {
		t.Fatal(err)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegReauthRefused {
		t.Fatalf("tokenless calls downgraded the refused state: %+v", kr)
	}
	// Signed in as someone else: the refusal no longer applies.
	stubTokens(t, TokenSet{AccessToken: stepUpJWT(t, map[string]any{"sub": "user-2"}), RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	if kr := c.KeyRegistration(); kr.Status != "" {
		t.Fatalf("another account's refusal leaked: %+v", kr)
	}
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); !errors.Is(err, ErrKeysReauthRequired) || errors.Is(err, ErrStepUpRefused) {
		t.Fatalf("err = %v", err)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegReauthRequired {
		t.Fatalf("new account state = %+v, want plain reauth_required", kr)
	}
}

// An IdP that ignored prompt=login / max_age=0 returns an old auth_time:
// refused locally (bound to fail), nothing sent, admin copy shown.
func TestHoldStepUpToken_StaleAuthTime_RefusedLocally(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	for name, tok := range map[string]string{
		"stale":        stepUpIDToken(t, "user-1", time.Now().Add(-11*time.Minute)),
		"no auth_time": stepUpJWT(t, map[string]any{"sub": "user-1", "exp": time.Now().Add(time.Hour).Unix()}),
	} {
		if err := c.HoldStepUpToken(tok); !errors.Is(err, ErrStepUpRefused) {
			t.Fatalf("%s: err = %v, want ErrStepUpRefused", name, err)
		}
		if c.heldStepUpToken() != "" {
			t.Fatalf("%s: token held", name)
		}
		if kr := c.KeyRegistration(); kr.Status != KeyRegReauthRefused {
			t.Fatalf("%s: state = %+v", name, kr)
		}
	}
	if n := len(f.snapshot()); n != 0 {
		t.Fatalf("requests = %d, want none", n)
	}
	// A successful registration clears it.
	tok := stepUpIDToken(t, "user-1", time.Now())
	f.accept(tok)
	if err := c.HoldStepUpToken(tok); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); err != nil {
		t.Fatal(err)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegRegistered {
		t.Fatalf("state after success = %+v", kr)
	}
}

// The regular sign-in flow is unchanged: no max_age / prompt, and it keeps
// offline_access (refresh tokens).
func TestDeviceCodeFlow_AuthorizeURL_NoStepUpParams(t *testing.T) {
	urls := make(chan string, 1)
	prev := openBrowserFn
	openBrowserFn = func(raw string) error { urls <- raw; return nil } // never a real browser
	t.Cleanup(func() { openBrowserFn = prev })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := DeviceCodeFlow(ctx, EnvProfile{
			Name: EnvLocal, ZitadelIssuer: "http://127.0.0.1:1", NativeClientID: "test",
			FleetBaseURL: "http://127.0.0.1:1", OIDCScopes: DefaultOIDCScopes,
		})
		done <- err
	}()
	var raw string
	select {
	case raw = <-urls:
	case <-time.After(5 * time.Second):
		t.Fatal("browser never opened")
	}
	cancel()
	<-done
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if _, ok := q["max_age"]; ok {
		t.Fatal("sign-in must not send max_age")
	}
	if _, ok := q["prompt"]; ok {
		t.Fatal("sign-in must not send prompt")
	}
	if !strings.Contains(" "+q.Get("scope")+" ", " offline_access ") {
		t.Fatalf("sign-in must keep offline_access: scope=%q", q.Get("scope"))
	}
}

// Enroll under enforce: 200 + keys_reauth_required → enroll succeeds
// (identity returned), state is reauth_required; a held token is sent.
func TestEnroll_KeysReauthRequired_PartialSuccess(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)

	id, err := c.RefreshIdentity(t.Context(), "node-E", "darwin", "0.99.0")
	if err != nil {
		t.Fatalf("enroll must succeed with keys_reauth_required: %v", err)
	}
	if id.OrgID != "org-1" {
		t.Fatalf("identity = %+v", id)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegReauthRequired || kr.HandoffFingerprint != "" {
		t.Fatalf("key registration = %+v, want reauth_required", kr)
	}
	if n := len(f.snapshot()); n != 1 {
		t.Fatalf("enroll requests = %d, want 1 (no retry)", n)
	}

	tok := stepUpIDToken(t, "user-1", time.Now())
	f.accept(tok)
	if err := c.HoldStepUpToken(tok); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RefreshIdentity(t.Context(), "node-E", "darwin", "0.99.0"); err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
	reqs := f.snapshot()
	if reqs[len(reqs)-1].StepUp != tok {
		t.Fatalf("enroll with a held token must send %s", StepUpHeader)
	}
	if kr := c.KeyRegistration(); kr.Status != KeyRegRegistered {
		t.Fatalf("after step-up enroll key registration = %+v", kr)
	}
}

func TestHoldStepUpToken_RejectsOtherAccountAndGarbage(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	if err := c.HoldStepUpToken(stepUpIDToken(t, "someone-else", time.Now())); !errors.Is(err, ErrStepUpWrongAccount) {
		t.Fatalf("other account: err = %v, want ErrStepUpWrongAccount", err)
	}
	if err := c.HoldStepUpToken("not-a-jwt"); !errors.Is(err, ErrStepUpNoIDToken) {
		t.Fatalf("garbage: err = %v", err)
	}
	if c.heldStepUpToken() != "" {
		t.Fatal("nothing should be held")
	}
}

// The held token expires with Fleet's freshness window.
func TestStepUpToken_ExpiresWithFreshnessWindow(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	var mu sync.Mutex
	now := time.Now()
	c.stepUp.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	if err := c.HoldStepUpToken(stepUpIDToken(t, "user-1", now.Add(-2*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if c.heldStepUpToken() == "" {
		t.Fatal("fresh token should be held")
	}
	mu.Lock()
	now = now.Add(9 * time.Minute) // auth_time + 11 min
	mu.Unlock()
	if c.heldStepUpToken() != "" {
		t.Fatal("token must be dropped once auth_time is older than the window")
	}
}

// A regular sign-in's ID token counts as a step-up only with a fresh
// auth_time (an SSO-session reuse is ignored).
func TestOfferSignInIDToken_OnlyWhenFresh(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	c.OfferSignInIDToken(stepUpIDToken(t, "user-1", time.Now().Add(-3*time.Hour)))
	if c.heldStepUpToken() != "" {
		t.Fatal("stale sign-in token must not be held")
	}
	c.OfferSignInIDToken(stepUpJWT(t, map[string]any{"sub": "user-1"})) // no auth_time
	if c.heldStepUpToken() != "" {
		t.Fatal("token without auth_time must not be held")
	}
	fresh := stepUpIDToken(t, "user-1", time.Now())
	c.OfferSignInIDToken(fresh)
	if c.heldStepUpToken() != fresh {
		t.Fatal("fresh sign-in token should be held")
	}
	c.ClearStepUpToken()
	if c.heldStepUpToken() != "" {
		t.Fatal("ClearStepUpToken must drop it")
	}
}

// The step-up token is never persisted: not in the token store and not in
// any file under the data dir.
func TestStepUpToken_NeverPersisted(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	before, err := LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	tok := stepUpIDToken(t, "user-1", time.Now())
	f.accept(tok)
	if err := c.HoldStepUpToken(tok); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterDeviceKeys(t.Context(), "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.99.0"); err != nil {
		t.Fatal(err)
	}
	after, err := LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if after.AccessToken != before.AccessToken || after.RefreshToken != before.RefreshToken || after.IDToken != "" {
		t.Fatal("the token store changed during a step-up")
	}
	for _, v := range []string{after.AccessToken, after.RefreshToken} {
		if v == tok {
			t.Fatal("step-up token found in the token store")
		}
	}
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), tok) {
			t.Fatalf("step-up token persisted to %s", filepath.Base(p))
		}
		return nil
	})
}

// StepUpFlow asks for a fresh authentication (max_age=0, prompt=login), mints
// no refresh token (no offline_access), returns only the ID token and saves
// nothing.
func TestStepUpFlow_FreshAuthParams_ReturnsIDTokenOnly(t *testing.T) {
	idTok := stepUpIDToken(t, "user-1", time.Now())
	var tokenForm url.Values
	var formMu sync.Mutex
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/v2/token" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		formMu.Lock()
		tokenForm = r.PostForm
		formMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "throwaway-at", "id_token": idTok, "expires_in": 3600, "token_type": "Bearer",
		})
	}))
	t.Cleanup(issuer.Close)

	stubTokens(t, TokenSet{AccessToken: "at-existing", RefreshToken: "rt-existing", ExpiresAt: time.Now().Add(time.Hour)})

	urls := make(chan string, 1)
	prev := openBrowserFn
	openBrowserFn = func(raw string) error { urls <- raw; return nil } // never a real browser
	t.Cleanup(func() { openBrowserFn = prev })

	profile := EnvProfile{
		Name: EnvLocal, ZitadelIssuer: issuer.URL, NativeClientID: "test",
		FleetBaseURL: issuer.URL, OIDCScopes: DefaultOIDCScopes,
	}
	type result struct {
		tok string
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := StepUpFlow(context.Background(), profile)
		done <- result{tok, err}
	}()

	var authURL *url.URL
	select {
	case raw := <-urls:
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		authURL = u
	case <-time.After(5 * time.Second):
		t.Fatal("browser never opened")
	}
	q := authURL.Query()
	if q.Get("max_age") != "0" || q.Get("prompt") != "login" {
		t.Fatalf("authorize URL must request a fresh sign-in: max_age=%q prompt=%q", q.Get("max_age"), q.Get("prompt"))
	}
	if q.Get("client_id") != "test" {
		t.Fatalf("client_id = %q, want the same client as the session", q.Get("client_id"))
	}
	scopes := strings.Fields(q.Get("scope"))
	hasOpenID := false
	for _, s := range scopes {
		if s == "offline_access" {
			t.Fatal("step-up must not request offline_access (no refresh token)")
		}
		if s == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		t.Fatal("step-up must request openid (an ID token)")
	}
	// The browser completes: hit the loopback callback.
	cb := q.Get("redirect_uri") + "?code=c0de&state=" + url.QueryEscape(q.Get("state"))
	resp, err := http.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("StepUpFlow: %v", r.err)
		}
		if r.tok != idTok {
			t.Fatal("StepUpFlow must return the ID token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StepUpFlow did not finish")
	}
	formMu.Lock()
	if tokenForm.Get("grant_type") != "authorization_code" || tokenForm.Get("client_id") != "test" {
		t.Fatalf("token exchange form = %v", tokenForm)
	}
	formMu.Unlock()
	if ts, err := LoadTokens(); err != nil || ts.AccessToken != "at-existing" || ts.RefreshToken != "rt-existing" {
		t.Fatal("StepUpFlow must not replace the stored session tokens")
	}
}
