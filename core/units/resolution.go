package units

// resolution.go — same-unit conflict resolution (WP17) and resolution-time
// surfacing of enshrined conflicts (WP18) for the unified Unit model
// (unified-context-artifacts-01NCTXU01 / Phase 3).
//
// Two user-chosen outcomes for a same-unit divergence (DESIGN §4):
//
//   - MERGE   — whole-body resolve: the user supplies a resolved body, the
//               local unit's version is bumped, the conflict is cleared, and a
//               re-sync naturally follows (the bumped version is dirty again).
//
//   - ENSHRINE — keep BOTH: a new coexisting Unit is created carrying the other
//               side's body, linked to the original by a 'conflicts_with' marker
//               Edge. Both units load; precedence orders them; the conflict is
//               surfaced FLAGGED at resolution time (WP18), never dropped
//               (FR-041 / FR-042).
//
// This file is fleet-free (DIRECTIVE_001): it operates purely over the Store
// contract. The fleet UnitSyncer surfaces the UnitConflict; the RPC layer (or a
// fleet-side resolver) calls these Manager methods to act on the user's choice.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// enshrineMetaKey is the reserved metadata key that flags an enshrined-conflict
// unit. Its presence (truthy) marks the unit as one side of a legitimately
// diverged pair so resolution-time surfacing can flag it (WP18). The value is
// the conflict's peer/group context.
const enshrineMetaKey = "_enshrined_conflict"

// EnshrineMarker is the JSON value stored under metadata["_enshrined_conflict"].
// It records which unit this one diverges from and a stable group id so the
// surfacing layer can cluster the N coexisting sides of one conflict.
type EnshrineMarker struct {
	// PeerUnitID is the original unit this enshrined unit coexists with.
	PeerUnitID string `json:"peer_unit_id"`
	// GroupID clusters all sides of one enshrined conflict (defaults to the
	// original unit id when unset).
	GroupID string `json:"group_id,omitempty"`
	// Reason is an optional human note ("two teams diverge here").
	Reason string `json:"reason,omitempty"`
}

// ErrNotEnshrineable is returned when ResolveEnshrine is asked to enshrine
// against a missing or invalid source unit.
var ErrNotEnshrineable = errors.New("units: cannot enshrine against source")

// ResolveMerge applies a whole-body MERGE resolution to a conflicted unit: it
// writes resolvedBody (and optional metadata) as a new version via the normal
// Update path, bumping the version and appending history. The bumped version
// makes the unit dirty again, so the next sync cycle re-offers it — clearing
// the conflict by converging on the resolved body (WP17a).
//
// metadata may be nil; when nil the unit's existing metadata is preserved so
// a whole-body merge does not destroy artifact provenance fields such as
// content_hash and byte_size that were not part of the conflict.
func (m *Manager) ResolveMerge(ctx context.Context, unitID, resolvedBody string, metadata json.RawMessage) (Unit, error) {
	if unitID == "" {
		return Unit{}, errors.New("units: ResolveMerge: empty unitID")
	}
	// Fetch the current unit: confirms existence (clean ErrUnitNotFound) and
	// gives us the existing metadata to carry forward when the caller passes nil.
	current, err := m.store.Get(ctx, unitID)
	if err != nil {
		return Unit{}, fmt.Errorf("units: ResolveMerge: %w", err)
	}
	// Preserve existing metadata when the caller passes nil so provenance
	// fields (content_hash, byte_size, etc.) survive a whole-body resolve.
	if metadata == nil {
		metadata = current.Metadata
	}
	updated, err := m.store.Update(ctx, unitID, resolvedBody, []byte(metadata))
	if err != nil {
		return Unit{}, fmt.Errorf("units: ResolveMerge: %w", err)
	}
	return updated, nil
}

// ResolveEnshrine applies an ENSHRINE resolution: it creates a NEW coexisting
// Unit carrying enshrinedBody (the other diverged side) in the same scope /
// classification as the source, then links the new unit to the source with a
// 'conflicts_with' marker Edge so BOTH coexist and load (WP17b). The new unit's
// metadata carries an EnshrineMarker so resolution-time surfacing can flag it
// (WP18). The source unit is left untouched.
//
// enshrinedTitle defaults to the source title when empty. reason is an optional
// human note recorded in the marker.
//
// Returns (newUnit, markerEdge, nil) on success.
func (m *Manager) ResolveEnshrine(ctx context.Context, srcID, enshrinedTitle, enshrinedBody, reason string) (Unit, Edge, error) {
	if srcID == "" {
		return Unit{}, Edge{}, errors.New("units: ResolveEnshrine: empty srcID")
	}
	src, err := m.store.Get(ctx, srcID)
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("%w: %v", ErrNotEnshrineable, err)
	}

	title := enshrinedTitle
	if title == "" {
		title = src.Title
	}
	marker := EnshrineMarker{
		PeerUnitID: src.ID,
		GroupID:    src.ID,
		Reason:     reason,
	}
	meta, err := foldEnshrineMarker(src.Metadata, marker)
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: ResolveEnshrine: marker: %w", err)
	}

	coexisting := Unit{
		Kind:           src.Kind,
		Scope:          src.Scope,
		ScopeID:        src.ScopeID,
		Classification: src.Classification,
		LoadPolicy:     src.LoadPolicy,
		Title:          title,
		Body:           enshrinedBody,
		Metadata:       meta,
	}
	// Atomic: the coexisting unit and its conflicts_with marker edge are written
	// together so a failure never leaves an enshrined unit without its marker.
	newUnit, edge, err := m.store.CreateWithEdge(ctx, coexisting, Edge{
		ToID: src.ID,
		Kind: EdgeConflictsWith,
	})
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: ResolveEnshrine: %w", err)
	}
	return newUnit, edge, nil
}

