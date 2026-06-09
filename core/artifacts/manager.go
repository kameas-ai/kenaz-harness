package artifacts

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/attachments"
)

// MediaStorer is the narrow surface the artifacts manager needs from
// the MediaStore. attachments.MediaStore satisfies it; the interface
// keeps the manager decoupled from the on-disk + SQL details and
// mockable in tests.
type MediaStorer interface {
	Put(ctx context.Context, bytes []byte, mediaType, originalName string) (attachments.MediaArtifact, error)
}

// CaptureCandidate is what a detector emits and Manager.Capture
// consumes. The bytes ride into the MediaStore CAS at Put time; the
// metadata flows into the artifacts row that points at the same
// content_hash.
type CaptureCandidate struct {
	// Title is a human-readable label (filename for code blocks, tool
	// description for tool outputs, user-supplied for pins).
	Title string
	// MimeType drives the preview path and is recorded verbatim on
	// the artifact row.
	MimeType string
	// Bytes is the raw content. May be empty (for tool outputs that
	// reference an off-process resource) but typically non-empty.
	Bytes []byte
	// Source is one of SourceCodeBlock / SourceToolOutput /
	// SourceUserPin.
	Source string
	// SourceRef carries provenance back to the originating message /
	// tool call.
	SourceRef ArtifactSourceRef
}

// CaptureConfig carries the live tunables every capture path checks.
// Settings WP plumbs these from the user's Settings row; the detector
// library reads them on entry of each capture pass so mid-stream
// toggles take effect on the next message.
type CaptureConfig struct {
	// AutoCaptureCodeBlocks gates the code-block detector. When false
	// the detector is bypassed entirely; manual user pins still work.
	AutoCaptureCodeBlocks bool
	// CodeBlockMinLines is the lower bound for line count. A block
	// with a title hint and ≥ MinLines lines is captured even if it
	// is below MinBytes.
	CodeBlockMinLines int
	// CodeBlockMinBytes is the lower bound for byte count. A block
	// with a title hint and ≥ MinBytes bytes is captured even if it
	// is below MinLines.
	CodeBlockMinBytes int
	// AutoCaptureToolOutputs gates the tool-output detector. False
	// disables the post-tool-use capture path; user pins still work.
	AutoCaptureToolOutputs bool
	// AutoCaptureGeneratedImages gates the model-output image-capture
	// pipeline. When true, every StreamGeneratedImage event emitted by
	// the LLM adapter is fetched (when URL-only) and stored as an
	// artifact with Source==SourceModelOutput. Default true.
	// (multimodal-io-extended-01KQ8TD2 WP02)
	AutoCaptureGeneratedImages bool
	// MaxGeneratedImageBytes is the per-image byte cap for auto-capture.
	// Images whose raw byte size exceeds this cap are discarded and a
	// warning is logged; a zero value disables the cap entirely.
	// Default 20 MiB (20 * 1024 * 1024).
	// (multimodal-io-extended-01KQ8TD2 WP02)
	MaxGeneratedImageBytes int64
}

// DefaultCaptureConfig returns the spec's tuned defaults: capture on,
// 10 lines OR 200 bytes for code blocks, capture on for tool outputs,
// capture on for model-generated images with a 20 MiB per-image cap.
func DefaultCaptureConfig() CaptureConfig {
	return CaptureConfig{
		AutoCaptureCodeBlocks:      true,
		CodeBlockMinLines:          10,
		CodeBlockMinBytes:          200,
		AutoCaptureToolOutputs:     true,
		AutoCaptureGeneratedImages: true,
		MaxGeneratedImageBytes:     20 * 1024 * 1024, // 20 MiB
	}
}

// Manager orchestrates capture: bytes → MediaStore (CAS dedup) →
// artifacts row (pointing at the content_hash). Every call is
// transaction-safe at the row level: the MediaStore Put is durable
// before the artifacts row is inserted, so a crash mid-batch leaves
// at most one orphan media row pointing at a CAS file (the orphan
// sweep at boot reclaims it). Partial-success semantics: when
// candidate N fails, candidates [0,N) are returned to the caller
// alongside the error.
type Manager struct {
	store      Store
	media      MediaStorer
	sessReader SessionProjectReader

	mu  sync.RWMutex
	cfg CaptureConfig
}

// ManagerOption configures a Manager at construction.
type ManagerOption func(*Manager)

// WithSessionReader injects the session→project lookup used to default
// new artifacts' project_id from the session that produced them.
func WithSessionReader(r SessionProjectReader) ManagerOption {
	return func(m *Manager) {
		m.sessReader = r
	}
}

