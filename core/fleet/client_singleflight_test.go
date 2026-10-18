package fleet_test

// Regression test for finding #98 (2026-09-14, enrollment storm).
//
// Production logs showed ~5000 POST /api/v1/enroll round trips in 2 hours
// from a single running install — roughly 10x what UserMenu.vue's (now
// widened) background poll alone would produce. The excess came from
// multiple independent, uncoordinated frontend call sites
// (UserMenu.vue's poll, AccountPanel.vue's mount + manual-refresh button,
// CedarEditor.vue's mount) that can all invoke fleetRefreshIdentity()
// around the same moment with zero shared cache or in-flight
// de-duplication — unlike CapabilityPoller.Refresh, which already
// collapses concurrent callers via singleflight (capability_poller.go).
//
// This test pins RefreshIdentity's singleflight wrapper (client.go): N
// concurrent callers must produce exactly one HTTP request, and all
// callers must observe the same result.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshIdentity_ConcurrentCallers_CollapseToOneRequest(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&callCount, 1)
		// Widen the in-flight window so concurrently-launched callers
		// reliably overlap with this handler execution instead of racing
		// past it one at a time.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"org_id": 7, "team_id": "team-1", "org_name": "Acme", "role": "member"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)

	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)
	ready := make(chan struct{})
	errs := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-ready
			id, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.79.0")
			if err != nil {
				errs <- err
				return
			}
			if id.OrgID != "7" {
				t.Errorf("RefreshIdentity: OrgID = %q, want \"7\"", id.OrgID)
			}
		}()
	}
	close(ready) // release all goroutines at once
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("RefreshIdentity: %v", err)
	}

	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Fatalf("POST /api/v1/enroll called %d times for %d concurrent RefreshIdentity callers, want 1 (singleflight should collapse them into a single round trip)", got, concurrency)
	}
}

// TestRefreshIdentity_SequentialCallers_EachIssuesARequest guards against
// an over-broad fix: singleflight must only collapse calls that are
// actually concurrent. A caller that starts after the previous call has
// already completed (the normal poll-tick case) must still get a fresh
// network round trip — otherwise a real tier change would never be
// observed.
func TestRefreshIdentity_SequentialCallers_EachIssuesARequest(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"org_id": 7, "team_id": "team-1"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)

	for i := 0; i < 3; i++ {
		if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.79.0"); err != nil {
			t.Fatalf("RefreshIdentity call %d: %v", i, err)
		}
	}

	if got := atomic.LoadInt32(&callCount); got != 3 {
		t.Fatalf("POST /api/v1/enroll called %d times for 3 sequential RefreshIdentity callers, want 3", got)
	}
}
