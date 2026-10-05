package bundle

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	httpchannel "github.com/kameas-ai/kenaz-harness/core/bundle/channels/http"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// TestInstall_OversizedManifestRefused (engine-publication WP-H6, review
// F2): the manifest is buffered in memory, and http_mirror no longer caps
// a progressing body by wall clock, so the buffer itself is capped.
func TestInstall_OversizedManifestRefused(t *testing.T) {
	bundleDir := filepath.Join(t.TempDir(), "huge")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := append([]byte("schema_version: 1\nname: huge\nversion: 0.1.0\nlicense: MIT\n# "),
		bytes.Repeat([]byte("x"), channels.MaxManifestBytes)...)
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(testCAS(t)))
	_, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if !errors.Is(err, channels.ErrFetchTooLarge) {
		t.Fatalf("oversized manifest err = %v, want ErrFetchTooLarge", err)
	}
	if len(rw.data) != 0 {
		t.Fatal("refused install touched the lockfile")
	}
}

func artifactManifest(digest string) string {
	return `schema_version: 1
name: big
version: 0.1.0
license: MIT
artifacts:
  - name: blob
    kind: policy
    path: blob.bin
    content_hash: "` + digest + `"
`
}

// TestInstall_ArtifactOverCapRefused (WP-H7, review D5): an artifact
// stream past the per-artifact cap is refused, the CAS staging file is
// removed and the lockfile is untouched. The 2 GiB production cap is
// shrunk via the package knob.
func TestInstall_ArtifactOverCapRefused(t *testing.T) {
	saved := bundleArtifactCap
	bundleArtifactCap = 1024
	t.Cleanup(func() { bundleArtifactCap = saved })
	if maxBundleArtifactBytes != 2<<30 {
		t.Fatalf("production per-artifact cap = %d, want 2 GiB", maxBundleArtifactBytes)
	}

	bundleDir := filepath.Join(t.TempDir(), "big")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := bytes.Repeat([]byte("b"), 4096)
	if err := os.WriteFile(filepath.Join(bundleDir, "blob.bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(artifactManifest(sha256Hex(blob))), 0o644); err != nil {
		t.Fatal(err)
	}
	casDir := t.TempDir()
	c, err := cache.New(casDir)
	if err != nil {
		t.Fatal(err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(CASFromCache(c)))
	_, err = api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if !errors.Is(err, channels.ErrFetchTooLarge) {
		t.Fatalf("over-cap artifact err = %v, want ErrFetchTooLarge", err)
	}
	if len(rw.data) != 0 {
		t.Fatal("refused install touched the lockfile")
	}
	staged, _ := os.ReadDir(filepath.Join(casDir, "staging"))
	if len(staged) != 0 {
		t.Fatalf("CAS staging not cleaned: %d entries", len(staged))
	}
}

// TestInstall_TrickleHitsOverallDeadline (WP-H7, review D5): a mirror
// that never stalls and never finishes is stopped by Install's overall
// deadline (shrunk via the package knob).
func TestInstall_TrickleHitsOverallDeadline(t *testing.T) {
	saved := installDeadline
	installDeadline = 400 * time.Millisecond
	t.Cleanup(func() { installDeadline = saved })

	mux := http.NewServeMux()
	mux.HandleFunc("/kenaz.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(artifactManifest(sha256Hex([]byte("never")))))
	})
	mux.HandleFunc("/blob.bin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := channels.NewRegistry()
	if err := reg.Register(httpchannel.Kind, httpchannel.Factory); err != nil {
		t.Fatal(err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(testCAS(t)),
		WithChannelRegistry(reg), WithSecretsResolver(secrets.NoopResolver{}))
	start := time.Now()
	if _, err := api.Install(context.Background(), InstallRequest{Kind: httpchannel.Kind, URL: srv.URL}); err == nil {
		t.Fatal("a never-ending trickle install completed")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("deadline took %s", elapsed)
	}
	if len(rw.data) != 0 {
		t.Fatal("timed-out install touched the lockfile")
	}
}
