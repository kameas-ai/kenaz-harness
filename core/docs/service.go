package docs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// The document store (spec 092 FR-001..FR-004).
//
// A document is a core/units Unit with Kind "doc". This file is the only
// writer of doc-kind bodies: every create and every update passes Sanitize
// before the unit store sees a byte, so the store invariant documented at
// the top of sanitize.go holds for everything Service returns.
//
// # Classification is frozen at personal
//
// Plan decision R-7: every document Service creates is ClassPersonal, and
// Service refuses to read or update a doc unit carrying any other
// classification. A team or org classification makes a unit sync-eligible
// (units.Manager.ListDirty), and document sync is spec 092 US8 — §IX
// territory that has not been specified. Freezing here, in the one writer,
// is what makes the freeze a property of the product rather than of each
// call site.
//
// # Visibility
//
// A session-scoped document is visible only to the session that owns it;
// a global document is visible to every session. Project scope is not
// resolved here because no caller of this package has a project id to
// offer yet. A document the caller cannot see is reported as
// ErrDocumentNotFound — never as a distinct "forbidden" — so a model in
// one session cannot probe for the existence of another session's work.

// FormatHTML is the only body format a document has in v1.
const FormatHTML = "text/html"

// Provenance author kinds (FR-002). AuthorModelOutput is a body produced by
// a model tool call; AuthorUserEdit is a body a person wrote through the
// Documents UI. The model-assisted kind arrives with inline AI actions
// (FR-009) and is not declared until something writes it.
const (
	AuthorModelOutput = "model_output"
	AuthorUserEdit    = "user_edit"
)

// Errors returned by Service. Callers map them to wire codes with
// ServiceErrorCode.
var (
	ErrDocumentNotFound = errors.New("docs: document not found")
	ErrInvalidTitle     = errors.New("docs: invalid title")
	ErrEmptyBody        = errors.New("docs: empty body")
	ErrNoSession        = errors.New("docs: no session")
	ErrVersionConflict  = errors.New("docs: version conflict")
)

// Wire codes for Service errors, extending the sanitizer's codes in
// limits.go.
const (
	CodeNotFound        = "document_not_found"
	CodeInvalidTitle    = "invalid_title"
	CodeEmptyBody       = "empty_body"
	CodeNoSession       = "no_session"
	CodeVersionConflict = "version_conflict"
)

// ServiceErrorCode maps any error Service returns — including sanitizer
// errors — onto a stable wire code. nil maps to "".
func ServiceErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrDocumentNotFound):
		return CodeNotFound
	case errors.Is(err, ErrInvalidTitle):
		return CodeInvalidTitle
	case errors.Is(err, ErrEmptyBody):
		return CodeEmptyBody
	case errors.Is(err, ErrNoSession):
		return CodeNoSession
	case errors.Is(err, ErrVersionConflict), errors.Is(err, units.ErrVersionConflict):
		return CodeVersionConflict
	default:
		return ErrorCode(err)
	}
}

// UnitStore is the slice of *units.Manager the document store needs.
type UnitStore interface {
	Create(ctx context.Context, u units.Unit) (units.Unit, error)
	Get(ctx context.Context, id string) (units.Unit, error)
	List(ctx context.Context, filter units.UnitFilter) ([]units.Unit, error)
	UpdateAtVersion(ctx context.Context, id string, baseVersion int, body string, metadata json.RawMessage) (units.Unit, error)
}

// Provenance records who produced one version of a document body (FR-002).
type Provenance struct {
	AuthorKind string `json:"author_kind"`
	SessionID  string `json:"session_id,omitempty"`
	// Tool is the builtin tool that wrote the version, when one did.
	Tool string `json:"tool,omitempty"`
}

// metadata is the JSON object stored in a doc unit's Metadata column. It
// carries a hash of the body rather than anything derived from its text,
// so the column is safe to surface in audit and sync bookkeeping.
type metadata struct {
	Format        string     `json:"format"`
	ContentSHA256 string     `json:"content_sha256"`
	ByteSize      int        `json:"byte_size"`
	Provenance    Provenance `json:"provenance"`
}

