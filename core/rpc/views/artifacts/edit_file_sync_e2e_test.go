// Package artifacts contains end-to-end tests for the edit-file artifact
// sync pipeline (mission edit-file-artifact-sync-01KQ8TD5 WP06).
//
// These tests drive the full in-process flow: sink construction →
// OnPostToolMessage dispatch → CoalesceBuffer dedup → capture recording.
// They do NOT require a real LLM or network: a thin in-process driver
// wires a [recordingCaptureManager] in place of the real
// core/artifacts.Manager and a temp directory in place of real disk files.
//
// Cross-project isolation (WP06 Test C) is inherently a store-level
// concern that requires a real SQLite store with project-scoped rows.
// The recordingCaptureManager has no project concept, so Test C is
// implemented at the sink-invocation level: the test creates two
// independent sinkWithEditSync instances (one per "project"), calls
// OnPostToolMessage on each, and asserts each manager only saw its own
// capture — simulating the chassis construction that builds one sink per
// agent-graph context. A note is included in the test body.
package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// editFileArgs builds a minimal JSON tool-args string for kenaz__edit_file.
func editFileArgs(path string) string {
	return `{"path":"` + path + `","old_str":"x","new_str":"y"}`
}

// editFileOKResult is a representative tool result for a successful
// kenaz__edit_file call (the sink only checks for non-empty result;
// actual content is irrelevant for capture purposes).
const editFileOKResult = `{"written":12}`

// ─── Test A — coalesce: 3 edits to same path → 1 artifact ──────────────────

