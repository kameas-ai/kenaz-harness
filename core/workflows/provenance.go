package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Install provenance (install-framework-01DOGF0B WP05 review, H1/H2/H4).
//
// Every workflow the install framework installs — a shipped template via
// the workflow catalog, or a fleet catalog payload via the workflows view's
// InstallDocument — gets a provenance record: where it came from and the
// version installed. Two decisions read it:
//
//   - Collision refusal (H1/H2): a fleet payload may create a new workflow
//     id, or update a row whose provenance is THAT catalog item. It may
//     never overwrite a user workflow, a shipped template, or another
//     catalog item's workflow — without provenance there is no proof of
//     ownership, so an existing row with none is a collision.
//   - Template updates (H4): an installed template whose recorded shipped
//     version differs from the binary's is "installed_outdated".
//
// A side file, not a column: the workflows table (migration 0319) carries
// no metadata, and a new session migration would interleave with sibling
// missions' reserved numbers. The record is advisory metadata keyed by
// workflow id; deleting the workflow deletes the record.

// Provenance sources.
const (
	ProvenanceBuiltin = "builtin"
	ProvenanceCatalog = "catalog"
	// ProvenanceMandated marks a fleet catalog workflow installed because
	// the org MANDATED it (bundle mandated_items, owner ruling 2026-10-06
	// WP02). Reconciliation removes it when the mandate is withdrawn — and
	// only while its provenance still says so.
	ProvenanceMandated = "mandated"
)

// InstallProvenance records where an installed workflow came from.
type InstallProvenance struct {
	WorkflowID string `json:"workflow_id"`
	// Source is ProvenanceBuiltin, ProvenanceCatalog or ProvenanceMandated.
	Source string `json:"source"`
	// CatalogID is the fleet catalog item id (catalog installs only).
	CatalogID string `json:"catalog_id,omitempty"`
	// Slug is the id the source advertised (the template id, or the
	// catalog item's slug).
	Slug string `json:"slug,omitempty"`
	// Version is the source version installed: "v<N>" of the shipped
	// template, or the catalog item version.
	Version     string    `json:"version,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

// ProvenanceStore persists InstallProvenance records. Safe for concurrent use.
type ProvenanceStore interface {
	Get(workflowID string) (InstallProvenance, bool, error)
	Put(p InstallProvenance) error
	Remove(workflowID string) error
	// List returns every record (any order) — the revocation sweep's
	// enumeration of catalog installs (skill-library-01SKLIB01 WP03).
	List() ([]InstallProvenance, error)
}

// provenanceFileName is the side file under the data directory.
const provenanceFileName = "workflows/install_provenance.json"

type fileProvenanceStore struct {
	mu     sync.Mutex
	path   string // "" = in-memory only
	recs   map[string]InstallProvenance
	load   bool
	warned bool
}

// fail logs a provenance failure at WARN the first time it is seen (a
// corrupt or unreadable file is otherwise permanent and silent: every
// collision check fails closed and no template update is offered) and
// returns err.
func (s *fileProvenanceStore) fail(op string, err error) error {
	if !s.warned {
		s.warned = true
		logging.L().Warn("workflows.install_provenance.unusable",
			"op", op, "path", s.path, "err", err.Error(),
			"effect", "fleet workflow installs over existing ids are refused and template updates are not offered until the file is repaired or removed")
	}
	return err
}

// NewFileProvenanceStore returns a store persisted at
// <dataDir>/workflows/install_provenance.json (atomic rewrite on every
// change). An empty dataDir yields an in-memory store.
func NewFileProvenanceStore(dataDir string) ProvenanceStore {
	s := &fileProvenanceStore{recs: map[string]InstallProvenance{}}
	if dataDir != "" {
		s.path = filepath.Join(dataDir, provenanceFileName)
	}
	return s
}

// NewMemoryProvenanceStore returns an unpersisted store (tests, chassis
// without a data directory).
func NewMemoryProvenanceStore() ProvenanceStore { return NewFileProvenanceStore("") }

func (s *fileProvenanceStore) ensureLoaded() error {
	if s.load || s.path == "" {
		s.load = true
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.load = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("workflows: read install provenance: %w", err)
	}
	var wire struct {
		Records []InstallProvenance `json:"records"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("workflows: decode install provenance: %w", err)
	}
	for _, r := range wire.Records {
		s.recs[r.WorkflowID] = r
	}
	s.load = true
	return nil
}

func (s *fileProvenanceStore) persist() error {
	if s.path == "" {
		return nil
	}
	wire := struct {
		Records []InstallProvenance `json:"records"`
	}{}
	for _, r := range s.recs {
		wire.Records = append(wire.Records, r)
	}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("workflows: mkdir install provenance: %w", err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", s.path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("workflows: write install provenance: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflows: commit install provenance: %w", err)
	}
	return nil
}

// Get implements ProvenanceStore.
func (s *fileProvenanceStore) Get(id string) (InstallProvenance, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return InstallProvenance{}, false, s.fail("get", err)
	}
	r, ok := s.recs[id]
	return r, ok, nil
}

// Put implements ProvenanceStore.
func (s *fileProvenanceStore) Put(p InstallProvenance) error {
	if p.WorkflowID == "" {
		return fmt.Errorf("workflows: install provenance: empty workflow id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return s.fail("put", err)
	}
	if p.InstalledAt.IsZero() {
		p.InstalledAt = time.Now().UTC()
	}
	prev, had := s.recs[p.WorkflowID]
	s.recs[p.WorkflowID] = p
	if err := s.persist(); err != nil {
		if had {
			s.recs[p.WorkflowID] = prev
		} else {
			delete(s.recs, p.WorkflowID)
		}
		return s.fail("put", err)
	}
	return nil
}

// List implements ProvenanceStore. An unreadable file is an error, never an
// empty list: a caller deciding what to remove must not read "unknown" as
// "nothing installed".
func (s *fileProvenanceStore) List() ([]InstallProvenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, s.fail("list", err)
	}
	out := make([]InstallProvenance, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	return out, nil
}

// Remove implements ProvenanceStore. Removing an absent record is a no-op.
func (s *fileProvenanceStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return s.fail("remove", err)
	}
	prev, had := s.recs[id]
	if !had {
		return nil
	}
	delete(s.recs, id)
	if err := s.persist(); err != nil {
		s.recs[id] = prev
		return s.fail("remove", err)
	}
	return nil
}

// BuiltinVersionTag is the provenance version string of a shipped template.
func BuiltinVersionTag(w Workflow) string { return fmt.Sprintf("v%d", w.Version) }
