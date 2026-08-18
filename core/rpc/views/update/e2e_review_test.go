// e2e_review_test.go — adversarial-review additions to WP07.
//
// Two gaps in the shipped AC-1/AC-10 evidence, both closed here:
//
//  1. AC-10 (the mission's PREMISE — the pre-fix double-await always
//     throws ErrNothingStaged) was observed once by hand on a running
//     app and recorded in prose. Prose is not a regression test: nothing
//     stops a future refactor from making StartDownload synchronous,
//     quietly retiring the reason installLatest polls at all. Pinned.
//
//  2. AC-1 runs the real Service (real HTTP, real sha256, real pump) but
//     fakes BOTH halves of the Swapper, so realSwapper.Swap — the
//     destructive rename plus its pre-swap sha256 re-verify, i.e. the
//     step that actually installs the update — has never executed on
//     this path. Only Restart genuinely cannot run in-process (it
//     fork-execs and os.Exit(0)s the test binary). So: run the REAL
//     Swap, fake only Restart.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

// reviewRig stands up a real manifest + asset server and a real
// Service, and returns a Manager over it.
func reviewRig(t *testing.T, swapper coreupdate.Swapper) (*Manager, string, string) {
	t.Helper()
	payload := []byte("review rig payload — real bytes, real sha256, real pump\n")
	shaSum := sha256.Sum256(payload)
	sha := hex.EncodeToString(shaSum[:])

	var srv *httptest.Server
	mux := http.NewServeMux()
	srv = httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"version": "9.9.9",
			"notes":   "review rig release",
			"assets": []map[string]string{
				{"platform": "linux/amd64", "url": "http://" + srv.Listener.Addr().String() + "/bin", "sha256": sha},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = w.Write(payload)
	})
	srv.Start()
	t.Cleanup(srv.Close)

	dataDir := t.TempDir()
	runningPath := filepath.Join(dataDir, "kenaz-harness")
	if err := os.WriteFile(runningPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("write running binary stub: %v", err)
	}
	svc, err := coreupdate.NewService(coreupdate.Config{
		CurrentVersion:    "0.0.1",
		DataDir:           dataDir,
		HTTPClient:        &http.Client{Timeout: 5 * time.Second},
		Swapper:           swapper,
		RunningBinaryPath: runningPath,
		ManifestURL:       srv.URL + "/manifest.json",
		Platform:          "linux/amd64",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mgr := New(Config{Service: svc, CurrentVersion: "0.0.1"})
	if err := mgr.StartCheck(context.Background()); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}
	return mgr, runningPath, string(payload)
}

// realSwapFakeRestart runs the PRODUCTION Swap (rename + pre-swap
// sha256 re-verify) and fakes only Restart, which fork-execs and
// os.Exit(0)s and therefore cannot run inside a test binary.
type realSwapFakeRestart struct {
	inner        coreupdate.Swapper
	restartCalls int
	restartPath  string
}

func (s *realSwapFakeRestart) Swap(ctx context.Context, staged coreupdate.StagedUpdate, running, dataDir string) error {
	return s.inner.Swap(ctx, staged, running, dataDir)
}

func (s *realSwapFakeRestart) Restart(_ context.Context, p string) error {
	s.restartCalls++
	s.restartPath = p
	return nil
}

// TestAC10_PreFixDoubleAwaitAlwaysFails pins spec §1.1's premise: the
// pre-fix `await StartDownload(); await Apply();` sequence is lost by
// CONSTRUCTION, not by timing — StartDownload clears hasStaged on the
// calling goroutine before it returns, so no goroutine schedule rescues
// Apply. Run 50 times to make "not by timing" an assertion rather than
// a claim.
//
// Mutation: make StartDownload block until the pump finishes (i.e.
// remove the reason installLatest polls) → this test fails, which is
// the point: whoever does that must consciously retire the poll loop
// rather than leave it as cargo.
func TestAC10_PreFixDoubleAwaitAlwaysFails(t *testing.T) {
	for i := 0; i < 50; i++ {
		mgr, _, _ := reviewRig(t, &ac1FakeSwapper{})
		ctx := context.Background()
		if err := mgr.StartDownload(ctx); err != nil {
			t.Fatalf("iteration %d: StartDownload: %v", i, err)
		}
		// The exact pre-fix call sequence, with no poll in between.
		err := mgr.Apply(ctx)
		if !errors.Is(err, ErrNothingStaged) {
			t.Fatalf("iteration %d: Apply after bare StartDownload = %v, want ErrNothingStaged", i, err)
		}
	}
}

// TestReview_FullInstall_RealSwap drives the whole fixed path with the
// REAL production Swap: real manifest fetch, real download, real sha256
// verify, real poll-to-staged, real rename over the running binary.
// Asserts the bytes on disk at the running-binary path are the
// downloaded payload afterwards — the observable definition of "the
// update was installed", which no test in the mission asserted.
func TestReview_FullInstall_RealSwap(t *testing.T) {
	sw := &realSwapFakeRestart{inner: coreupdate.NewRealSwapper()}
	mgr, runningPath, payload := reviewRig(t, sw)
	ctx := context.Background()

	before, err := os.ReadFile(runningPath)
	if err != nil {
		t.Fatalf("read running binary before: %v", err)
	}
	if string(before) != "old-binary" {
		t.Fatalf("precondition: running binary = %q", before)
	}

	if err := mgr.StartDownload(ctx); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var st StatusOutput
	for time.Now().Before(deadline) {
		st, _ = mgr.Status(ctx)
		if st.DownloadState == "staged" || st.DownloadState == "failed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.DownloadState != "staged" {
		t.Fatalf("never staged: %+v", st)
	}

	if err := mgr.Apply(ctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, err := os.ReadFile(runningPath)
	if err != nil {
		t.Fatalf("read running binary after: %v", err)
	}
	if string(after) != payload {
		t.Fatalf("running binary after Apply = %q, want the downloaded payload %q", after, payload)
	}
	if sw.restartCalls != 1 || sw.restartPath != runningPath {
		t.Errorf("Restart calls=%d path=%q, want 1 / %q", sw.restartCalls, sw.restartPath, runningPath)
	}
}
