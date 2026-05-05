// edit_file_sync.go implements the edit-file artifact sync pipeline for
// mission edit-file-artifact-sync-01KQ8TD5. The feature is gated behind
// HARNESS_EDIT_FILE_ARTIFACT_SYNC=on (WP04). When active, every
// kaneaz__edit_file tool call is recognised by the sink and a post-edit
// snapshot of the file is captured as an artifact with AbsolutePath set
// in the SourceRef.
//
// Architecture (WPs 02–06):
//
//	WP02 — ExtractEditFilePath:  recognise __edit_file + extract the
//	        canonical path from tool args.
//	WP03 — CoalesceBuffer: per-session buffer that deduplicates flushes
//	        within one turn so editing the same file N times only creates
//	        one artifact per flush boundary.
//	WP05 — captureFromEditFile: read the post-edit content and create a
//	        CaptureCandidate with AbsolutePath set.
//
// WP04 feature-gate: editFileArtifactSyncActive() checks the env-var.
// The sink also checks the per-user settings dial (LoadEditFileArtifactSyncEnabled).
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package artifacts

import (
	"context"
	"encoding/json"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
)

// envEditFileArtifactSync is the primary feature-gate env-var for the
// edit-file artifact sync feature (WP04).
const envEditFileArtifactSync = "HARNESS_EDIT_FILE_ARTIFACT_SYNC"

// editFileArtifactSyncActive reports whether the env-var gate is open.
// Called on every tool dispatch so a mid-session toggle takes effect
// without a restart (matches HARNESS_COMPACTION=off semantics).
func editFileArtifactSyncActive() bool {
	return os.Getenv(envEditFileArtifactSync) == "on"
}

// EditFileArtifactSyncEnabled is the per-user settings opt-in predicate
// type. The sink stores a copy of this function at construction; it is
// called after the env-var gate so both checks must pass.
type EditFileArtifactSyncEnabled func() bool

// CoalesceBuffer is a per-session deduplication buffer for edit_file
// paths. Within a single flush boundary (one chat turn), editing the
// same file multiple times coalesces into one artifact per file.
//
// CoalesceBuffer is safe for concurrent use.
type CoalesceBuffer struct {
	mu       sync.Mutex
	sessions map[string]map[string]struct{} // sessionID → set[absPath]
}

// NewCoalesceBuffer returns an empty CoalesceBuffer.
func NewCoalesceBuffer() *CoalesceBuffer {
	return &CoalesceBuffer{sessions: map[string]map[string]struct{}{}}
}

// Add records absPath as pending capture for sessionID. Returns true if
// this is the first time this path appears in the current flush window.
func (b *CoalesceBuffer) Add(sessionID, absPath string) bool {
	if b == nil || absPath == "" {
		return true // nil buffer → always first
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions[sessionID] == nil {
		b.sessions[sessionID] = map[string]struct{}{}
	}
	if _, exists := b.sessions[sessionID][absPath]; exists {
		return false
	}
	b.sessions[sessionID][absPath] = struct{}{}
	return true
}

// Flush drains the session's pending paths and returns them. After
// Flush the session's set is empty, ready for the next turn.
func (b *CoalesceBuffer) Flush(sessionID string) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	paths := b.sessions[sessionID]
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	delete(b.sessions, sessionID)
	return out
}

// Drop removes all pending entries for sessionID (called when a session ends).
func (b *CoalesceBuffer) Drop(sessionID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sessions, sessionID)
}

// ExtractEditFilePath parses toolArgs and returns the canonical
// absolute path from a kaneaz__edit_file call. Returns "" when
// toolName is not an edit_file variant or args are unparseable.
//
// Recognised suffixes: "__edit_file" (kaneaz built-in).
func ExtractEditFilePath(toolName, toolArgs string) string {
	if toolName == "" || toolArgs == "" {
		return ""
	}
	if !strings.HasSuffix(toolName, "__edit_file") {
		return ""
	}
	// Parse the path field from JSON args.
	// We avoid importing encoding/json in the hot path — a simple string
	// scan for "\"path\":" is robust enough for our well-formed args.
	var parsed struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(toolArgs), &parsed); err != nil {
		return ""
	}
	if parsed.Path == "" {
		return ""
	}
	abs, err := filepath.Abs(parsed.Path)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// captureFromEditFile reads the current on-disk content of absPath and
