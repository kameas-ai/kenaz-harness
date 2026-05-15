package localruntime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLlamaServerFetcher_ActiveModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"id": "llama-3.1-8b-Q4_K_M.gguf"}]}`))
	}))
	defer srv.Close()

	models, err := fetchLlamaServerActive(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchLlamaServerActive error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model; got %d", len(models))
	}
	if models[0].QuantLevel != "Q4_K_M" {
		t.Errorf("expected quant Q4_K_M; got %q", models[0].QuantLevel)
	}
}

func TestLlamaServerFetcher_WalkerBailsAtLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping file-creation stress test in short mode")
	}
	// Create a temp directory with > llamaServerMaxFiles .gguf files.
	dir := t.TempDir()
	for i := 0; i < llamaServerMaxFiles+20; i++ {
		name := filepath.Join(dir, fmt.Sprintf("model_%05d.gguf", i))
		if err := os.WriteFile(name, []byte{}, 0600); err != nil {
			t.Skip("cannot create temp files")
		}
	}

	// Override the model dirs for the duration of this test.
	origDirs := llamaServerModelDirs
	llamaServerModelDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { llamaServerModelDirs = origDirs })

	models, err := walkLlamaServerDirs(context.Background())
	if err != nil {
		t.Fatalf("walkLlamaServerDirs error: %v", err)
	}
	if len(models) > llamaServerMaxFiles {
		t.Errorf("expected at most %d models; got %d", llamaServerMaxFiles, len(models))
	}
}

func TestLlamaServerFetcher_WallerBailsAtTimeout(t *testing.T) {
	dir := t.TempDir()
	// Create a few files.
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(dir, "model.gguf"), []byte{}, 0600)
		break
	}

	origDirs := llamaServerModelDirs
	llamaServerModelDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { llamaServerModelDirs = origDirs })

	// Already-cancelled context simulates wallclock timeout.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	// Should not panic even with cancelled context.
	_, _ = walkLlamaServerDirs(ctx)
}

func TestLlamaServerFetcher_MissingDirIsNoError(t *testing.T) {
	origDirs := llamaServerModelDirs
	llamaServerModelDirs = func() []string { return []string{"/nonexistent/path/that/never/exists"} }
	t.Cleanup(func() { llamaServerModelDirs = origDirs })

	models, err := walkLlamaServerDirs(context.Background())
	if err != nil {
		t.Fatalf("expected no error for missing dir; got: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty result for missing dir; got %d", len(models))
	}
}