// Document is the read model Service returns.
type Document struct {
	ID            string
	Title         string
	Scope         units.Scope
	ScopeID       string
	Version       int
	Body          string
	ContentSHA256 string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Service is the document store. Safe for concurrent use to the extent
// the underlying UnitStore is.
type Service struct {
	store UnitStore
}

// NewService constructs a Service. store is required.
func NewService(store UnitStore) *Service {
	if store == nil {
		panic("docs.NewService: nil store")
	}
	return &Service{store: store}
}

// Save sanitizes body and creates a new session-scoped, personal document
// owned by sessionID.
func (s *Service) Save(ctx context.Context, sessionID, title, body string, prov Provenance) (Document, error) {
	if sessionID == "" {
		return Document{}, ErrNoSession
	}
	title, err := normaliseTitle(title)
	if err != nil {
		return Document{}, err
	}
	clean, meta, err := sanitizeBody(body, prov)
	if err != nil {
		return Document{}, err
	}
	u, err := s.store.Create(ctx, units.Unit{
		Kind:           units.KindDoc,
		Scope:          units.ScopeSession,
		ScopeID:        sessionID,
		Classification: units.ClassPersonal,
		LoadPolicy:     units.LoadOnDemand,
		Title:          title,
		Body:           clean,
		Metadata:       meta,
	})
	if err != nil {
		return Document{}, fmt.Errorf("docs: create: %w", err)
	}
	return toDocument(u), nil
}

// Update sanitizes body and writes it as the next version of document id.
// baseVersion, when non-negative, must equal the document's current
// version; a mismatch returns ErrVersionConflict so a writer working from
// a stale read cannot silently overwrite a newer version.
func (s *Service) Update(ctx context.Context, sessionID, id string, baseVersion int, body string, prov Provenance) (Document, error) {
	current, err := s.visible(ctx, sessionID, id)
	if err != nil {
		return Document{}, err
	}
	if baseVersion >= 0 && baseVersion != current.Version {
		return Document{}, ErrVersionConflict
	}
	clean, meta, err := sanitizeBody(body, prov)
	if err != nil {
		return Document{}, err
	}
	u, err := s.store.UpdateAtVersion(ctx, id, current.Version, clean, meta)
	if err != nil {
		return Document{}, fmt.Errorf("docs: update: %w", err)
	}
	return toDocument(u), nil
}

// Get returns document id if sessionID can see it.
func (s *Service) Get(ctx context.Context, sessionID, id string) (Document, error) {
	u, err := s.visible(ctx, sessionID, id)
	if err != nil {
		return Document{}, err
	}
	return toDocument(u), nil
}

// ListVisible returns every document sessionID can see: its own
// session-scoped documents followed by global documents, each group newest
// first (the unit store's order).
func (s *Service) ListVisible(ctx context.Context, sessionID string) ([]Document, error) {
	if sessionID == "" {
		return nil, ErrNoSession
	}
	var out []Document
	for _, f := range []units.UnitFilter{
		{Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: sessionID, Classification: units.ClassPersonal},
		{Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassPersonal},
	} {
		us, err := s.store.List(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("docs: list: %w", err)
		}
		for _, u := range us {
			out = append(out, toDocument(u))
		}
	}
	return out, nil
}

// visible fetches id and applies the kind, classification, and visibility
// rules. Every refusal is ErrDocumentNotFound.
func (s *Service) visible(ctx context.Context, sessionID, id string) (units.Unit, error) {
	if sessionID == "" {
		return units.Unit{}, ErrNoSession
	}
	if id == "" {
		return units.Unit{}, ErrDocumentNotFound
	}
	u, err := s.store.Get(ctx, id)
	if errors.Is(err, units.ErrUnitNotFound) {
		return units.Unit{}, ErrDocumentNotFound
	}
	if err != nil {
		return units.Unit{}, fmt.Errorf("docs: get: %w", err)
	}
	if u.Kind != units.KindDoc || u.Classification != units.ClassPersonal {
		return units.Unit{}, ErrDocumentNotFound
	}
	switch u.Scope {
	case units.ScopeGlobal:
	case units.ScopeSession:
		if u.ScopeID != sessionID {
			return units.Unit{}, ErrDocumentNotFound
		}
	default:
		return units.Unit{}, ErrDocumentNotFound
	}
	return u, nil
}

func sanitizeBody(body string, prov Provenance) (string, json.RawMessage, error) {
	if strings.TrimSpace(body) == "" {
		return "", nil, ErrEmptyBody
	}
	clean, err := Sanitize(body)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(clean) == "" {
		// Everything in the body was active or external content.
		return "", nil, ErrEmptyBody
	}
	sum := sha256.Sum256([]byte(clean))
	meta, err := json.Marshal(metadata{
		Format:        FormatHTML,
		ContentSHA256: hex.EncodeToString(sum[:]),
		ByteSize:      len(clean),
		Provenance:    prov,
	})
	if err != nil {
		return "", nil, fmt.Errorf("docs: marshal metadata: %w", err)
	}
	return clean, meta, nil
}

func normaliseTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > MaxTitleRunes {
		return "", ErrInvalidTitle
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return "", ErrInvalidTitle
		}
	}
	return title, nil
}

func toDocument(u units.Unit) Document {
	var m metadata
	// A body written by Service always has parseable metadata; a unit
	// with unparseable metadata still reads, with an empty hash.
	_ = json.Unmarshal(u.Metadata, &m)
	return Document{
		ID:            u.ID,
		Title:         u.Title,
		Scope:         u.Scope,
		ScopeID:       u.ScopeID,
		Version:       u.Version,
		Body:          u.Body,
		ContentSHA256: m.ContentSHA256,
		CreatedAt:     u.CreatedAt,
		UpdatedAt:     u.UpdatedAt,
	}
}
