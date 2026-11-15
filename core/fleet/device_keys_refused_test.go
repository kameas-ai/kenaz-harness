package fleet

import (
	"errors"
	"os"
	"testing"
	"time"
)

// restartClient is a fresh Client (a new process) over the same data dir.
func restartClient(t *testing.T, f *stepUpFleet, dataDir string) *Client {
	t.Helper()
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = dataDir
	return c
}

// refuseLocally records reauth_refused for user-1 (a stale step-up token).
func refuseLocally(t *testing.T, c *Client) {
	t.Helper()
	if err := c.HoldStepUpToken(stepUpIDToken(t, "user-1", time.Now().Add(-11*time.Minute))); !errors.Is(err, ErrStepUpRefused) {
		t.Fatalf("err = %v, want ErrStepUpRefused", err)
	}
}

// After a restart, the first tokenless enroll must not downgrade the refusal
// to plain reauth_required.
func TestReauthRefused_SurvivesRestart(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	refuseLocally(t, c)
	info, err := os.Stat(reauthRefusedPath(c.dataDir))
	if err != nil {
		t.Fatalf("refusal not persisted: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("refusal file mode = %o, want 600", perm)
	}

	c2 := restartClient(t, f, c.dataDir)
	if kr := c2.KeyRegistration(); kr.Status != KeyRegReauthRefused {
		t.Fatalf("after restart state = %+v, want reauth_refused", kr)
	}
	if _, err := c2.RefreshIdentity(t.Context(), "node-1", "darwin", "0.99.0"); err != nil {
		t.Fatal(err)
	}
	if kr := c2.KeyRegistration(); kr.Status != KeyRegReauthRefused {
		t.Fatalf("tokenless enroll after restart downgraded the refusal: %+v", kr)
	}

	// Fresh client whose FIRST call is the enroll (no KeyRegistration read
	// beforehand): the sticky check itself must consult the file.
	c3 := restartClient(t, f, c.dataDir)
	if _, err := c3.RefreshIdentity(t.Context(), "node-1", "darwin", "0.99.0"); err != nil {
		t.Fatal(err)
	}
	if kr := c3.KeyRegistration(); kr.Status != KeyRegReauthRefused {
		t.Fatalf("first-call enroll after restart downgraded the refusal: %+v", kr)
	}
}

func TestReauthRefused_ClearedOnSuccess(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	refuseLocally(t, c)

	c2 := restartClient(t, f, c.dataDir)
	tok := stepUpIDToken(t, "user-1", time.Now())
	f.accept(tok)
	if err := c2.HoldStepUpToken(tok); err != nil {
		t.Fatal(err)
	}
	if err := c2.RegisterDeviceKeys(t.Context(), "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reauthRefusedPath(c.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("refusal file survived a successful registration: %v", err)
	}
	if kr := restartClient(t, f, c.dataDir).KeyRegistration(); kr.Status != "" {
		t.Fatalf("after success + restart state = %+v, want none", kr)
	}
}

func TestReauthRefused_IgnoredForAnotherAccount(t *testing.T) {
	f := newStepUpFleet(t)
	c := stepUpClient(t, f)
	refuseLocally(t, c)

	stubTokens(t, TokenSet{AccessToken: stepUpJWT(t, map[string]any{"sub": "user-2", "exp": time.Now().Add(time.Hour).Unix()}),
		RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	c2 := restartClient(t, f, c.dataDir)
	if kr := c2.KeyRegistration(); kr.Status != "" {
		t.Fatalf("another account's refusal leaked after restart: %+v", kr)
	}
	if err := c2.RegisterDeviceKeys(t.Context(), "node-1"); !errors.Is(err, ErrKeysReauthRequired) || errors.Is(err, ErrStepUpRefused) {
		t.Fatalf("err = %v", err)
	}
	if kr := c2.KeyRegistration(); kr.Status != KeyRegReauthRequired {
		t.Fatalf("new account state = %+v, want plain reauth_required", kr)
	}
	if _, err := os.Stat(reauthRefusedPath(c.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("another account's refusal file was kept: %v", err)
	}
}