// IsEnshrined reports whether a unit carries the enshrined-conflict marker, and
// returns the parsed marker when present.
func IsEnshrined(u Unit) (EnshrineMarker, bool) {
	if len(u.Metadata) == 0 {
		return EnshrineMarker{}, false
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(u.Metadata, &obj); err != nil {
		return EnshrineMarker{}, false
	}
	raw, ok := obj[enshrineMetaKey]
	if !ok {
		return EnshrineMarker{}, false
	}
	var mk EnshrineMarker
	if err := json.Unmarshal(raw, &mk); err != nil {
		return EnshrineMarker{}, false
	}
	return mk, true
}

// foldEnshrineMarker merges caller metadata with the reserved enshrine marker.
func foldEnshrineMarker(base json.RawMessage, marker EnshrineMarker) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &obj); err != nil {
			// Non-object base metadata is replaced with a fresh object holding
			// only the marker rather than failing the enshrine.
			obj = map[string]json.RawMessage{}
		}
	}
	mb, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	obj[enshrineMetaKey] = mb
	return json.Marshal(obj)
}

// ── Resolution-time surfacing (WP18) ────────────────────────────────────────

// ResolvedUnit is one entry in the resolved, precedence-ordered loadable set
// returned at conversation-start resolution. Enshrined-conflict units are
// surfaced FLAGGED (Flagged=true) and ordered by precedence alongside their
// peers — never silently dropped (FR-042).
type ResolvedUnit struct {
	// Unit is the underlying unit.
	Unit Unit
	// Flagged is true when this unit is one side of an enshrined conflict and
	// the agent must be told "N sides diverge here".
	Flagged bool
	// Marker carries the enshrine context when Flagged.
	Marker EnshrineMarker
	// Precedence is the computed sort rank (lower loads first). Surfaced for
	// callers that want to render the ordering.
	Precedence int
}

// ResolveLoadable returns the units that should be injected at conversation
// start for the given filter, precedence-ordered, with enshrined conflicts
// flagged (WP18 wires this into the unit/context resolution path).
//
// Selection: only LoadAlways units (eager-load set). On-demand units are
// reachable via read_context_file and are not returned here.
//
// Precedence (DESIGN §5, default): classification org→team→personal
// (most-shared first as the base layer) and scope global→project→session, with
// the most-specific / most-personal layered LAST so it wins at read time. The
// resulting Precedence rank is ascending (0 loads first). Enshrined units sort
// immediately after their non-enshrined peer so the agent sees the canonical
// side first, then the flagged divergence — never dropped.
func (m *Manager) ResolveLoadable(ctx context.Context, filter UnitFilter) ([]ResolvedUnit, error) {
	// Force the eager-load policy regardless of caller filter — resolution only
	// injects the always-set.
	filter.LoadPolicy = LoadAlways
	all, err := m.store.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("units: ResolveLoadable: %w", err)
	}

	out := make([]ResolvedUnit, 0, len(all))
	for _, u := range all {
		marker, flagged := IsEnshrined(u)
		out = append(out, ResolvedUnit{
			Unit:    u,
			Flagged: flagged,
			Marker:  marker,
		})
	}

	// Stable precedence sort. Primary: classification rank (org=0, team=1,
	// personal=2) so shared layers load first as the base and personal wins
	// last. Secondary: scope rank (global=0, project=1, session=2). Tertiary:
	// cluster all sides of one enshrined conflict together by PeerUnitID so
	// every flagged side sorts right after its canonical peer regardless of
	// insertion order. Quaternary: unflagged before flagged within a cluster.
	// Quinary: created_at for determinism.
	sort.SliceStable(out, func(i, j int) bool {
		ci, cj := classRank(out[i].Unit.Classification), classRank(out[j].Unit.Classification)
		if ci != cj {
			return ci < cj
		}
		si, sj := scopeRank(out[i].Unit.Scope), scopeRank(out[j].Unit.Scope)
		if si != sj {
			return si < sj
		}
		// Cluster all sides of one enshrined conflict by PeerUnitID. The
		// non-flagged canonical peer has an empty PeerUnitID; the flagged
		// sides carry the peer's id. Using the unit's own id for the
		// non-flagged side keeps it at the head of the cluster.
		pi := out[i].Marker.PeerUnitID
		if !out[i].Flagged {
			pi = out[i].Unit.ID
		}
		pj := out[j].Marker.PeerUnitID
		if !out[j].Flagged {
			pj = out[j].Unit.ID
		}
		if pi != pj {
			return pi < pj
		}
		// Within a conflict cluster: non-flagged (canonical) before flagged.
		if out[i].Flagged != out[j].Flagged {
			return !out[i].Flagged
		}
		return out[i].Unit.CreatedAt.Before(out[j].Unit.CreatedAt)
	})

	for i := range out {
		out[i].Precedence = i
	}
	return out, nil
}

// classRank orders classifications for precedence (org loads first as the base
// layer, personal last so it wins at read time).
func classRank(c Classification) int {
	switch c {
	case ClassOrg:
		return 0
	case ClassTeam:
		return 1
	case ClassPersonal:
		return 2
	default:
		return 3
	}
}

// scopeRank orders scopes for precedence (global base → session most-specific).
func scopeRank(s Scope) int {
	switch s {
	case ScopeGlobal:
		return 0
	case ScopeProject:
		return 1
	case ScopeSession:
		return 2
	default:
		return 3
	}
}
