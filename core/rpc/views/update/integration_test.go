// integration_test.go — self-update-repair-01PMUP01 WP07, AC-1.
//
// Drives the ACTUAL production call path the fixed
// frontend/src/lib/updateClient.ts's installLatest uses:
//
//	Manager.StartDownload(ctx) → poll Manager.Status(ctx) until staged →
//	Manager.Apply(ctx)
//
// against a REAL core/update.Service — real httptest.Server, real byte
// payload, real sha256 verification, the real downloadPump goroutine —
// with only the platform Swapper faked (CLAUDE.md blind spot #2: a fake
// Service here would bypass the exact layer (Service.Download's HTTP +
// sha256 + on-disk staging) this mission's fix depends on being real).
// core/update/integration_test.go already covers Service.* directly;
// this file covers the Manager wrapper the frontend actually calls,
// which is the thing WP02 fixed.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
)

// ac1FakeSwapper records Swap/Restart calls without touching the real
// process. This is the ONLY fake in this test — everything else
// (manifest fetch, binary download, sha256 verification, on-disk
// staging) runs through the real coreupdate.Service.
type ac1FakeSwapper struct {
	swapCalls    int
	swapPath     string
	restartCalls int
	restartPath  string
}

func (s *ac1FakeSwapper) Swap(_ context.Context, staged coreupdate.StagedUpdate, runningBinaryPath, _ string) error {
	s.swapCalls++
	s.swapPath = staged.Path
	if _, err := os.Stat(staged.Path); err != nil {
		return fmt.Errorf("ac1FakeSwapper.Swap: staged path missing: %w", err)
	}
	if runningBinaryPath == "" {
		return fmt.Errorf("ac1FakeSwapper.Swap: empty running binary path")
	}
	return nil
}

func (s *ac1FakeSwapper) Restart(_ context.Context, newBinaryPath string) error {
	s.restartCalls++
	s.restartPath = newBinaryPath
	return nil
}

// TestAC1_EndToEnd_ManagerStartDownloadPollApply drives the real
// production path AC-1 names. Asserts:
//   - Apply returns nil
//   - Swap was called once, with the staged artifact's path
//   - Restart was called once
func TestAC1_EndToEnd_ManagerStartDownloadPollApply(t *testing.T) {
	payload := []byte("AC-1 end-to-end payload — real bytes, real sha256, real download pump\n")
	shaSum := sha256.Sum256(payload)
	sha := hex.EncodeToString(shaSum[:])

	var srv *httptest.Server
	mux := http.NewServeMux()
	srv = httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(struct {
			Version string `json:"version"`
			Notes   string `json:"notes"`
			Assets  []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
				Sha256   string `json:"sha256"`
			} `json:"assets"`
		}{
			Version: "9.9.9",
			Notes:   "AC-1 test release",
			Assets: []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
				Sha256   string `json:"sha256"`
			}{
				{Platform: "linux/amd64", URL: "http://" + srv.Listener.Addr().String() + "/bin", Sha256: sha},
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

	swapper := &ac1FakeSwapper{}
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
		t.Fatalf("coreupdate.NewService: %v", err)
	}

	mgr := New(Config{Service: svc, CurrentVersion: "0.0.1"})
	ctx := context.Background()

	// ── StartCheck: real manifest fetch over real HTTP ────────────────
	if err := mgr.StartCheck(ctx); err != nil {
		t.Fatalf("StartCheck: %v", err)
	}
	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status (after check): %v", err)
	}
	if !status.Available {
		t.Fatalf("Status.Available = false after a real check found a newer version: %+v", status)
	}

	// ── StartDownload: real HTTP GET, real sha256 verify, real staging ─
	if err := mgr.StartDownload(ctx); err != nil {
		t.Fatalf("StartDownload: %v", err)
	}

	// ── Poll Status until staged — the WP02 mechanism, exercised
	// end-to-end against a real download instead of a fake channel. ────
	deadline := time.Now().Add(10 * time.Second)
	var finalStatus StatusOutput
	for time.Now().Before(deadline) {
		finalStatus, err = mgr.Status(ctx)
		if err != nil {
			t.Fatalf("Status (polling): %v", err)
		}
		if finalStatus.DownloadState == "staged" || finalStatus.DownloadState == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finalStatus.DownloadState != "staged" {
		t.Fatalf("download did not reach 'staged' within 10s: final status = %+v", finalStatus)
	}

	// ── Apply: the real Swap+Restart contract, against the fake Swapper ─
	if err := mgr.Apply(ctx); err != nil {
		t.Fatalf("Apply returned an error: %v", err)
	}
	if swapper.swapCalls != 1 {
		t.Errorf("Swap called %d times, want 1", swapper.swapCalls)
	}
	if swapper.restartCalls != 1 {
		t.Errorf("Restart called %d times, want 1", swapper.restartCalls)
	}
	if swapper.swapPath == "" || filepath.Dir(swapper.swapPath) != filepath.Join(dataDir, "update", "staging") {
		t.Errorf("Swap called with unexpected staged path: %q", swapper.swapPath)
	}
	if swapper.restartPath != runningPath {
		t.Errorf("Restart called with %q, want %q", swapper.restartPath, runningPath)
	}
}
