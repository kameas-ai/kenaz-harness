// edit_file_sync.go implements the edit-file artifact sync pipeline for
// mission edit-file-artifact-sync-01KQ8TD5. The feature is gated behind
// HARNESS_EDIT_FILE_ARTIFACT_SYNC=on (WP04). When active, every
// kenaz__edit_file tool call is recognised by the sink and a post-edit
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
//	WP06 — OTel instrumentation: artifact.editfile.enqueue and
//	        artifact.editfile.flush spans emitted around the hot paths.
//	        private.* attribute prefix used for any attribute carrying
//	        user content (path values are treated as user content,
//	        defence-in-depth per NFR-004).
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

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// editFileSyncTracer is the instrumentation library name used for all
// spans emitted by the edit-file sync pipeline (WP06).
const editFileSyncTracerName = "kenaz-harness/artifacts/edit-file-sync"

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

// pendingCount returns the number of unique paths currently buffered for
// sessionID. Called by the OTel instrumentation to populate the
// path_count_after attribute on the artifact.editfile.enqueue span (WP06).
func (b *CoalesceBuffer) pendingCount(sessionID string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sessions[sessionID])
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
// absolute path from a kenaz__edit_file call. Returns "" when
// toolName is not an edit_file variant or args are unparseable.
//
// Recognised suffixes: "__edit_file" (kenaz built-in).
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
//
// WP06: emits an artifact.editfile.enqueue span on each invocation.
// Attributes carrying user content use the private.* prefix per
// NFR-004 defence-in-depth.
func (s *sink) onPostEditFileMessage(
	ctx context.Context,
	sessionID, toolName, toolArgs string,
	buf *CoalesceBuffer,
) {
	// WP06 — OTel: start enqueue span before path extraction so the
	// span captures the full cost of this dispatch (including JSON parse).
	tracer := otel.GetTracerProvider().Tracer(editFileSyncTracerName)
	ctx, span := tracer.Start(ctx, "artifact.editfile.enqueue")
	defer span.End()

	span.SetAttributes(attribute.String("tool_name", toolName))

	absPath := ExtractEditFilePath(toolName, toolArgs)
	if absPath == "" {
		span.SetStatus(codes.Ok, "path_empty")
		return
	}

	// Dedup: only capture once per turn per path.
	if buf != nil && !buf.Add(sessionID, absPath) {
		// Count the paths currently in the buffer for the attribute.
		// We use the session's pending set size post-dedup.
		span.SetAttributes(attribute.Int("path_count_after", buf.pendingCount(sessionID)))
		span.SetStatus(codes.Ok, "coalesced")
		return
	}

	// Record path count after the add (first occurrence).
	if buf != nil {
		span.SetAttributes(attribute.Int("path_count_after", buf.pendingCount(sessionID)))
	}

	cand := captureFromEditFile(toolName, absPath)
	if cand == nil {
		s.log.Warn("artifacts.edit_file_sync.read_failed",
			"session_id", sessionID,
			"private.path", absPath)
		span.SetStatus(codes.Error, "read_failed")
		return
	}

	captured, err := s.mgr.Capture(ctx, []coreart.CaptureCandidate{*cand}, sessionID)
	if err != nil {
		s.log.Warn("artifacts.edit_file_sync.capture_failed",
			"session_id", sessionID,
			"private.path", absPath,
			"err", err.Error())
		span.SetStatus(codes.Error, "capture_failed")
		return
	}
	span.SetStatus(codes.Ok, "")
	if len(captured) > 0 {
		s.log.Info("artifacts.edit_file_sync.captured",
			"count", len(captured),
			"session_id", sessionID,
			"tool", toolName,
			"private.path", absPath,
		)
	}
}

// FlushSession drains the CoalesceBuffer for sessionID and resets it
// for the next turn. Call this at turn/run completion (OnChatRunComplete)
// so same-file edits in a subsequent turn are re-captured as new
// revisions rather than coalesced with the previous turn.
//
// WP06: emits an artifact.editfile.flush span around the drain.
// Attributes:
//   - paths_drained      — count of unique paths flushed
//   - artifacts_captured — always 0 here (capture happened at enqueue time)
//   - duration_ms        — wall-clock duration of the flush operation
func (s *sinkWithEditSync) FlushSession(ctx context.Context, sessionID string) {
	if s == nil || s.buf == nil {
		return
	}

	tracer := otel.GetTracerProvider().Tracer(editFileSyncTracerName)
	_, span := tracer.Start(ctx, "artifact.editfile.flush")
	defer span.End()

	t0 := time.Now()
	paths := s.buf.Flush(sessionID)
	durMS := time.Since(t0).Milliseconds()

	span.SetAttributes(
		attribute.Int("paths_drained", len(paths)),
		attribute.Int("artifacts_captured", 0), // capture happens at enqueue time
		attribute.Int64("duration_ms", durMS),
	)
	span.SetStatus(codes.Ok, "")

	if len(paths) > 0 {
		s.sink.log.Info("artifacts.edit_file_sync.session_flushed",
			"session_id", sessionID,
			"paths_drained", len(paths),
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
