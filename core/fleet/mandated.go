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

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
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
	// as org-mandated provenance for catalogID@version, returning its id
	// and, when the mandate TOOK OVER the user's own catalog install of
	// that id, an opaque snapshot of the user's copy (review F6; ledger
	// 2026-10-06 R2). prior is nil otherwise.
	InstallMandatedWorkflow(ctx context.Context, catalogID, version string, payload []byte) (workflowID string, prior json.RawMessage, err error)
	// RemoveMandatedWorkflow ends the mandate on workflowID — only while
	// its recorded provenance is still the mandate of catalogID (a no-op
	// otherwise: the user or another install now owns it). A non-empty
	// prior restores the user's own copy; legacyRestore (a v0.91 record:
	// takeover recorded, no snapshot) hands the current content back as a
	// catalog install; otherwise it is deleted. The result says whether
	// the user got a copy back, or why policy refused to restore it.
	RemoveMandatedWorkflow(ctx context.Context, workflowID, catalogID string, prior json.RawMessage, legacyRestore bool) (MandatedWorkflowRemoval, error)
}

// MandatedWorkflowRemoval is the outcome of ending a workflow mandate.
type MandatedWorkflowRemoval struct {
	// Restored: the user's own earlier copy was handed back.
	Restored bool
	// RestoreRefused is set when a snapshot existed but the policy save
	// gate refused to write it back (e.g. a shell step under strict mode):
	// the mandated copy was deleted instead, and the reason is audited —
	// never dropped silently.
	RestoreRefused string
}

// MandatedApplier dispatches + reconciles mandated items. Keep ONE instance
// per process (it remembers whether a state-file error was already
// reported). A nil consumer turns the items that need it into "failed".
type MandatedApplier struct {
	Skills    *slashcmd.SkillStore
	Registry  *slashcmd.Registry
	Workflows MandatedWorkflows
	// DataDir holds the persisted applied set; empty = in-memory only (tests).
	DataDir string
	// Emitter records the LOCAL audit of reconcile removals: "removed" for
	// a real uninstall / hand-back, "upgraded" when a newer version took
	// over the same local item in the same bundle (skill-library-01SKLIB01
	// WP04, fleet H3). nil = no local audit (tests).
	Emitter AuditEmitter

	mu sync.Mutex
	// mem is the applied set when DataDir is empty.
	mem map[string]mandatedRecord
	// loadErrReported: a corrupt/unreadable state file is surfaced in the
	// ACK errors ONCE per process (review F5), then only logged.
	loadErrReported bool
}

// SetConsumers updates the consumers (wired at boot, possibly after the
// applier was created). Safe for concurrent use with Apply.
func (m *MandatedApplier) SetConsumers(skills *slashcmd.SkillStore, registry *slashcmd.Registry, wf MandatedWorkflows) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Skills, m.Registry, m.Workflows = skills, registry, wf
}

// SetEmitter wires the local audit emitter. Safe for concurrent use with
// Apply.
func (m *MandatedApplier) SetEmitter(em AuditEmitter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Emitter = em
}

// consumersReadyLocked: every consumer is wired. Until then the applier
// never seeds from or writes the persisted applied set (review F4): a
// state file written by an unwired applier would record "nothing applied"
// and permanently burn the legacy-residue seed.
func (m *MandatedApplier) consumersReadyLocked() bool {
	return m.Skills != nil && m.Registry != nil && m.Workflows != nil
}

