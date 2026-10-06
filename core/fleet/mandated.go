// Package fleet — mandated.go
//
// MandatedApplier applies the bundle's mandated_items section (owner
// wire-contract ruling 2026-10-06, WP02): dispatch each envelope by kind,
// reconcile against the previously applied set, and report a per-item status
// for the ACK.
//
//	skill    → the slash store (persist + live-register, Source=mandated)
//	workflow → the workflows view's InstallDocument path, provenance mandated
//	pack     → refused: ErrMandatedKindUnsupported (no consumer in this build)
//	bundle   → refused: ErrMandatedKindUnsupported
//	other    → refused: ErrMandatedKindUnsupported
//
// Reconciliation: the set applied last time is persisted at
// <dataDir>/fleet/mandated_applied.json. An item in that set and absent from
// the new section is uninstalled (skill: unregistered from the slash store;
// workflow: deleted, only when its provenance is still this mandate) and
// reported with status "removed". An EMPTY section therefore means "remove
// everything previously mandated" — the applier must run on every bundle.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// Mandated item kinds (fleet catalog kinds, service/handlers_catalog.go:66-71).
const (
	MandatedKindSkill    = "skill"
	MandatedKindWorkflow = "workflow"
	MandatedKindPack     = "pack"
	MandatedKindBundle   = "bundle"
)

// Per-item ACK statuses (fleet-delivered contract, 2026-10-06).
const (
	MandatedStatusApplied = "applied" // installed / updated successfully
	MandatedStatusRefused = "refused" // a kind this build cannot install
	MandatedStatusFailed  = "failed"  // attempted and errored
	MandatedStatusRemoved = "removed" // reconcile-uninstalled: no longer mandated
)

// ErrMandatedKindUnsupported is the named refusal for a mandated item whose
// kind has no consumer in this build (pack and bundle today). It is surfaced
// in the ACK's errors[] and per-item status — never a silent drop.
var ErrMandatedKindUnsupported = errors.New("fleet: mandated item kind is not supported by this harness")

// ErrMandatedConsumerUnwired names a mandated item (or a removal) this device
// cannot act on because the consumer was never wired at boot.
var ErrMandatedConsumerUnwired = errors.New("fleet: mandated item consumer not wired")

// MandatedItemStatus is one per-item ACK entry.
type MandatedItemStatus struct {
	CatalogID string `json:"catalog_id"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// MandatedWorkflows is the workflow consumer the applier dispatches to. The
// rpc layer adapts the workflows view to it (core/fleet must not import it).
type MandatedWorkflows interface {
	// InstallMandatedWorkflow installs/updates the workflow document payload
	// as org-mandated provenance for catalogID@version, returning its id.
	InstallMandatedWorkflow(ctx context.Context, catalogID, version string, payload []byte) (workflowID string, err error)
	// RemoveMandatedWorkflow deletes workflowID only when its recorded
	// provenance is still the mandate of catalogID (a no-op nil otherwise —
	// the user or another install now owns it).
	RemoveMandatedWorkflow(ctx context.Context, workflowID, catalogID string) error
}

// MandatedApplier dispatches + reconciles mandated items. All fields are
// optional; a nil consumer turns the items that need it into "failed".
type MandatedApplier struct {
	Skills    *slashcmd.SkillStore
	Registry  *slashcmd.Registry
	Workflows MandatedWorkflows
	// DataDir holds the persisted applied set; empty = in-memory only (tests).
	DataDir string

	mu sync.Mutex
	// mem is the applied set when DataDir is empty.
	mem map[string]mandatedRecord
}

// mandatedRecord is one installed mandated item in the persisted set.
type mandatedRecord struct {
	CatalogID string `json:"catalog_id"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	// LocalID is the consumer-side id: the skill store id or the workflow id.
	LocalID string `json:"local_id"`
}

type mandatedState struct {
	Schema int                       `json:"schema"`
	Items  map[string]mandatedRecord `json:"items"`
}

// legacyCatalogPrefix marks a seed record for a skill mandated through the
// retired mandated_skills section, which never carried a catalog id.
const legacyCatalogPrefix = "legacy:"

func mandatedKey(kind, catalogID string) string { return kind + ":" + catalogID }

func mandatedStatePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "mandated_applied.json")
}

