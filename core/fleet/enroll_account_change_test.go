package fleet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The reviewer's reproduction (legal D164): an enroll is running under A's token
// when a sign-in saves B's. The sign-in must send its own enroll instead of
// joining A's and taking its answer, and A's late answer must not overwrite B's
// identity.
func TestEnroll_SignInDuringARunningEnrollGetsItsOwnAnswer(t *testing.T) {
	aStarted, aRelease := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	bearers := map[string]int{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api_base_url":"` + srv.URL + `"}`))
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		bearers[bearer]++
		mu.Unlock()
		user := "user-b"
		if bearer == testJWT("sub-a") {
			user = "user-a"
			close(aStarted)
			<-aRelease
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":"` + user + `","org_id":"org-1","role":"org_member"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := NewClientForTestingWithDataDir(srv.URL, dir)
	saveTestTokens(t, "sub-a")

	ctx := context.Background()
	aErr := make(chan error, 1)
	go func() {
		_, err := c.RefreshIdentity(ctx, "node-1", "darwin", "test")
		aErr <- err
	}()
	select {
	case <-aStarted:
	case err := <-aErr:
		close(aRelease)
		t.Fatalf("A's enroll never reached Fleet: %v", err)
	}

	saveTestTokens(t, "sub-b")
	id, err := c.RefreshIdentity(ctx, "node-1", "darwin", "test")
	if err != nil {
		t.Fatalf("sign-in's enroll: %v", err)
	}
	if id.UserID != "user-b" {
		t.Fatalf("sign-in got %q, the previous account's answer", id.UserID)
	}

	close(aRelease)
	if err := <-aErr; !errors.Is(err, ErrEnrollTokenChanged) {
		t.Fatalf("A's late enroll err = %v", err)
	}
	saved, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.UserID != "user-b" || saved.TokenKey != TokenIdentityKey() {
		t.Fatalf("identity after A's late answer = %+v", saved)
	}
	mu.Lock()
	defer mu.Unlock()
	if bearers[testJWT("sub-b")] != 1 {
		t.Fatalf("B's token enrolled %d times", bearers[testJWT("sub-b")])
	}
}
