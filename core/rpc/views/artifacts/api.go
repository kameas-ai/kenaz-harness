// Package artifacts is the view-scoped accessor for the artifacts
// table. It is the rpc-layer face of core/artifacts.Manager — the
// typed JSON wire shape the frontend consumes lives here so the
// package import direction stays one-way (rpc imports core, never
// the reverse).
package artifacts

import (
	"context"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
)

// Artifact is the wire shape for one stored artifact. Timestamps
// render as RFC3339Nano UTC for byte-stable JSON.
type Artifact struct {
	ID          string            `json:"id"`
	SessionID   string            `json:"sessionId"`
	ProjectID   string            `json:"projectId,omitempty"`
	Title       string            `json:"title"`
	MimeType    string            `json:"mimeType"`
	ContentHash string            `json:"contentHash"`
	ByteSize    int64             `json:"byteSize"`
	Source      string            `json:"source"`
	SourceRef   ArtifactSourceRef `json:"sourceRef"`
	ScopeKind   string            `json:"scopeKind"`
	CreatedAt   string            `json:"createdAt"`
}

// ArtifactSourceRef carries provenance back to the originating
// message / tool call so the UI can render a backlink chip.
type ArtifactSourceRef struct {
	MessageID      string `json:"messageId,omitempty"`
	ToolCallID     string `json:"toolCallId,omitempty"`
	CodeBlockIndex int    `json:"codeBlockIndex,omitempty"`
	Filename       string `json:"filename,omitempty"`
	// AbsolutePath is the on-disk path when the artifact originated from
	// a kaneaz__edit_file / kaneaz__write_file call
	// (edit-file-artifact-sync-01KQ8TD5 WP01). Enables "Show in Finder"
	// affordances in the Artifacts tab.
	AbsolutePath string `json:"absolutePath,omitempty"`
}

// ArtifactFilter narrows the List query.
type ArtifactFilter struct {
	SessionID      string `json:"sessionId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	MimeTypePrefix string `json:"mimeTypePrefix,omitempty"`
	Source         string `json:"source,omitempty"`
	ScopeKind      string `json:"scopeKind,omitempty"`
}

// ArtifactWithBytes is the wire envelope returned by Get. Bytes ride
// over the boundary as the raw []byte slice; Wails JSON-encodes it
// as a base64 string so the frontend receives a directly-usable blob.
type ArtifactWithBytes struct {
	Artifact Artifact `json:"artifact"`
	Bytes    []byte   `json:"bytes"`
}

// ArtifactsAPI is the view-scoped surface backing the artifacts table.
type ArtifactsAPI interface {
	// List returns artifacts matching filter, ordered by created_at
	// DESC.
	List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error)
	// Get returns the metadata + on-disk bytes for one id. The bytes
	// ride the wire as base64 (Wails encodes []byte automatically).
	Get(ctx context.Context, id string) (ArtifactWithBytes, error)
	// Promote moves an artifact's scope. newScopeKind is "session" or
	// "project"; newScopeID is the project id when promoting (empty
	// when demoting). Returns the updated row.
	Promote(ctx context.Context, id, newScopeKind, newScopeID string) (Artifact, error)
	// Delete removes an artifact by id. Refcount-aware: when no other
	// row references the content_hash the on-disk CAS file is also
	// removed.
	Delete(ctx context.Context, id string) error
	// SaveFromMessage manually pins a message (or a sub-range thereof)
	// as a user-pin artifact. sourceRangeStart/End are byte offsets
	// into the message's content; (0, 0) captures the full message.
	SaveFromMessage(ctx context.Context, sessionID, messageID, title string, sourceRangeStart, sourceRangeEnd int) (Artifact, error)
}

// MessageReader is the narrow read surface SaveFromMessage uses to
// pull the message text for a given (session, message) pair. The rpc
// layer wires this against core/session.Manager via an adapter.
type MessageReader interface {
	GetMessage(ctx context.Context, sessionID, messageID string) (Message, error)
}

// Message is the slice of session.Message SaveFromMessage consumes.
// Defined here so the artifacts view does not import core/session
// directly (DIRECTIVE_001 + import-cycle hygiene).
type Message struct {
	ID        string
	SessionID string
	Role      string
	Content   string
}

// ArtifactSink is the LLM impl's hook into the code-block detector.
// The concrete sink (impl: NewSink) lives in this package; the
// interface is exported here so callers can wire it into
// llm.Config.Artifacts.
//
// Mirrors llm.ArtifactSink — duplicated to keep the import direction
// one-way. The rpc chassis adapts between them via a tiny shim.
type ArtifactSink interface {
	OnAssistantMessage(ctx context.Context, sessionID, messageID, text string) error
}

// CaptureConfigReader returns the live CaptureConfig (settings →
// detector tunables). The sink reads on entry of every
// OnAssistantMessage invocation so a mid-session settings toggle takes
// effect on the next message without a process restart.
type CaptureConfigReader func() coreart.CaptureConfig

// GeneratedImageCapturer is the narrow surface the LLM provider adapter
// uses to capture model-generated images into the artifact store.
// The chat runner wires a concrete implementation into the adapter at
// StartStream time; tests substitute a fake.
// (multimodal-io-extended-01KQ8TD2 WP02)
type GeneratedImageCapturer interface {
	// OnGeneratedImage captures one model-generated image as a
	// SourceModelOutput artifact. sessionID is the active chat session;
	// messageID is the assistant message that requested the image;
	// payload carries the raw bytes (or a URL to fetch), the MIME type,
	// and the 0-based image index. Returns nil on success.
	OnGeneratedImage(ctx context.Context, sessionID, messageID string, payload GeneratedImagePayload) error
}

// GeneratedImagePayload carries one model-generated image to the
// capture pipeline. Mirrors core/llm.GeneratedImagePayload without
// importing the llm package (import-direction hygiene).
type GeneratedImagePayload struct {
	// Data is the raw image bytes. When non-nil, it is stored directly.
	// When nil and URL is non-empty, the pipeline fetches the URL.
	Data []byte
	// URL is a signed provider URL returned when Data is empty.
	// The pipeline fetches it with a bounded timeout.
	URL string
	// MimeType is the IANA MIME type of the image (e.g. "image/png").
	MimeType string
	// RevisedPrompt is the model-edited prompt returned by some
	// providers (e.g. DALL-E 3). Empty when not revised.
	RevisedPrompt string
	// Index is the 0-based image position within the response.
	Index int
}