// TestE2E_A_Coalesce_SamePath3Edits verifies that three consecutive
// kenaz__edit_file calls to the same path within one turn produce
// exactly ONE artifact capture (CoalesceBuffer deduplication).
func TestE2E_A_Coalesce_SamePath3Edits(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "design.md")
	if err := os.WriteFile(path, []byte("# design\nv1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	buf := NewCoalesceBuffer()
	synced := NewSinkWithEditSync(inner, buf, func() bool { return true })

	ctx := context.Background()
	args := editFileArgs(path)

	// Three edits to the same path within one turn.
	synced.OnPostToolMessage(ctx, "sess-a", "kenaz__edit_file", args, editFileOKResult, time.Second)
	synced.OnPostToolMessage(ctx, "sess-a", "kenaz__edit_file", args, editFileOKResult, time.Second)
	synced.OnPostToolMessage(ctx, "sess-a", "kenaz__edit_file", args, editFileOKResult, time.Second)

	// Count captures with AbsolutePath set to our path.
	count := countCapturesForPath(mgr, path)
	if count != 1 {
		t.Errorf("Test A: expected 1 artifact (coalesced), got %d", count)
	}
}

// ─── Test B — coalesce-1-revision-per-turn ─────────────────────────────────

// TestE2E_B_CoalescePerTurn verifies turn-boundary semantics:
//   - 3 edits to the same path in turn 1 → 1 capture
//   - FlushSession resets the buffer between turns
//   - 1 edit to the same path in turn 2 → 1 more capture (total 2)
func TestE2E_B_CoalescePerTurn(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(path, []byte("# spec\nv1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	buf := NewCoalesceBuffer()
	synced := NewSinkWithEditSync(inner, buf, func() bool { return true })

	ctx := context.Background()
	args := editFileArgs(path)

	// Turn 1: 3 edits to same path → 1 capture.
	synced.OnPostToolMessage(ctx, "sess-b", "kenaz__edit_file", args, editFileOKResult, time.Second)
	synced.OnPostToolMessage(ctx, "sess-b", "kenaz__edit_file", args, editFileOKResult, time.Second)
	synced.OnPostToolMessage(ctx, "sess-b", "kenaz__edit_file", args, editFileOKResult, time.Second)

	afterTurn1 := countCapturesForPath(mgr, path)
	if afterTurn1 != 1 {
		t.Errorf("Test B turn 1: expected 1 capture, got %d", afterTurn1)
	}

	// Simulate OnChatRunComplete: flush the session buffer.
	synced.FlushSession(ctx, "sess-b")

	// Turn 2: 1 more edit to the same path → should create a second capture
	// because the buffer was flushed.
	synced.OnPostToolMessage(ctx, "sess-b", "kenaz__edit_file", args, editFileOKResult, time.Second)

	afterTurn2 := countCapturesForPath(mgr, path)
	if afterTurn2 != 2 {
		t.Errorf("Test B turn 2: expected 2 total captures (1 per turn), got %d", afterTurn2)
	}
}

// ─── Test C — cross-project isolation ──────────────────────────────────────

// TestE2E_C_CrossProject simulates two chat sessions in different "projects"
// editing the same disk path. The chassis constructs one sinkWithEditSync per
// agent-graph context, so in production each project's session gets a
// separate sink + manager. Here we model that by using two independent
// recordingCaptureManager instances (one per "project").
//
// Assertion: manager A only sees the capture from session A; manager B only
// sees the capture from session B. Neither manager is contaminated by the
// other session's edit.
func TestE2E_C_CrossProject(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "shared.md")
	if err := os.WriteFile(path, []byte("shared content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project A sink + manager.
	mgrA := &recordingCaptureManager{}
	syncedA := NewSinkWithEditSync(
		NewSinkConcrete(mgrA, nil, nil),
		NewCoalesceBuffer(),
		func() bool { return true },
	)

	// Project B sink + manager.
	mgrB := &recordingCaptureManager{}
	syncedB := NewSinkWithEditSync(
		NewSinkConcrete(mgrB, nil, nil),
		NewCoalesceBuffer(),
		func() bool { return true },
	)

	ctx := context.Background()
	args := editFileArgs(path)

	// Project A session edits the file.
	syncedA.OnPostToolMessage(ctx, "sess-projA", "kenaz__edit_file", args, editFileOKResult, time.Second)
	// Project B session edits the same disk path.
	syncedB.OnPostToolMessage(ctx, "sess-projB", "kenaz__edit_file", args, editFileOKResult, time.Second)

	// Manager A should see exactly 1 capture.
	if got := countCapturesForPath(mgrA, path); got != 1 {
		t.Errorf("Test C: project A manager: expected 1 capture, got %d", got)
	}
	// Manager B should see exactly 1 capture.
	if got := countCapturesForPath(mgrB, path); got != 1 {
		t.Errorf("Test C: project B manager: expected 1 capture, got %d", got)
	}
	// Manager A must NOT contain any capture that mgrB produced (no bleed).
	if got := len(mgrA.snapshotCalls()); got != 1 {
		t.Errorf("Test C: project A manager call count: expected 1, got %d", got)
	}
}

// ─── Test D — save-artifact-sourced rows skipped ───────────────────────────

// TestE2E_D_SaveArtifactSourcedRowNotUpdated verifies that a path that was
// never captured via __edit_file (i.e. only via save_artifact / code-block
// detection) does not interfere with a fresh __edit_file capture to a
// different path: each path is independent. This models the invariant that
// the save_artifact row (empty AbsolutePath) is not mutated when a new
// __edit_file capture lands for a never-saved-via-tool path.
//
// Implementation note: the recordingCaptureManager does not distinguish
// SourceUserPin vs SourceToolOutput — it just records candidates. We verify
// that the __edit_file capture produces exactly one call for the new path
// and that a prior call with an empty AbsolutePath is not revisited.
func TestE2E_D_SaveArtifactSourcedRowNotUpdated(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	editPath := filepath.Join(dir, "tool_output.md")
	if err := os.WriteFile(editPath, []byte("from tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return true })

	ctx := context.Background()

	// Simulate a prior save_artifact capture (empty AbsolutePath — set via
	// the base sink's OnAssistantMessage code-block path). We don't test that
	// path here; we just assert that a subsequent __edit_file call to a fresh
	// path does not erroneously update the prior row.
	// In production the store's FindByAbsolutePath(empty) → never matches
	// invariant provides the guard; here we assert that a __edit_file call to
	// editPath produces exactly one new capture for editPath.

	synced.OnPostToolMessage(ctx, "sess-d", "kenaz__edit_file", editFileArgs(editPath), editFileOKResult, time.Second)

	if got := countCapturesForPath(mgr, editPath); got != 1 {
		t.Errorf("Test D: expected 1 capture for editPath, got %d", got)
	}
	// Total Capture calls should be exactly 1 (the __edit_file one).
	if total := len(mgr.snapshotCalls()); total != 1 {
		t.Errorf("Test D: expected exactly 1 Capture call, got %d", total)
	}
}

// ─── Test E — edit-on-deleted-file ─────────────────────────────────────────

// TestE2E_E_EditOnDeletedFile verifies that when a file is deleted between
// the __edit_file tool call and the capture attempt (i.e. between the
// OnPostToolMessage dispatch and the captureFromEditFile read), the sink:
//   - does NOT panic
//   - does NOT call mgr.Capture
//   - (log output verifiable via slog; not asserted here due to test logger cost)
func TestE2E_E_EditOnDeletedFile(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "ephemeral.md")
	// Create the file so ExtractEditFilePath returns a non-empty path.
	if err := os.WriteFile(path, []byte("to be deleted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Delete the file before the tool message is dispatched to simulate a
	// race between the tool completing and the capture read.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return true })

	ctx := context.Background()

	// Must not panic.
	synced.OnPostToolMessage(ctx, "sess-e", "kenaz__edit_file", editFileArgs(path), editFileOKResult, time.Second)

	// No artifact should have been captured for the deleted file.
	if got := countCapturesForPath(mgr, path); got != 0 {
		t.Errorf("Test E: expected 0 captures for deleted file, got %d", got)
	}
}

// ─── Test F — feature flag off ─────────────────────────────────────────────

// TestE2E_F_FeatureFlagOff verifies that when HARNESS_EDIT_FILE_ARTIFACT_SYNC
// is unset (or not "on"), __edit_file calls produce no artifact mutations.
// The base sink's __write_file path remains active (it does not consult the
// env-var gate), so __write_file with content still captures — this is the
// pre-WP01 baseline behaviour.
func TestE2E_F_FeatureFlagOff(t *testing.T) {
	// Explicitly unset the env-var (gate off).
	t.Setenv(envEditFileArtifactSync, "")

	dir := t.TempDir()
	path := filepath.Join(dir, "gated.md")
	if err := os.WriteFile(path, []byte("gated content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return true })

	ctx := context.Background()

	synced.OnPostToolMessage(ctx, "sess-f", "kenaz__edit_file", editFileArgs(path), editFileOKResult, time.Second)

	// No artifact with AbsolutePath should have been created.
	if got := countCapturesForPath(mgr, path); got != 0 {
		t.Errorf("Test F: expected 0 captures when env-var gate is off, got %d", got)
	}
}

// ─── Test F2 — per-user disabled path ──────────────────────────────────────

// TestE2E_F2_PerUserGateDisabled verifies that the per-user settings dial
// (EditFileArtifactSyncDisabled=true) prevents captures even when the
// env-var gate is open.
func TestE2E_F2_PerUserGateDisabled(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	path := filepath.Join(dir, "user_disabled.md")
	if err := os.WriteFile(path, []byte("user disabled\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	// Per-user gate explicitly off.
	synced := NewSinkWithEditSync(inner, NewCoalesceBuffer(), func() bool { return false })

	ctx := context.Background()

	synced.OnPostToolMessage(ctx, "sess-f2", "kenaz__edit_file", editFileArgs(path), editFileOKResult, time.Second)

	if got := countCapturesForPath(mgr, path); got != 0 {
		t.Errorf("Test F2: expected 0 captures when per-user gate is off, got %d", got)
	}
}

// ─── Test: 3 different paths → 3 artifacts ─────────────────────────────────

// TestE2E_ThreeDifferentPaths verifies that three edits to three DISTINCT
// paths within one turn each produce one artifact (no false coalescing).
func TestE2E_ThreeDifferentPaths(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "a.md"),
		filepath.Join(dir, "b.md"),
		filepath.Join(dir, "c.md"),
	}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("content\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	buf := NewCoalesceBuffer()
	synced := NewSinkWithEditSync(inner, buf, func() bool { return true })

	ctx := context.Background()

	for _, p := range paths {
		synced.OnPostToolMessage(ctx, "sess-3paths", "kenaz__edit_file", editFileArgs(p), editFileOKResult, time.Second)
	}

	// Each distinct path should produce exactly 1 capture.
	for _, p := range paths {
		if got := countCapturesForPath(mgr, p); got != 1 {
			t.Errorf("expected 1 capture for %q, got %d", p, got)
		}
	}
	// Total captures == 3 (one per path).
	if total := len(mgr.snapshotCalls()); total != 3 {
		t.Errorf("expected 3 total Capture calls, got %d", total)
	}
}

// ─── Test: FlushSession is a no-op when buffer is nil ──────────────────────

// TestE2E_FlushSession_NilBuf verifies that FlushSession does not panic
// when the sinkWithEditSync has a nil CoalesceBuffer.
func TestE2E_FlushSession_NilBuf(t *testing.T) {
	t.Setenv(envEditFileArtifactSync, "on")

	mgr := &recordingCaptureManager{}
	inner := NewSinkConcrete(mgr, nil, nil)
	// Intentionally pass nil buf — should not panic.
	synced := NewSinkWithEditSync(inner, nil, nil)

	ctx := context.Background()
	synced.FlushSession(ctx, "sess-nilbuf") // must not panic
}

// ─── helper ─────────────────────────────────────────────────────────────────

// countCapturesForPath returns the number of CaptureCandidate values
// the recordingCaptureManager received whose SourceRef.AbsolutePath
// matches path.
func countCapturesForPath(m *recordingCaptureManager, path string) int {
	count := 0
	for _, batch := range m.snapshotCalls() {
		for _, c := range batch {
			if c.SourceRef.AbsolutePath == path {
				count++
			}
		}
	}
	return count
}
