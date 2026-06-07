package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
)

// ErrInvalidRange is returned when SaveFromMessage receives a
// (start, end) pair that does not bracket a valid substring of the
// message content.
var ErrInvalidRange = errors.New("rpc/artifacts: invalid source range")

// API is the concrete ArtifactsAPI implementation. It coordinates the
// artifacts store, the artifacts manager (for capture orchestration
// + media writes), the attachments MediaStore (for byte fetch +
// refcount-aware delete), and a session message reader (for
// SaveFromMessage).
type API struct {
	store    coreart.Store
	mgr      *coreart.Manager
	media    attachments.MediaStore
	messages MessageReader
	dataDir  string
}

// Config bundles the API's collaborators.
type Config struct {
	// Store is the artifacts store (required).
	Store coreart.Store
	// Manager is the capture orchestrator (required for
	// SaveFromMessage).
	Manager *coreart.Manager
	// Media is the MediaStore that owns the on-disk CAS
	// (required for Get + refcount-aware Delete).
	Media attachments.MediaStore
	// Messages resolves message text by (sessionID, messageID)
	// for SaveFromMessage. nil disables SaveFromMessage.
	Messages MessageReader
	// DataDir is the harness data directory; the on-disk CAS
	// lives at <DataDir>/media/<content_hash>. Required for Delete
	// to reclaim the file when refcount hits zero.
	DataDir string
}

// New constructs the API.
func New(cfg Config) *API {
	if cfg.Store == nil {
		panic("rpc/artifacts: nil store")
	}
	return &API{
		store:    cfg.Store,
		mgr:      cfg.Manager,
		media:    cfg.Media,
		messages: cfg.Messages,
		dataDir:  cfg.DataDir,
	}
}

// List implements ArtifactsAPI.
func (a *API) List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error) {
	rows, err := a.store.List(ctx, coreart.ArtifactFilter{
		SessionID:      filter.SessionID,
		ProjectID:      filter.ProjectID,
		MimeTypePrefix: filter.MimeTypePrefix,
		Source:         filter.Source,
		ScopeKind:      filter.ScopeKind,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Artifact, 0, len(rows))
	for _, r := range rows {
		out = append(out, artifactToView(r))
	}
	return out, nil
}

// Get implements ArtifactsAPI. Pulls metadata from the store, then
// resolves the on-disk bytes through the MediaStore by content hash.
func (a *API) Get(ctx context.Context, id string) (ArtifactWithBytes, error) {
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return ArtifactWithBytes{}, err
	}
	if a.media == nil {
		return ArtifactWithBytes{}, errors.New("rpc/artifacts: media store not wired")
	}
	_, body, err := a.media.GetByHash(ctx, row.ContentHash)
	if err != nil {
		return ArtifactWithBytes{}, fmt.Errorf("rpc/artifacts: load bytes: %w", err)
	}
	return ArtifactWithBytes{
		Artifact: artifactToView(row),
		Bytes:    body,
	}, nil
}

// Promote implements ArtifactsAPI. Routes through the store's
// UpdateScope, which validates session→project membership.
func (a *API) Promote(ctx context.Context, id, newScopeKind, newScopeID string) (Artifact, error) {
	updated, err := a.store.UpdateScope(ctx, id, newScopeKind, newScopeID)
	if err != nil {
		return Artifact{}, err
	}
	return artifactToView(updated), nil
}

// Delete implements ArtifactsAPI. Removes the row, then sweeps the
// orphan CAS file when no other reference remains. Refcount accounting:
//
//   - Each Capture path goes through MediaStore.Put which inserts one
//     media_artifacts row per upload. The composite refcount sums
//     (media metadata rows) + (attachments source) + (artifacts source).
//   - When the user deletes an artifact we MUST also drop the
//     media_artifacts row created at Capture time — otherwise the
//     metadata count keeps the file pinned forever.
//
// Strategy: delete the artifact row, then walk every media_artifacts
// row pointing at the same content hash and Delete each one whose
// only remaining reference is itself. After every metadata row for
// this hash is gone the composite refcount drops to whatever the
// other sources contribute; if that's zero we reclaim the on-disk
// file. PruneOrphans handles the actual removal idempotently.
//
// Refcount race protection: two simultaneous deletes for rows
// pointing at the same hash both see zero after the row deletes;
// os.Remove (and PruneOrphans) is idempotent so the second call
// drops to a no-op.
func (a *API) Delete(ctx context.Context, id string) error {
	contentHash, err := a.store.Delete(ctx, id)
	if err != nil {
		return err
	}
	if a.media == nil || a.dataDir == "" || contentHash == "" {
		return nil
	}
	// Drop every media_artifacts row pointing at this hash whose
	// non-self refcount is zero (i.e. only this metadata row holds
	// it open). We sample non-self refcount by computing
	// composite − metaCount where metaCount is the row count for
	// this hash; that lets us decide before mutating.
	mediaRows, err := a.media.List(ctx, attachments.MediaFilter{ContentHash: contentHash})
	if err != nil {
		return fmt.Errorf("rpc/artifacts: list media rows: %w", err)
	}
	if shouldSweep, err := a.nonSelfRefcountIsZero(ctx, contentHash, len(mediaRows)); err != nil {
		return err
	} else if shouldSweep {
		for _, m := range mediaRows {
			if derr := a.media.Delete(ctx, m.ID); derr != nil {
				return fmt.Errorf("rpc/artifacts: delete media row: %w", derr)
			}
		}
		path := filepath.Join(a.dataDir, "media", contentHash)
		if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("rpc/artifacts: remove cas file: %w", rerr)
		}
	}
	return nil
}

