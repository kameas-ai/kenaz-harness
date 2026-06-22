// Package artifacts owns the harness's "things the model produced for
// you" surface. Three sources feed the table: fenced code blocks with
// filename hints in assistant text, file-shaped tool outputs (image /
// resource / content-array MCP shapes), and explicit user pins. Every
// artifact's bytes ride on the shared CAS at <DataDir>/media/<sha256>
// laid down by the multimodal-io mission; the artifacts package
// participates in MediaStore's composite refcount via
// ArtifactsRefcountSource so the on-disk file is only swept when no
// attachments AND no artifacts row reference the hash.
//
// DIRECTIVE_001: this package is the single owner of the artifacts
// table; all read/write access from rpc/views and hooks must go through
// the public Manager API.
package artifacts

import (
	"errors"
	"time"
)

// Artifact is the durable metadata row for one captured output. The
// bytes themselves live at <DataDir>/media/<ContentHash>; the row
// carries provenance (Source + SourceRef) so the UI can render a
// backlink to the message / tool call that produced it.
type Artifact struct {
	// ID is a 26-char Crockford-base32 ULID minted at Insert time.
	ID string
	// SessionID is the session this artifact was captured under. FK
	// to sessions.id with ON DELETE CASCADE — deleting the session
	// removes its artifacts.
	SessionID string
	// ProjectID is the project the artifact is rolled up under, or nil
	// for session-only artifacts. FK ON DELETE SET NULL — deleting
	// the project demotes the artifact's project link without losing
	// the row.
	ProjectID *string
	// Title is a human-readable label. For code blocks, the captured
	// filename hint; for tool outputs, the tool name + index; for
	// user pins, the user-supplied title.
	Title string
	// MimeType is the IANA MIME type recorded by the capture path
	// (inferred from filename extension for code blocks, from the
	// tool result envelope for tool outputs).
	MimeType string
	// ContentHash is the hex sha256 of the raw bytes; matches the file
	// name on disk under <DataDir>/media/.
	ContentHash string
	// ByteSize is the on-disk size in bytes.
	ByteSize int64
	// Source enumerates how the artifact was captured.
	Source string
	// SourceRef carries provenance back to the originating message
	// and tool call. Stored as JSON in source_ref_json.
	SourceRef ArtifactSourceRef
	// ScopeKind is "session" (default) or "project" (after promote).
	ScopeKind string
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time
}

