package fleet_test

// Regression test for finding #98 WP2 (2026-09-14, config-cache write race).
//
// core/fleet/config.go's ResolveFleetConfig is reached by every single
// fleet HTTP call — enrollIdentity, CapabilityPoller.fetch,
// ConfigPoller.poll, Watcher.poll, and activateOTLPPipeline's direct
// FleetConfig() call — via Client.APIURL / Client.FleetConfig. This is not
// a rare edge case: core/rpc/views/settings/fleet.go's SetFleetClient
// starts CapabilityPoller and ConfigPoller as two separate goroutines,
// both performing an immediate first fetch when their cache is empty —
// which it always is on process boot, since the in-memory FleetConfig
// cache (configCache) is a fresh package-level map every run. Two
// independent resolvers racing a cold cache happens on every launch, no
// enrollment storm required.
//
// Before the fix, every cache-miss resolution independently called
// saveFleetConfigToDisk, writing to the SAME deterministic tmp path
// (config_<hash>.json.tmp — the hash depends only on spaBaseURL). The
// loser's os.Rename(tmp, path) failed with ENOENT because the winner's
// rename had already consumed the shared tmp file — logged as
// fleet.config.cache.save_failed (106 occurrences against 453 fetches in
// the production log that surfaced this). ResolveFleetConfig swallows that
// error (log-and-continue; see resolveFleetConfigUncached), so the
// regression pin here is at the fetch-count boundary: if concurrent
// resolutions no longer collapse into one, the disk-write race is back,
// whether or not any single run happens to observe the ENOENT.
import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestResolveFleetConfig_ConcurrentCallers_CollapseToOneFetch(t *testing.T) {
	var fetchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config.json" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_base_url": "https://api.example.test"}`))
	}))
	defer srv.Close()

	dataDir := t.TempDir()

	// The config cache is package-global and keyed by server URL. httptest
	// ports are reused within a package run (observed on the Linux CI
	// runner, PR #354): a prior test's still-fresh cache entry for this
	// reused port makes the first Resolve return the DEAD server's config
	// with zero fetches, throwing off this test's fetch count. Start from
	// a clean slate for our key.
	fleet.InvalidateFleetConfig(dataDir, srv.URL)

	const concurrency = 10
	var wg sync.WaitGroup
	wg.Add(concurrency)
	ready := make(chan struct{})
	errs := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			<-ready
			cfg, err := fleet.ResolveFleetConfig(t.Context(), dataDir, srv.URL)
			if err != nil {
				errs <- err
				return
			}
			if cfg.APIBaseURL != "https://api.example.test" {
				t.Errorf("ResolveFleetConfig: APIBaseURL = %q, want https://api.example.test", cfg.APIBaseURL)
			}
		}()
	}
	close(ready) // release all goroutines at once
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ResolveFleetConfig: %v", err)
	}

	if got := atomic.LoadInt32(&fetchCount); got != 1 {
		t.Fatalf("GET /config.json called %d times for %d concurrent ResolveFleetConfig callers, want 1 "+
			"(a count > 1 means the disk-cache write race — config.cache.save_failed — is back: "+
			"each caller independently writes the same deterministic tmp path)", got, concurrency)
	}
}

// TestResolveFleetConfig_SequentialCallers_RefetchesAfterInvalidate guards
// against an over-broad fix: singleflight must only collapse calls that
// are actually concurrent / cache-fresh. A caller after an explicit
// InvalidateFleetConfig (e.g. following an observed config-drift symptom)
// must still get a real network round trip.
func TestResolveFleetConfig_SequentialCallers_RefetchesAfterInvalidate(t *testing.T) {
	var fetchCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config.json" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&fetchCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_base_url": "https://api.example.test"}`))
	}))
	defer srv.Close()

	dataDir := t.TempDir()

	// The config cache is package-global and keyed by server URL. httptest
	// ports are reused within a package run (observed on the Linux CI
	// runner, PR #354): a prior test's still-fresh cache entry for this
	// reused port makes the first Resolve return the DEAD server's config
	// with zero fetches, throwing off this test's fetch count. Start from
	// a clean slate for our key.
	fleet.InvalidateFleetConfig(dataDir, srv.URL)

	if _, err := fleet.ResolveFleetConfig(t.Context(), dataDir, srv.URL); err != nil {
		t.Fatalf("first ResolveFleetConfig: %v", err)
	}
	fleet.InvalidateFleetConfig(dataDir, srv.URL)
	if _, err := fleet.ResolveFleetConfig(t.Context(), dataDir, srv.URL); err != nil {
		t.Fatalf("second ResolveFleetConfig: %v", err)
	}

	if got := atomic.LoadInt32(&fetchCount); got != 2 {
		t.Fatalf("GET /config.json called %d times across 2 invalidate-separated calls, want 2", got)
	}
}