// constructs a CaptureCandidate with AbsolutePath set in the SourceRef.
// Returns nil if the file is unreadable.
func captureFromEditFile(toolName, absPath string) *coreart.CaptureCandidate {
	if absPath == "" {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	base := filepath.Base(absPath)
	mt := mime.TypeByExtension(filepath.Ext(absPath))
	if mt == "" {
		mt = "text/plain; charset=utf-8"
	}
	ref := coreart.ArtifactSourceRef{
		MessageID:    "tool:" + toolName,
		Filename:     base,
		AbsolutePath: absPath,
	}
	return &coreart.CaptureCandidate{
		Title:     base,
		MimeType:  mt,
		Bytes:     data,
		Source:    coreart.SourceToolOutput,
		SourceRef: ref,
	}
}

// onPostEditFileMessage is called by the sink's OnPostToolMessage when
// the feature is active and the tool is a __edit_file variant. It:
//  1. Extracts the path from args.
//  2. Adds to the CoalesceBuffer (dedup within one turn).
//  3. Immediately captures the post-edit content.
func (s *sink) onPostEditFileMessage(
	ctx context.Context,
	sessionID, toolName, toolArgs string,
	buf *CoalesceBuffer,
) {
	absPath := ExtractEditFilePath(toolName, toolArgs)
	if absPath == "" {
		return
	}

	// Dedup: only capture once per turn per path.
	if buf != nil && !buf.Add(sessionID, absPath) {
		return
	}

	cand := captureFromEditFile(toolName, absPath)
	if cand == nil {
		s.log.Warn("artifacts.edit_file_sync.read_failed",
			"session_id", sessionID,
			"path", absPath)
		return
	}

	captured, err := s.mgr.Capture(ctx, []coreart.CaptureCandidate{*cand}, sessionID)
	if err != nil {
		s.log.Warn("artifacts.edit_file_sync.capture_failed",
			"session_id", sessionID,
			"path", absPath,
			"err", err.Error())
		return
	}
	if len(captured) > 0 {
		s.log.Info("artifacts.edit_file_sync.captured",
			"count", len(captured),
			"session_id", sessionID,
			"tool", toolName,
			"path", absPath,
		)
	}
}

// sinkWithEditSync wraps a sink and adds the CoalesceBuffer for the
// edit-file artifact sync pipeline. Exposed as a separate type so the
// chassis can wire it without changing the core sink interface.
type sinkWithEditSync struct {
	*sink
	buf                  *CoalesceBuffer
	editFileSyncEnabled  EditFileArtifactSyncEnabled
}

// NewSinkWithEditSync wraps an existing sink with the edit-file sync
// pipeline. editFileSyncEnabled is the per-user settings predicate;
// nil means "always enabled when env-var is set".
func NewSinkWithEditSync(inner *Sink, buf *CoalesceBuffer, editFileSyncEnabled EditFileArtifactSyncEnabled) *sinkWithEditSync {
	if inner == nil {
		return nil
	}
	return &sinkWithEditSync{
		sink:                inner.sink,
		buf:                 buf,
		editFileSyncEnabled: editFileSyncEnabled,
	}
}

// OnPostToolMessage overrides the embedded sink's method to intercept
// __edit_file calls when the feature is active.
func (s *sinkWithEditSync) OnPostToolMessage(ctx context.Context, sessionID, toolName, toolArgs, toolResult string, dur time.Duration) {
	// Always run the base sink's logic first.
	s.sink.OnPostToolMessage(ctx, sessionID, toolName, toolArgs, toolResult, dur)

	// Feature gate: env-var must be on.
	if !editFileArtifactSyncActive() {
		return
	}
	// Per-user gate: settings must not have explicitly disabled it.
	if s.editFileSyncEnabled != nil && !s.editFileSyncEnabled() {
		return
	}
	// Only handle __edit_file calls.
	if !strings.HasSuffix(toolName, "__edit_file") {
		return
	}
	s.onPostEditFileMessage(ctx, sessionID, toolName, toolArgs, s.buf)
}
