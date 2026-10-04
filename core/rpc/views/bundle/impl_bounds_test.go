package bundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
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