// WithCaptureConfig pins the initial CaptureConfig. Tests use this to
// override the defaults; production wires settings live via
// SetCaptureConfig as the user toggles values.
func WithCaptureConfig(cfg CaptureConfig) ManagerOption {
	return func(m *Manager) {
		m.cfg = cfg
	}
}

// NewManager constructs a Manager. Both store and media are required.
func NewManager(store Store, media MediaStorer, opts ...ManagerOption) *Manager {
	if store == nil {
		panic("artifacts.NewManager: nil store")
	}
	if media == nil {
		panic("artifacts.NewManager: nil media")
	}
	m := &Manager{
		store: store,
		media: media,
		cfg:   DefaultCaptureConfig(),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Config returns the live CaptureConfig.
func (m *Manager) Config() CaptureConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// SetCaptureConfig replaces the live config atomically. Future captures
// observe the new values; in-flight detectors that have already read
// the prior config finish under the prior values.
func (m *Manager) SetCaptureConfig(cfg CaptureConfig) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// WriteVersion writes a new content revision for an existing artifact.
// The bytes are pushed into the MediaStore (CAS-dedup'd) and a new row
// is appended to artifact_versions. The parent Artifact row is never
// mutated; the caller reads the updated ArtifactVersion for the new
// content_hash + byte_size.
//
// summary and path are optional; pass nil to omit them.
//
// Returns ErrArtifactNotFound if artifactID has no matching row.
func (m *Manager) WriteVersion(ctx context.Context, artifactID string, bytes []byte, mimeType string, summary, path *string) (ArtifactVersion, error) {
	if artifactID == "" {
		return ArtifactVersion{}, errors.New("artifacts: empty artifactID")
	}
	// Resolve the parent artifact to inherit its MimeType when the caller
	// doesn't supply one.
	parent, err := m.store.Get(ctx, artifactID)
	if err != nil {
		return ArtifactVersion{}, err
	}
	if mimeType == "" {
		mimeType = parent.MimeType
	}
	mediaArt, err := m.media.Put(ctx, bytes, mimeType, parent.Title)
	if err != nil {
		return ArtifactVersion{}, fmt.Errorf("artifacts: WriteVersion: media put: %w", err)
	}
	v := ArtifactVersion{
		ArtifactID:  artifactID,
		ContentHash: mediaArt.ContentHash,
		ByteSize:    mediaArt.ByteSize,
		MimeType:    mimeType,
		Summary:     summary,
		Path:        path,
	}
	return m.store.WriteVersion(ctx, v)
}

// Capture materializes a slice of candidates into artifacts. For each
// candidate the bytes are written to the MediaStore (CAS-dedup'd by
// content_hash) and an artifacts row is inserted pointing at the
// resulting content_hash. The session's project (if any) is
// looked up via the SessionProjectReader; when present, the resulting
// artifact rows carry that project_id (scope_kind defaults to
// "session" — promoting to project scope is an explicit later call).
//
// Failure model is partial success: if candidate N errors, [0,N) is
// returned alongside the error. The caller decides whether to retry,
// roll back, or accept the partial.
func (m *Manager) Capture(ctx context.Context, candidates []CaptureCandidate, sessionID string) ([]Artifact, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if sessionID == "" {
		return nil, errors.New("artifacts: empty sessionID")
	}

	var projectID *string
	if m.sessReader != nil {
		pid, err := m.sessReader.SessionProject(ctx, sessionID)
		if err == nil && pid != "" {
			v := pid
			projectID = &v
		}
	}

	out := make([]Artifact, 0, len(candidates))
	for i, c := range candidates {
		if !validSource(c.Source) {
			return out, fmt.Errorf("artifacts: candidate %d: %w: %q",
				i, ErrUnsupportedSource, c.Source)
		}
		mediaArt, err := m.media.Put(ctx, c.Bytes, c.MimeType, c.Title)
		if err != nil {
			return out, fmt.Errorf("artifacts: candidate %d: media put: %w", i, err)
		}
		row := Artifact{
			SessionID:   sessionID,
			ProjectID:   projectID,
			Title:       c.Title,
			MimeType:    c.MimeType,
			ContentHash: mediaArt.ContentHash,
			ByteSize:    mediaArt.ByteSize,
			Source:      c.Source,
			SourceRef:   c.SourceRef,
			ScopeKind:   ScopeKindSession,
		}
		inserted, err := m.store.Insert(ctx, row)
		if err != nil {
			return out, fmt.Errorf("artifacts: candidate %d: insert: %w", i, err)
		}
		out = append(out, inserted)
	}
	return out, nil
}
