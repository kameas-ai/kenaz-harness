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
// boundary in migration 0303.
const (
	SourceCodeBlock  = "code_block"
	SourceToolOutput = "tool_output"
	SourceUserPin    = "user_pin"
)

// ScopeKind enum values for Artifact.ScopeKind. Validated at the SQL
// CHECK boundary in migration 0303.
const (
	ScopeKindSession = "session"
	ScopeKindProject = "project"
)

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrArtifactNotFound is returned when an id has no matching row.
	ErrArtifactNotFound = errors.New("artifacts: not found")

	// ErrUnsupportedSource is returned when Insert receives a Source
	// outside the {code_block, tool_output, user_pin} enum.
	ErrUnsupportedSource = errors.New("artifacts: unsupported source")

	// ErrUnsupportedScope is returned when UpdateScope receives a kind
	// outside {session, project}, or when a session→project promote
	// targets a session that has no project.
	ErrUnsupportedScope = errors.New("artifacts: unsupported scope")
)
