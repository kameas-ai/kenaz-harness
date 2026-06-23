package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── ExtractEditFilePath ─────────────────────────────────────────────────────

func TestExtractEditFilePath_HappyPath(t *testing.T) {
	t.Parallel()
	args := `{"path":"/tmp/foo.go","old_str":"foo","new_str":"bar"}`
	got := ExtractEditFilePath("kenaz__edit_file", args)
	if got == "" {
		t.Fatal("expected non-empty path")
	}
	// Result must be an absolute path.
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestExtractEditFilePath_WrongTool(t *testing.T) {
	t.Parallel()
	args := `{"path":"/tmp/foo.go","old_str":"foo","new_str":"bar"}`
	got := ExtractEditFilePath("kenaz__read_file", args)
	if got != "" {
		t.Errorf("expected empty for non-edit_file tool, got %q", got)
	}
}

func TestExtractEditFilePath_EmptyArgs(t *testing.T) {
	t.Parallel()
	got := ExtractEditFilePath("kenaz__edit_file", "")
	if got != "" {
		t.Errorf("expected empty for empty args, got %q", got)
	}
}

func TestExtractEditFilePath_MissingPath(t *testing.T) {
	t.Parallel()
	got := ExtractEditFilePath("kenaz__edit_file", `{"old_str":"x","new_str":"y"}`)
	if got != "" {
		t.Errorf("expected empty for missing path, got %q", got)
	}
}

// ─── CoalesceBuffer ──────────────────────────────────────────────────────────

func TestCoalesceBuffer_DeduplicatesWithinTurn(t *testing.T) {
	t.Parallel()
	buf := NewCoalesceBuffer()

	// First add: new path.
	if !buf.Add("s1", "/tmp/foo.go") {
		t.Error("expected true (first occurrence)")
	}
	// Second add: same path, same session → deduplicated.
	if buf.Add("s1", "/tmp/foo.go") {
		t.Error("expected false (duplicate)")
	}
	// Different session: independent.
	if !buf.Add("s2", "/tmp/foo.go") {
		t.Error("expected true for different session")
	}
}

func TestCoalesceBuffer_FlushClearsSession(t *testing.T) {
	t.Parallel()
	buf := NewCoalesceBuffer()
	buf.Add("s1", "/tmp/a.go")
	buf.Add("s1", "/tmp/b.go")

	paths := buf.Flush("s1")
	if len(paths) != 2 {
		t.Errorf("expected 2 paths after flush, got %d", len(paths))
	}
	// After flush, buffer is empty.
	paths2 := buf.Flush("s1")
	if len(paths2) != 0 {
		t.Errorf("expected 0 paths after second flush, got %d", len(paths2))
	}
}

func TestCoalesceBuffer_Drop(t *testing.T) {
	t.Parallel()
	buf := NewCoalesceBuffer()
	buf.Add("s1", "/tmp/x.go")
	buf.Drop("s1")

	// After drop, the path is no longer seen as duplicate.
	if !buf.Add("s1", "/tmp/x.go") {
		t.Error("expected true after Drop (buffer cleared)")
	}
}

// ─── captureFromEditFile ─────────────────────────────────────────────────────

func TestCaptureFromEditFile_ReadsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	content := "package main\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cand := captureFromEditFile("kenaz__edit_file", path)
	if cand == nil {
		t.Fatal("expected non-nil candidate")
	}
	if string(cand.Bytes) != content {
		t.Errorf("content mismatch: %q != %q", cand.Bytes, content)
	}
	if cand.SourceRef.AbsolutePath == "" {
		t.Error("expected AbsolutePath to be set")
	}
	if cand.SourceRef.AbsolutePath != path {
		t.Errorf("expected %q, got %q", path, cand.SourceRef.AbsolutePath)
	}
}

func TestCaptureFromEditFile_MissingFile(t *testing.T) {
	t.Parallel()
	cand := captureFromEditFile("kenaz__edit_file", "/nonexistent/file.go")
	if cand != nil {
		t.Error("expected nil for missing file")
	}
}

// ─── sinkWithEditSync.OnPostToolMessage ─────────────────────────────────────

func TestSinkWithEditSync_EnvVarGate(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel()
	// Ensure env-var is NOT set.
	t.Setenv(envEditFileArtifactSync, "")

	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), nil)

	args := `{"path":"` + path + `","old_str":"x","new_str":"y"}`
	synced.OnPostToolMessage(context.Background(), "s1", "kenaz__edit_file", args, `{"written":12}`, time.Second)

	// Without env-var, edit_file sync should not have fired.
	// The base sink may or may not fire for tool_output — check that no
	// artifact with AbsolutePath was created.
	calls := mgr.snapshotCalls()
	for _, batch := range calls {
		for _, c := range batch {
			if c.SourceRef.AbsolutePath != "" {
				t.Errorf("unexpected AbsolutePath in capture: %q", c.SourceRef.AbsolutePath)
			}
		}
	}
}

func TestSinkWithEditSync_CapturesWhenEnvVarOn(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel()
	// Enable the feature via env-var.
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	content := "package main\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return true })

	args := `{"path":"` + path + `","old_str":"x","new_str":"y"}`
	synced.OnPostToolMessage(context.Background(), "s1", "kenaz__edit_file", args, `{"written":30}`, time.Second)

	// At least one call should carry AbsolutePath.
	calls := mgr.snapshotCalls()
	found := false
	for _, batch := range calls {
		for _, c := range batch {
			if c.SourceRef.AbsolutePath == path {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected at least one capture with AbsolutePath=%q; calls=%d", path, len(calls))
	}
}

func TestSinkWithEditSync_CoalescesDuplicates(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel()
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	buf := NewCoalesceBuffer()
	synced := NewSinkWithEditSync(inner, buf, func() bool { return true })

	args := `{"path":"` + path + `","old_str":"x","new_str":"y"}`
	ctx := context.Background()
	// Call twice with the same path.
	synced.OnPostToolMessage(ctx, "s1", "kenaz__edit_file", args, `{}`, time.Second)
	synced.OnPostToolMessage(ctx, "s1", "kenaz__edit_file", args, `{}`, time.Second)

	// Count captures with AbsolutePath set.
	count := 0
	for _, batch := range mgr.snapshotCalls() {
		for _, c := range batch {
			if c.SourceRef.AbsolutePath == path {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 capture (coalesced), got %d", count)
	}
}

func TestSinkWithEditSync_PerUserGateOff(t *testing.T) {
	// t.Setenv cannot be used with t.Parallel()
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	// Per-user gate off.
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return false })

	args := `{"path":"` + path + `","old_str":"x","new_str":"y"}`
	synced.OnPostToolMessage(context.Background(), "s1", "kenaz__edit_file", args, `{}`, time.Second)

	for _, batch := range mgr.snapshotCalls() {
		for _, c := range batch {
			if c.SourceRef.AbsolutePath != "" {
				t.Errorf("unexpected capture with AbsolutePath when per-user gate is off")
			}
		}
	}
}