// Apply installs every item, removes items no longer mandated, persists the
// new applied set and returns per-item statuses plus every error (refusals
// included — a bundle with an item this device cannot honour must not ACK
// applied:true, the same posture as an unapplied cedar_delta).
func (m *MandatedApplier) Apply(ctx context.Context, items []BundleMandatedItem) ([]MandatedItemStatus, []error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev, loadErr := m.loadLocked()
	var errs []error
	if loadErr != nil {
		// Without the previous set removals cannot be computed; installs
		// still proceed and the error keeps the ACK honest.
		errs = append(errs, fmt.Errorf("fleet/mandated: read applied set: %w", loadErr))
	}
	next := make(map[string]mandatedRecord, len(items))
	statuses := make([]MandatedItemStatus, 0, len(items)+len(prev))
	seen := make(map[string]bool, len(items))

	for _, it := range items {
		key := mandatedKey(it.Kind, it.CatalogID)
		seen[key] = true
		st := MandatedItemStatus{CatalogID: it.CatalogID, Kind: it.Kind, Version: it.Version}
		localID, err := m.install(ctx, it, prev[key])
		switch {
		case err == nil:
			st.Status = MandatedStatusApplied
			next[key] = mandatedRecord{CatalogID: it.CatalogID, Kind: it.Kind, Version: it.Version, LocalID: localID}
		case errors.Is(err, ErrMandatedKindUnsupported):
			st.Status = MandatedStatusRefused
			st.Error = err.Error()
			errs = append(errs, err)
		default:
			st.Status = MandatedStatusFailed
			st.Error = err.Error()
			errs = append(errs, err)
			// A failed update leaves the earlier install in place: keep
			// tracking it so a later de-mandate still removes it.
			if old, ok := prev[key]; ok {
				next[key] = old
			}
		}
		statuses = append(statuses, st)
	}

	// Reconcile: everything previously applied and no longer mandated.
	removedKeys := make([]string, 0)
	for key := range prev {
		if !seen[key] {
			removedKeys = append(removedKeys, key)
		}
	}
	sort.Strings(removedKeys)
	for _, key := range removedKeys {
		old := prev[key]
		st := MandatedItemStatus{CatalogID: old.CatalogID, Kind: old.Kind, Version: old.Version}
		if err := m.remove(ctx, old); err != nil {
			st.Status = MandatedStatusFailed
			st.Error = err.Error()
			errs = append(errs, err)
			next[key] = old // still installed: retry the removal next bundle
		} else {
			st.Status = MandatedStatusRemoved
		}
		// A legacy seed (pre-envelope mandate) has no fleet catalog id to
		// report against; its removal is local bookkeeping only.
		if strings.HasPrefix(old.CatalogID, legacyCatalogPrefix) {
			continue
		}
		statuses = append(statuses, st)
	}

	if err := m.saveLocked(next); err != nil {
		errs = append(errs, fmt.Errorf("fleet/mandated: persist applied set: %w", err))
	}
	return statuses, errs
}

func (m *MandatedApplier) install(ctx context.Context, it BundleMandatedItem, prev mandatedRecord) (string, error) {
	if it.CatalogID == "" {
		return "", fmt.Errorf("fleet/mandated: %s item has no catalog_id", it.Kind)
	}
	switch it.Kind {
	case MandatedKindSkill:
		if m.Skills == nil || m.Registry == nil {
			return "", fmt.Errorf("%w: skill %s (slash store)", ErrMandatedConsumerUnwired, it.CatalogID)
		}
		var skill slashcmd.Skill
		if err := json.Unmarshal(it.Payload, &skill); err != nil {
			return "", fmt.Errorf("fleet/mandated: skill %s: %w: %v", it.CatalogID, ErrCatalogPayloadMalformed, err)
		}
		if skill.ID == "" {
			return "", fmt.Errorf("fleet/mandated: skill %s: %w: no id", it.CatalogID, ErrCatalogPayloadMalformed)
		}
		skill.Source = slashcmd.SkillSourceMandated
		skill.OrgManaged = true
		skill.CatalogID = it.CatalogID
		skill.Version = it.Version
		// The mandate moved to a new skill id: drop the one it replaces.
		if prev.LocalID != "" && prev.LocalID != skill.ID {
			if err := m.removeSkill(prev.LocalID, it.CatalogID); err != nil {
				return "", err
			}
		}
		if err := slashcmd.LiveRegister(m.Skills, m.Registry, skill); err != nil && !isSkillShadowedErr(err) {
			return "", fmt.Errorf("fleet/mandated: skill %s register %q: %w", it.CatalogID, skill.ID, err)
		}
		return skill.ID, nil
	case MandatedKindWorkflow:
		if m.Workflows == nil {
			return "", fmt.Errorf("%w: workflow %s (workflows view)", ErrMandatedConsumerUnwired, it.CatalogID)
		}
		id, err := m.Workflows.InstallMandatedWorkflow(ctx, it.CatalogID, it.Version, it.Payload)
		if err != nil {
			return "", fmt.Errorf("fleet/mandated: workflow %s: %w", it.CatalogID, err)
		}
		if prev.LocalID != "" && prev.LocalID != id {
			if err := m.Workflows.RemoveMandatedWorkflow(ctx, prev.LocalID, it.CatalogID); err != nil {
				return "", fmt.Errorf("fleet/mandated: workflow %s: remove superseded %q: %w", it.CatalogID, prev.LocalID, err)
			}
		}
		return id, nil
	default:
		// pack, bundle, and any kind fleet adds later.
		return "", fmt.Errorf("%w: %s %s", ErrMandatedKindUnsupported, it.Kind, it.CatalogID)
	}
}