// nonSelfRefcountIsZero reports whether nothing OTHER than the
// MediaStore's own metadata rows references the supplied content
// hash. Used by Delete to decide whether to sweep the orphan
// metadata + on-disk file.
func (a *API) nonSelfRefcountIsZero(ctx context.Context, contentHash string, mediaRowCount int) (bool, error) {
	total, err := a.media.RefcountFor(ctx, contentHash)
	if err != nil {
		return false, fmt.Errorf("rpc/artifacts: refcount: %w", err)
	}
	return total <= mediaRowCount, nil
}

// SaveFromMessage implements ArtifactsAPI. Pulls the message text via
// the wired MessageReader, slices to the supplied range when
// non-zero, and routes the result through Manager.Capture as a
// user_pin candidate.
func (a *API) SaveFromMessage(
	ctx context.Context,
	sessionID, messageID, title string,
	sourceRangeStart, sourceRangeEnd int,
) (Artifact, error) {
	if a.mgr == nil {
		return Artifact{}, errors.New("rpc/artifacts: manager not wired")
	}
	if a.messages == nil {
		return Artifact{}, errors.New("rpc/artifacts: message reader not wired")
	}
	msg, err := a.messages.GetMessage(ctx, sessionID, messageID)
	if err != nil {
		return Artifact{}, err
	}
	text := msg.Content
	if !(sourceRangeStart == 0 && sourceRangeEnd == 0) {
		if sourceRangeStart < 0 || sourceRangeEnd < sourceRangeStart || sourceRangeEnd > len(text) {
			return Artifact{}, fmt.Errorf("%w: [%d,%d) over %d", ErrInvalidRange,
				sourceRangeStart, sourceRangeEnd, len(text))
		}
		text = text[sourceRangeStart:sourceRangeEnd]
	}
	if title == "" {
		// Default title: first 60 chars of the slice (matching FR-006).
		if len(text) > 60 {
			title = text[:60]
		} else {
			title = text
		}
	}
	candidate := coreart.CaptureCandidate{
		Title:    title,
		MimeType: "text/markdown",
		Bytes:    []byte(text),
		Source:   coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{
			MessageID: messageID,
			Filename:  title,
		},
	}
	captured, err := a.mgr.Capture(ctx, []coreart.CaptureCandidate{candidate}, sessionID)
	if err != nil {
		return Artifact{}, err
	}
	if len(captured) == 0 {
		return Artifact{}, errors.New("rpc/artifacts: capture returned no rows")
	}
	return artifactToView(captured[0]), nil
}

// artifactToView projects core/artifacts.Artifact into the wire
// shape.
func artifactToView(a coreart.Artifact) Artifact {
	out := Artifact{
		ID:          a.ID,
		SessionID:   a.SessionID,
		Title:       a.Title,
		MimeType:    a.MimeType,
		ContentHash: a.ContentHash,
		ByteSize:    a.ByteSize,
		Source:      a.Source,
		ScopeKind:   a.ScopeKind,
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339Nano),
		SourceRef: ArtifactSourceRef{
			MessageID:      a.SourceRef.MessageID,
			ToolCallID:     a.SourceRef.ToolCallID,
			CodeBlockIndex: a.SourceRef.CodeBlockIndex,
			Filename:       a.SourceRef.Filename,
		},
	}
	if a.ProjectID != nil {
		out.ProjectID = *a.ProjectID
	}
	return out
}

// Compile-time witness.
var _ ArtifactsAPI = (*API)(nil)
