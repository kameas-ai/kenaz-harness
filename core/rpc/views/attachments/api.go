// Package attachments is the view-scoped accessor for the
// context_attachments table. It is the rpc-layer face of
// core/attachments.Manager — the typed JSON wire shape the frontend
// consumes lives here so the package import direction stays one-way
// (rpc imports core, never the reverse).
package attachments

import "context"

// Attachment is the wire shape for one stored attachment. Timestamps
// render as RFC3339Nano UTC for byte-stable JSON.
type Attachment struct {
	ID            string `json:"id"`
	ScopeKind     string `json:"scopeKind"`
	ScopeID       string `json:"scopeId,omitempty"`
	ContentSource string `json:"contentSource"`
	Content       string `json:"content"`
	Kind          string `json:"kind"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"createdAt"`
	// MediaID, when non-empty, identifies the MediaArtifact backing
	// this attachment (multimodal-io WP02). Surfaced on the wire so the
	// frontend can resolve back to the on-disk binary via a future
	// fetch endpoint without inspecting ContentSource manually.
	MediaID string `json:"mediaId,omitempty"`
}

// AddInput is the wire shape Add accepts. Mirrors core/attachments
// fields the caller is allowed to specify; the server fills in id +
// timestamps when omitted.
type AddInput struct {
	ScopeKind     string `json:"scopeKind"`
	ScopeID       string `json:"scopeId,omitempty"`
	ContentSource string `json:"contentSource"`
	Content       string `json:"content"`
	Kind          string `json:"kind,omitempty"`
	Position      int    `json:"position,omitempty"`
}

// AddMediaInput is the wire shape AddMedia accepts. MediaBytesBase64
// is the raw base64-encoded payload; the server decodes, validates,
// and pipes it through the MediaStore CAS path (multimodal-io WP02).
type AddMediaInput struct {
	ScopeKind        string `json:"scopeKind"`
	ScopeID          string `json:"scopeId,omitempty"`
	MediaBytesBase64 string `json:"mediaBytesBase64"`
	MediaType        string `json:"mediaType"`
	OriginalName     string `json:"originalName,omitempty"`
}

// AttachmentsAPI is the view-scoped surface backing the
// context_attachments table.
type AttachmentsAPI interface {
	// List returns attachments matching scopeKind + scopeID, ordered by
	// position ASC. Empty scopeKind matches every scope; empty scopeID
	// matches every id within the chosen kind.
	List(ctx context.Context, scopeKind, scopeID string) ([]Attachment, error)
	// ListResolved returns the merged attachment stream for sessionID
	// in [global..., project..., session...] order. The project layer
	// is empty when sessionID is loose (no project membership).
	ListResolved(ctx context.Context, sessionID string) ([]Attachment, error)
	// Add inserts a new attachment and returns the persisted record.
	Add(ctx context.Context, in AddInput) (Attachment, error)
	// AddMedia decodes the base64 bytes, persists them through the
	// MediaStore CAS path, and inserts a media-backed attachment row.
	// Oversize / invalid base64 / missing media store yield an error;
	// success returns the persisted Attachment with MediaID populated.
	AddMedia(ctx context.Context, in AddMediaInput) (Attachment, error)
	// Remove deletes an attachment by id.
	Remove(ctx context.Context, id string) error
	// Reorder rewrites position for every id in idsInOrder; ids must be
	// a complete permutation of (scopeKind, scopeID).
	Reorder(ctx context.Context, scopeKind, scopeID string, idsInOrder []string) error
	// Refresh re-reads a "library:<path>" attachment's content from the
	// Context Library. inline-source attachments are a no-op.
	Refresh(ctx context.Context, id string) (Attachment, error)
}