func (m *MandatedApplier) remove(ctx context.Context, r mandatedRecord) error {
	switch r.Kind {
	case MandatedKindSkill:
		return m.removeSkill(r.LocalID, r.CatalogID)
	case MandatedKindWorkflow:
		if m.Workflows == nil {
			return fmt.Errorf("%w: cannot remove workflow %s", ErrMandatedConsumerUnwired, r.CatalogID)
		}
		if err := m.Workflows.RemoveMandatedWorkflow(ctx, r.LocalID, r.CatalogID); err != nil {
			return fmt.Errorf("fleet/mandated: remove workflow %s: %w", r.CatalogID, err)
		}
		return nil
	default:
		// Never installed (refused kinds are never recorded); nothing to do.
		return nil
	}
}

// removeSkill unregisters a mandated skill — only while it is still the
// mandated copy of catalogID (anything else now owns the id).
func (m *MandatedApplier) removeSkill(localID, catalogID string) error {
	if m.Skills == nil || m.Registry == nil {
		return fmt.Errorf("%w: cannot remove skill %s", ErrMandatedConsumerUnwired, catalogID)
	}
	sk, err := m.Skills.Get(localID)
	if err != nil {
		return nil // already gone
	}
	if sk.Source != slashcmd.SkillSourceMandated {
		return nil // no longer the org's copy
	}
	if sk.CatalogID != "" && sk.CatalogID != catalogID {
		return nil // a different mandate owns it now
	}
	if err := slashcmd.LiveUnregister(m.Skills, m.Registry, localID); err != nil {
		return fmt.Errorf("fleet/mandated: remove skill %s (%q): %w", catalogID, localID, err)
	}
	return nil
}

// loadLocked reads the applied set. With no state file yet, it seeds from
// any skills in the store already marked mandated (residue of the retired
// mandated_skills section), so the first bundle reconciles them too.
func (m *MandatedApplier) loadLocked() (map[string]mandatedRecord, error) {
	if m.DataDir == "" {
		out := make(map[string]mandatedRecord, len(m.mem))
		for k, v := range m.mem {
			out[k] = v
		}
		return out, nil
	}
	raw, err := os.ReadFile(mandatedStatePath(m.DataDir))
	if errors.Is(err, os.ErrNotExist) {
		return m.legacySkillSeed(), nil
	}
	if err != nil {
		return map[string]mandatedRecord{}, err
	}
	var st mandatedState
	if err := json.Unmarshal(raw, &st); err != nil {
		return map[string]mandatedRecord{}, err
	}
	if st.Items == nil {
		st.Items = map[string]mandatedRecord{}
	}
	return st.Items, nil
}

func (m *MandatedApplier) legacySkillSeed() map[string]mandatedRecord {
	out := map[string]mandatedRecord{}
	if m.Skills == nil {
		return out
	}
	skills, err := m.Skills.List()
	if err != nil {
		return out
	}
	for _, sk := range skills {
		if sk.Source != slashcmd.SkillSourceMandated {
			continue
		}
		cid := sk.CatalogID
		if cid == "" {
			cid = legacyCatalogPrefix + sk.ID // pre-envelope mandate: no catalog id was ever sent
		}
		out[mandatedKey(MandatedKindSkill, cid)] = mandatedRecord{
			CatalogID: cid, Kind: MandatedKindSkill, Version: sk.Version, LocalID: sk.ID,
		}
	}
	return out
}

func (m *MandatedApplier) saveLocked(set map[string]mandatedRecord) error {
	if m.DataDir == "" {
		m.mem = set
		return nil
	}
	if err := os.MkdirAll(filepath.Join(m.DataDir, "fleet"), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(mandatedState{Schema: 1, Items: set})
	if err != nil {
		return err
	}
	return atomicWriteFile(mandatedStatePath(m.DataDir), string(raw)+"\n")
}