// mandatedRecord is one installed mandated item in the persisted set.
type mandatedRecord struct {
	CatalogID string `json:"catalog_id"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	// LocalID is the consumer-side id: the skill store id or the workflow id.
	LocalID string `json:"local_id"`
	// PriorSkill is the user's own skill the mandate replaced under the same
	// id (a catalog install or a local skill). Withdrawal RESTORES it
	// instead of deleting (review F6). Empty for a fresh mandate install.
	PriorSkill json.RawMessage `json:"prior_skill,omitempty"`
	// PriorCatalogWorkflow: the mandate took over the user's own catalog
	// install of this workflow; withdrawal hands it back (review F6). A
	// v0.91 record carries only this flag; since skill-library-01SKLIB01
	// the snapshot below is recorded too, and the flag alone means "legacy
	// record: relabel on withdrawal".
	PriorCatalogWorkflow bool `json:"prior_catalog_workflow,omitempty"`
	// PriorWorkflow is the workflows consumer's opaque snapshot of the
	// user's own copy the mandate took over (ledger 2026-10-06 R2):
	// withdrawal restores the user's actual version, not the mandated
	// content relabelled. Additive; schema stays 1.
	PriorWorkflow json.RawMessage `json:"prior_workflow,omitempty"`
}

func (r mandatedRecord) hasPrior() bool {
	return len(r.PriorSkill) > 0 || len(r.PriorWorkflow) > 0 || r.PriorCatalogWorkflow
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

// mandatedPendingPath is the side file that carries what was applied while
// mandated_applied.json was UNREADABLE (review F5 never overwrites it).
// Without it, a takeover during that window recorded nothing, so the
// user's prior copy was lost and a later withdrawal deleted instead of
// restoring (ledger 2026-10-06 R3). It is merged into the applied set on
// the next readable run and removed once that set is saved.
func mandatedPendingPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "mandated_applied.pending.json")
}

// Apply installs every item, removes items no longer mandated, persists the
// new applied set and returns per-item statuses plus the errors that should
// fail the bundle.
//
// ACK contract (review F1): a REFUSED item (a kind this build cannot
// install — pack, bundle) is reported only as items[].status="refused"; it
// is NOT a bundle error, so the ACK is applied:true and the bundle
// advances. A FAILED item (attempted and errored, e.g. a consumer not yet
// wired during boot) IS a bundle error, so the bundle is retried.
//
// ACK version fidelity (fleet H6): every status's Version is the ENVELOPE
// version (or, for a removal, the version recorded when it was applied) —
// never a version resolved from the consumer. Fleet's "on older version"
// adoption counts depend on it.
//
// Quiet upgrades (fleet H3, skill-library-01SKLIB01 WP04): when a removed
// item's local skill/workflow was taken over in THIS run by a different
// catalog id (the v1→v2 promote), the ACK still says "removed" — fleet
// groups it by entry and labels it "superseded" — but nothing is
// uninstalled, the user's prior copy (if the mandate had taken one over)
// carries forward to the new version's record, and the LOCAL audit records
// an upgrade, not an uninstall.
func (m *MandatedApplier) Apply(ctx context.Context, items []BundleMandatedItem) ([]MandatedItemStatus, []error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ready := m.consumersReadyLocked()
	prev, loadErr := m.loadLocked(ready)
	var errs []error
	persist := ready
	// carry is where each item's earlier record comes from for INSTALL
	// purposes (its takeover prior). It is prev — except while the state
	// file is unreadable, when it is what this applier recorded during that
	// window (review R3); removals still use prev only (F5).
	if loadErr == nil && ready {
		mergePending(prev, m.loadPendingLocked()) // R3
	}
	carry := prev
	if loadErr != nil {
		// Review F5: never persist over a state file we could not read —
		// that would erase the record of what is installed. Treat the
		// previous set as empty for this run (no removals), log every time,
		// surface in the ACK once per process.
		persist = false
		logging.L().Warn("fleet.mandated.state_unreadable", "err", loadErr.Error())
		if !m.loadErrReported {
			m.loadErrReported = true
			errs = append(errs, fmt.Errorf("fleet/mandated: read applied set (left untouched; removals skipped): %w", loadErr))
		}
		carry = m.loadPendingLocked()
	}
	next := make(map[string]mandatedRecord, len(items))
	statuses := make([]MandatedItemStatus, 0, len(items)+len(prev))
	seen := make(map[string]bool, len(items))
	// owner maps kind:localID to the key that holds it after this run.
	owner := make(map[string]string, len(items))

	for _, it := range items {
		key := mandatedKey(it.Kind, it.CatalogID)
		seen[key] = true
		st := MandatedItemStatus{CatalogID: it.CatalogID, Kind: it.Kind, Version: it.Version}
		rec, err := m.install(ctx, it, carry[key])
		switch {
		case err == nil:
			st.Status = MandatedStatusApplied
			next[key] = rec
			if rec.LocalID != "" {
				owner[it.Kind+":"+rec.LocalID] = key
			}
		case errors.Is(err, ErrMandatedKindUnsupported):
			// Refused: per-item status only, never a bundle error (F1).
			st.Status = MandatedStatusRefused
			st.Error = err.Error()
			logging.L().Info("fleet.mandated.refused", "kind", it.Kind, "catalog_id", it.CatalogID)
		default:
			st.Status = MandatedStatusFailed
			st.Error = err.Error()
			errs = append(errs, err)
			// A failed update leaves the earlier install in place: keep
			// tracking it so a later de-mandate still removes it.
			if old, ok := carry[key]; ok {
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
	var audits []mandatedAudit
	for _, key := range removedKeys {
		old := prev[key]
		st := MandatedItemStatus{CatalogID: old.CatalogID, Kind: old.Kind, Version: old.Version}
		if newKey, ok := owner[old.Kind+":"+old.LocalID]; ok && old.LocalID != "" {
			// Superseded in this run: the new version already holds the
			// local item. Nothing to uninstall; hand the user's prior copy
			// on to the new version's record.
			nr := next[newKey]
			if !nr.hasPrior() && old.hasPrior() {
				nr.PriorSkill, nr.PriorWorkflow, nr.PriorCatalogWorkflow = old.PriorSkill, old.PriorWorkflow, old.PriorCatalogWorkflow
				next[newKey] = nr
			}
			st.Status = MandatedStatusRemoved
			audits = append(audits, mandatedAudit{upgraded: true, from: old, to: nr})
		} else if out, err := m.remove(ctx, old); err != nil {
			st.Status = MandatedStatusFailed
			st.Error = err.Error()
			errs = append(errs, err)
			next[key] = old // still installed: retry the removal next bundle
		} else {
			st.Status = MandatedStatusRemoved
			audits = append(audits, mandatedAudit{from: old, restored: out.Restored, restoreRefused: out.RestoreRefused})
		}
		// A legacy seed (pre-envelope mandate) has no fleet catalog id to
		// report against; its removal is local bookkeeping only.
		if strings.HasPrefix(old.CatalogID, legacyCatalogPrefix) {
			continue
		}
		statuses = append(statuses, st)
	}

	switch {
	case persist:
		if err := m.saveLocked(next); err != nil {
			errs = append(errs, fmt.Errorf("fleet/mandated: persist applied set: %w", err))
		} else {
			m.clearPendingLocked()
		}
	case loadErr != nil && ready:
		// R3: record what this run applied (and its takeover priors) beside
		// the unreadable file, never over it.
		merged := make(map[string]mandatedRecord, len(carry)+len(next))
		for k, v := range carry {
			merged[k] = v
		}
		for k, v := range next {
			merged[k] = v
		}
		if err := m.savePendingLocked(merged); err != nil {
			logging.L().Warn("fleet.mandated.pending_write_failed", "err", err.Error())
		}
	}
	m.emitAuditsLocked(ctx, audits)
	return statuses, errs
}

// mandatedAudit is one local reconcile event (WP04).
type mandatedAudit struct {
	upgraded       bool
	from           mandatedRecord
	to             mandatedRecord
	restored       bool
	restoreRefused string
}

func (m *MandatedApplier) emitAuditsLocked(ctx context.Context, audits []mandatedAudit) {
	for _, a := range audits {
		p := contextaudit.FleetMandatedItemPayload{
			CatalogID: a.from.CatalogID, Kind: a.from.Kind, Version: a.from.Version, LocalID: a.from.LocalID,
		}
		kind := contextaudit.KindFleetMandatedItemRemoved
		if a.upgraded {
			kind = contextaudit.KindFleetMandatedItemUpgraded
			p.ToCatalogID, p.ToVersion = a.to.CatalogID, a.to.Version
			logging.L().Info("fleet.mandated.upgraded", "kind", a.from.Kind, "local_id", a.from.LocalID,
				"from_catalog_id", a.from.CatalogID, "to_catalog_id", a.to.CatalogID)
		} else {
			p.Restored = a.restored
			p.RestoreRefused = a.restoreRefused
			logging.L().Info("fleet.mandated.removed", "kind", a.from.Kind, "local_id", a.from.LocalID,
				"catalog_id", a.from.CatalogID, "restored", a.restored, "restore_refused", a.restoreRefused != "")
		}
		if m.Emitter != nil {
			_ = m.Emitter.EmitFleetEvent(ctx, kind, p)
		}
	}
}

func (m *MandatedApplier) install(ctx context.Context, it BundleMandatedItem, prev mandatedRecord) (mandatedRecord, error) {
	rec := mandatedRecord{CatalogID: it.CatalogID, Kind: it.Kind, Version: it.Version}
	if it.CatalogID == "" {
		return rec, fmt.Errorf("fleet/mandated: %s item has no catalog_id", it.Kind)
	}
	switch it.Kind {
	case MandatedKindSkill:
		if m.Skills == nil || m.Registry == nil {
			return rec, fmt.Errorf("%w: skill %s (slash store)", ErrMandatedConsumerUnwired, it.CatalogID)
		}
		var skill slashcmd.Skill
		if err := json.Unmarshal(it.Payload, &skill); err != nil {
			return rec, fmt.Errorf("fleet/mandated: skill %s: %w: %v", it.CatalogID, ErrCatalogPayloadMalformed, err)
		}
		if skill.ID == "" {
			return rec, fmt.Errorf("fleet/mandated: skill %s: %w: no id", it.CatalogID, ErrCatalogPayloadMalformed)
		}
		skill.Source = slashcmd.SkillSourceMandated
		skill.OrgManaged = true
		skill.CatalogID = it.CatalogID
		skill.Version = it.Version
		// The mandate moved to a new skill id: end it on the old one.
		if prev.LocalID != "" && prev.LocalID != skill.ID {
			if _, err := m.endSkillMandate(prev.LocalID, it.CatalogID, prev.PriorSkill); err != nil {
				return rec, err
			}
			prev.PriorSkill = nil
		}
		// Taking over the user's own skill under this id (a catalog
		// install or a local skill): remember it so withdrawal restores
		// it instead of deleting (F6). An earlier mandate's record wins.
		rec.PriorSkill = prev.PriorSkill
		if prev.LocalID != skill.ID || rec.PriorSkill == nil {
			if cur, err := m.Skills.Get(skill.ID); err == nil && cur.Source != slashcmd.SkillSourceMandated && !cur.OrgManaged {
				if raw, merr := json.Marshal(cur); merr == nil {
					rec.PriorSkill = raw
				}
			}
		}
		if err := slashcmd.LiveRegister(m.Skills, m.Registry, skill); err != nil && !isSkillShadowedErr(err) {
			return rec, fmt.Errorf("fleet/mandated: skill %s register %q: %w", it.CatalogID, skill.ID, err)
		}
		rec.LocalID = skill.ID
		return rec, nil
	case MandatedKindWorkflow:
		if m.Workflows == nil {
			return rec, fmt.Errorf("%w: workflow %s (workflows view)", ErrMandatedConsumerUnwired, it.CatalogID)
		}
		id, prior, err := m.Workflows.InstallMandatedWorkflow(ctx, it.CatalogID, it.Version, it.Payload)
		if err != nil {
			return rec, fmt.Errorf("fleet/mandated: workflow %s: %w", it.CatalogID, err)
		}
		// R2: keep the user's actual copy (a snapshot), not just a flag.
		rec.PriorWorkflow = prior
		rec.PriorCatalogWorkflow = len(prior) > 0
		if prev.LocalID == id {
			if len(rec.PriorWorkflow) == 0 {
				// An update of this mandate: the earlier record's prior wins.
				rec.PriorWorkflow, rec.PriorCatalogWorkflow = prev.PriorWorkflow, prev.PriorCatalogWorkflow
			}
		} else if prev.LocalID != "" {
			if _, err := m.Workflows.RemoveMandatedWorkflow(ctx, prev.LocalID, it.CatalogID, prev.PriorWorkflow, prev.legacyWorkflowRestore()); err != nil {
				return rec, fmt.Errorf("fleet/mandated: workflow %s: remove superseded %q: %w", it.CatalogID, prev.LocalID, err)
			}
		}
		rec.LocalID = id
		return rec, nil
	default:
		// pack, bundle, and any kind fleet adds later.
		return rec, fmt.Errorf("%w: %s %s", ErrMandatedKindUnsupported, it.Kind, it.CatalogID)
	}
}

// remove ends the mandate r recorded: whether the user's own earlier copy
// was handed back instead of deleted, or why restoring it was refused.
func (m *MandatedApplier) remove(ctx context.Context, r mandatedRecord) (MandatedWorkflowRemoval, error) {
	switch r.Kind {
	case MandatedKindSkill:
		restored, err := m.endSkillMandate(r.LocalID, r.CatalogID, r.PriorSkill)
		return MandatedWorkflowRemoval{Restored: restored}, err
	case MandatedKindWorkflow:
		if m.Workflows == nil {
			return MandatedWorkflowRemoval{}, fmt.Errorf("%w: cannot remove workflow %s", ErrMandatedConsumerUnwired, r.CatalogID)
		}
		out, err := m.Workflows.RemoveMandatedWorkflow(ctx, r.LocalID, r.CatalogID, r.PriorWorkflow, r.legacyWorkflowRestore())
		if err != nil {
			return MandatedWorkflowRemoval{}, fmt.Errorf("fleet/mandated: remove workflow %s: %w", r.CatalogID, err)
		}
		return out, nil
	default:
		// Never installed (refused kinds are never recorded); nothing to do.
		return MandatedWorkflowRemoval{}, nil
	}
}

// legacyWorkflowRestore: a v0.91 record that took over the user's catalog
// install recorded only the flag — no snapshot to restore — so withdrawal
// can only relabel (the pre-R2 behaviour, kept for those records).
func (r mandatedRecord) legacyWorkflowRestore() bool {
	return r.PriorCatalogWorkflow && len(r.PriorWorkflow) == 0
}

// endSkillMandate ends the mandate on localID: the mandated copy is
// unregistered and, when the mandate had taken over the user's own skill
// (prior), that skill is restored in its place (review F6). Acts only while
// the stored skill is still the mandated copy of catalogID. restored reports
// whether the prior skill was put back.
func (m *MandatedApplier) endSkillMandate(localID, catalogID string, prior json.RawMessage) (bool, error) {
	if err := m.removeSkill(localID, catalogID); err != nil {
		return false, err
	}
	if len(prior) == 0 {
		return false, nil
	}
	if _, err := m.Skills.Get(localID); err == nil {
		return false, nil // something else holds the id now — leave it
	}
	var sk slashcmd.Skill
	if err := json.Unmarshal(prior, &sk); err != nil {
		return false, fmt.Errorf("fleet/mandated: restore skill %q: %w", localID, err)
	}
	sk.OrgManaged = false
	if err := slashcmd.LiveRegister(m.Skills, m.Registry, sk); err != nil && !isSkillShadowedErr(err) {
		return false, fmt.Errorf("fleet/mandated: restore skill %q: %w", localID, err)
	}
	return true, nil
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
func (m *MandatedApplier) loadLocked(ready bool) (map[string]mandatedRecord, error) {
	if m.DataDir == "" {
		out := make(map[string]mandatedRecord, len(m.mem))
		for k, v := range m.mem {
			out[k] = v
		}
		return out, nil
	}
	raw, err := os.ReadFile(mandatedStatePath(m.DataDir))
	if errors.Is(err, os.ErrNotExist) {
		if !ready {
			return map[string]mandatedRecord{}, nil // F4: no seed until wired
		}
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

// loadPendingLocked reads the R3 side file; missing or unreadable is empty.
func (m *MandatedApplier) loadPendingLocked() map[string]mandatedRecord {
	out := map[string]mandatedRecord{}
	if m.DataDir == "" {
		return out
	}
	raw, err := os.ReadFile(mandatedPendingPath(m.DataDir))
	if err != nil {
		return out
	}
	var st mandatedState
	if err := json.Unmarshal(raw, &st); err != nil || st.Items == nil {
		return out
	}
	return st.Items
}

func (m *MandatedApplier) savePendingLocked(set map[string]mandatedRecord) error {
	if m.DataDir == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(m.DataDir, "fleet"), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(mandatedState{Schema: 1, Items: set})
	if err != nil {
		return err
	}
	return atomicWriteFile(mandatedPendingPath(m.DataDir), string(raw)+"\n")
}

func (m *MandatedApplier) clearPendingLocked() {
	if m.DataDir == "" {
		return
	}
	if err := os.Remove(mandatedPendingPath(m.DataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logging.L().Warn("fleet.mandated.pending_clear_failed", "err", err.Error())
	}
}

// mergePending folds records applied while the state file was unreadable
// into the readable applied set: a key the set lacks is added (it IS
// installed, so a later de-mandate must remove it), and a matching record
// missing its takeover prior gains it (R3).
func mergePending(prev, pending map[string]mandatedRecord) {
	for k, p := range pending {
		cur, ok := prev[k]
		if !ok {
			prev[k] = p
			continue
		}
		if !cur.hasPrior() && p.hasPrior() && cur.LocalID == p.LocalID {
			cur.PriorSkill, cur.PriorWorkflow, cur.PriorCatalogWorkflow = p.PriorSkill, p.PriorWorkflow, p.PriorCatalogWorkflow
			prev[k] = cur
		}
	}
}
