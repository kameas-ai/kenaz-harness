package shell

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordingOpener is a test Opener that captures every URL the API
// hands it without invoking the real runtime.
type recordingOpener struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingOpener) Capture(_ context.Context, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, url)
}

func (r *recordingOpener) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.urls))
	copy(out, r.urls)
	return out
}

func TestOpenInOSBrowser_NonExistentPathFails(t *testing.T) {
	t.Parallel()
	rec := &recordingOpener{}
	api := New(rec.Capture)
	api.SetContext(context.Background())

	err := api.OpenInOSBrowser(context.Background(), filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("OpenInOSBrowser on nonexistent path: want error, got nil")
	}
	if len(rec.Calls()) != 0 {
		t.Fatalf("opener was invoked despite stat failure: %v", rec.Calls())
	}
}

func TestOpenInOSBrowser_ExistingPathDispatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec := &recordingOpener{}
	api := New(rec.Capture)
	api.SetContext(context.Background())

	if err := api.OpenInOSBrowser(context.Background(), dir); err != nil {
		t.Fatalf("OpenInOSBrowser: %v", err)
	}
	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("opener calls = %d, want 1", len(calls))
	}
	if !strings.HasPrefix(calls[0], "file://") {
		t.Fatalf("URL = %q, want file:// prefix", calls[0])
	}
	abs, _ := filepath.Abs(dir)
	want := "file://" + abs
	if calls[0] != want {
		t.Fatalf("URL = %q, want %q", calls[0], want)
	}
}

func TestOpenInOSBrowser_EmptyPathFails(t *testing.T) {
	t.Parallel()
	api := New(func(context.Context, string) {})
	api.SetContext(context.Background())
	if err := api.OpenInOSBrowser(context.Background(), ""); err == nil {
		t.Fatal("OpenInOSBrowser(\"\"): want error, got nil")
	}
}

func TestOpenInOSBrowser_ContextNotWiredFails(t *testing.T) {
	t.Parallel()
	rec := &recordingOpener{}
	api := New(rec.Capture)
	// Skip SetContext: simulates pre-startup call.
	if err := api.OpenInOSBrowser(context.Background(), t.TempDir()); err == nil {
		t.Fatal("pre-SetContext OpenInOSBrowser: want error, got nil")
	}
	if len(rec.Calls()) != 0 {
		t.Fatalf("opener was invoked despite missing wails ctx: %v", rec.Calls())
	}
}
