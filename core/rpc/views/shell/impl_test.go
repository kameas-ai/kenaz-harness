package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
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

// ── PathComplete tests ─────────────────────────────────────────────────

func TestPathComplete_EmptyReturnsNothing(t *testing.T) {
	t.Parallel()
	api := New(nil)
	out, err := api.PathComplete(context.Background(), "")
	if err != nil {
		t.Fatalf("PathComplete(\"\"): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty completions for empty partial, got %v", out)
	}
}

func TestPathComplete_ListsAllowedDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.txt", "gamma.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	api := New(nil)
	out, err := api.PathComplete(context.Background(), filepath.Join(dir, "")+string(filepath.Separator))
	if err != nil {
		t.Fatalf("PathComplete: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("PathComplete returned %d entries, want 3: %v", len(out), out)
	}
}

func TestPathComplete_PrefixFilters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.txt", "alpaca.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	api := New(nil)
	out, err := api.PathComplete(context.Background(), filepath.Join(dir, "alp"))
	if err != nil {
		t.Fatalf("PathComplete: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("PathComplete returned %d entries, want 2 for 'alp' prefix: %v", len(out), out)
	}
	for _, p := range out {
		if !strings.HasPrefix(filepath.Base(p), "alp") {
			t.Errorf("entry %q does not match prefix 'alp'", p)
		}
	}
}

func TestPathComplete_HonoursDenyList(t *testing.T) {
	t.Parallel()
	api := New(nil)
	// /etc is in the deny-list. Asking for completions there should
	// return no entries.
	out, err := api.PathComplete(context.Background(), "/etc/")
	if err != nil {
		t.Fatalf("PathComplete /etc/: %v", err)
	}
	for _, p := range out {
		if !tools.IsDeniedPath(p) {
			continue
		}
		t.Errorf("PathComplete returned denied entry %q", p)
	}
	if len(out) != 0 {
		t.Errorf("expected empty completions for denied dir, got %d: %v", len(out), out)
	}
}

func TestPathComplete_NonexistentParentReturnsEmpty(t *testing.T) {
	t.Parallel()
	api := New(nil)
	out, err := api.PathComplete(context.Background(), filepath.Join(t.TempDir(), "no-such-dir", "x"))
	if err != nil {
		t.Fatalf("PathComplete: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("nonexistent partial should yield empty list, got %v", out)
	}
}

// ── ReadFile tests ──────────────────────────────────────────────────────

func TestReadFile_ReadsAllowedTextFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	body := []byte("hello world")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	api := New(nil)
	data, mt, err := api.ReadFile(context.Background(), p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("data = %q, want %q", string(data), string(body))
	}
	if mt != "text/plain" {
		t.Errorf("mediaType = %q, want text/plain", mt)
	}
}

func TestReadFile_HonoursDenyList(t *testing.T) {
	t.Parallel()
	api := New(nil)
	// /etc/hosts exists on every Unix; it lives under the denied
	// /etc root. ReadFile must reject it.
	if _, err := os.Stat("/etc/hosts"); err != nil {
		t.Skip("no /etc/hosts on this platform")
	}
	_, _, err := api.ReadFile(context.Background(), "/etc/hosts")
	if err == nil {
		t.Fatal("ReadFile(/etc/hosts): want deny-list error, got nil")
	}
	if !errors.Is(err, tools.ErrPathInDenyList) {
		t.Errorf("ReadFile error = %v, want ErrPathInDenyList", err)
	}
}

func TestReadFile_NonexistentPathFails(t *testing.T) {
	t.Parallel()
	api := New(nil)
	_, _, err := api.ReadFile(context.Background(), filepath.Join(t.TempDir(), "no-such-file"))
	if err == nil {
		t.Fatal("ReadFile on nonexistent path: want error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile error = %v, want os.ErrNotExist", err)
	}
}

func TestReadFile_DirectoryFails(t *testing.T) {
	t.Parallel()
	api := New(nil)
	_, _, err := api.ReadFile(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("ReadFile on directory: want error, got nil")
	}
}

func TestReadFile_TextSizeCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	// One byte over the 1 MiB text snapshot cap.
	body := make([]byte, MaxTextSnapshotBytes+1)
	for i := range body {
		body[i] = 'a'
	}
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	api := New(nil)
	_, _, err := api.ReadFile(context.Background(), p)
	if err == nil {
		t.Fatal("ReadFile on oversized text file: want error, got nil")
	}
}

func TestReadFile_DetectsImageType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "img.png")
	// Minimal PNG header.
	body := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x00}
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	api := New(nil)
	_, mt, err := api.ReadFile(context.Background(), p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if mt != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mt)
	}
}

func TestReadFile_EmptyPathFails(t *testing.T) {
	t.Parallel()
	api := New(nil)
	if _, _, err := api.ReadFile(context.Background(), ""); err == nil {
		t.Fatal("ReadFile(\"\"): want error, got nil")
	}
}