// ArtifactSourceRef carries provenance back to the message / tool call
// that produced an artifact. Persisted as JSON in source_ref_json.
type ArtifactSourceRef struct {
	// MessageID is the assistant message the capture fired against.
	// Always present.
	MessageID string `json:"message_id"`
	// ToolCallID is set for Source=="tool_output" captures.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CodeBlockIndex is the 0-based index of the captured fenced
	// block within the message. Set for Source=="code_block".
	CodeBlockIndex int `json:"code_block_index,omitempty"`
	// Filename is the title hint extracted from the block (sanitized
	// for display — path separators replaced).
	Filename string `json:"filename,omitempty"`
	// AbsolutePath is the canonical on-disk path when the artifact
	// originated from a kaneaz__edit_file / kaneaz__write_file call
	// (edit-file-artifact-sync-01KQ8TD5 WP01). Empty for all other
	// sources. When set, the Artifacts tab can render a "Show in Finder /
	// Open in editor" affordance pointing at the live file on disk.
	AbsolutePath string `json:"absolute_path,omitempty"`
	// ImageIndex is the 0-based index of the generated image within the
	// model response. Set for Source=="model_output" when the model
	// returned multiple images in a single response (e.g. DALL-E 3
	// n>1). Zero for the first (or only) image.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	ImageIndex int `json:"image_index,omitempty"`
	// RevisedPrompt carries the model-edited prompt returned by some
	// image-generation providers (e.g. DALL-E 3 prompt revision). Empty
	// when the model did not revise the prompt.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ArtifactVersion is one entry in the append-only revision history for
// an artifact. Written by kaneaz__update_artifact; the parent Artifact
// row is never mutated. The actual bytes live in the shared CAS at
// <DataDir>/media/<ContentHash> — same location as the original capture.
//
// Version starts at 1 for the first update. The (ArtifactID, Version)
// pair is unique.
type ArtifactVersion struct {
	// ID is the auto-incrementing row id.
	ID int64
	// ArtifactID is the parent artifact this version belongs to.
	ArtifactID string
	// Version is the monotonically increasing revision number scoped per
	// artifact (first update = 1).
	Version int
	// ContentHash is the hex sha256 of the updated bytes; matches the
	// file name under <DataDir>/media/.
	ContentHash string
	// ByteSize is the on-disk size in bytes of this revision.
	ByteSize int64
	// MimeType is the MIME type of this revision. May differ from the
	// parent artifact's MimeType if the content type changed.
	MimeType string
	// Summary is an optional editor-supplied description of the change
	// (e.g. "fixed off-by-one in line 42"). Nil when not provided.
	Summary *string
	// Path is the optional updated canonical path for file-source
	// artifacts. Nil means "same path as the parent's AbsolutePath".
	Path *string
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time
}

// ArtifactFilter narrows the List query. Empty fields match every row.
type ArtifactFilter struct {
	// SessionID restricts the result set to artifacts produced under
	// one session. Empty = all sessions.
	SessionID string
	// ProjectID restricts the result set to artifacts rolled up under
	// one project. Empty = all projects (nullable artifacts included).
	ProjectID string
	// MimeTypePrefix matches MimeType by prefix. "image/" filters all
	// images; "" = all.
	MimeTypePrefix string
	// Source restricts to one of SourceCodeBlock / SourceToolOutput /
	// SourceUserPin. Empty = all sources.
	Source string
	// ScopeKind restricts to "session" or "project". Empty = both.
	ScopeKind string
}

// Source enum values for Artifact.Source. Validated at the SQL CHECK
// boundary in migration 0303 (extended to include model_output by
// migration 0327 — multimodal-io-extended-01KQ8TD2 WP02).
const (
	SourceCodeBlock    = "code_block"
	SourceToolOutput   = "tool_output"
	SourceUserPin      = "user_pin"
	// SourceModelOutput is set when the artifact originates from a
	// model-generated image (e.g. DALL-E 3, gpt-image-1, Titan Image).
	// The SourceRef.ImageIndex field records the 0-based image position
	// within the response; the SourceRef.MessageID ties the artifact to
	// the assistant message that requested the image.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	SourceModelOutput  = "model_output"
)

// ScopeKind enum values for Artifact.ScopeKind. Validated at the SQL
// CHECK boundary in migration 0303 (extended to include "global" by
// migration 0332 — unified-context-artifacts-01NCTXU01 additive scope
// widening).
const (
	ScopeKindSession = "session"
	ScopeKindProject = "project"
	// ScopeKindGlobal makes an artifact visible across all sessions and
	// projects. Added in migration 0332 so global artifacts can be
	// surfaced as Units in the unified context+artifacts store.
	// ScopeID is empty for global-scope artifacts.
	ScopeKindGlobal = "global"
)

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrArtifactNotFound is returned when an id has no matching row.
	ErrArtifactNotFound = errors.New("artifacts: not found")

	// ErrUnsupportedSource is returned when Insert receives a Source
	// outside the {code_block, tool_output, user_pin} enum.
	ErrUnsupportedSource = errors.New("artifacts: unsupported source")

	// ErrUnsupportedScope is returned when UpdateScope receives a kind
	// outside {session, project, global}, or when a session→project
	// promote targets a session that has no project.
	ErrUnsupportedScope = errors.New("artifacts: unsupported scope")

	// ErrVersionConflict is returned when WriteVersion detects that the
	// (artifact_id, version) pair already exists in artifact_versions.
	// Callers should retry with the next version number.
	ErrVersionConflict = errors.New("artifacts: version conflict")
)
