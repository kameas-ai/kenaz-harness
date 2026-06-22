package fleet

import (
	"context"
	"errors"
	"fmt"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// Impl implements FleetAPI backed by core/fleet.TelemetryConsent and the
// Phase-3 unit-collaboration plumbing (units.Manager + corefleet.UnitSyncer).
//
// Units and Syncer may be nil on the test chassis / offline path; every unit
// method nil-guards and returns ErrUnitsUnavailable so the surface degrades
// gracefully rather than panicking.
type Impl struct {
	Consent *corefleet.TelemetryConsent

	// Units is the fleet-free unified Unit store (resolution + enshrine live
	// here). Nil on the test chassis.
	Units *units.Manager
	// Syncer is the fleet sync engine; it owns the promote-as-MR client and the
	// surfaced pull-conflict list. Nil when fleet is disabled.
	Syncer *corefleet.UnitSyncer
}

var _ FleetAPI = (*Impl)(nil)

// ErrUnitsUnavailable is returned by the unit methods when the units store is
// not wired (test chassis / no DataDir).
var ErrUnitsUnavailable = errors.New("fleet: unit store unavailable")

// ErrSyncerUnavailable is returned when a method needs the fleet syncer (e.g.
// promote-as-MR) but it is not wired (fleet disabled).
var ErrSyncerUnavailable = errors.New("fleet: sync engine unavailable")

// GetTelemetryConsent returns the effective consent level.
func (f *Impl) GetTelemetryConsent(_ context.Context) (string, error) {
	return string(f.Consent.EffectiveLevel()), nil
}

// SetTelemetryConsent validates the level string and delegates to
// TelemetryConsent.SetLevel, which enforces tier gating.
func (f *Impl) SetTelemetryConsent(_ context.Context, level string) error {
	cl := corefleet.ConsentLevel(level)
	switch cl {
	case corefleet.ConsentNone, corefleet.ConsentAggregate, corefleet.ConsentFull:
		// valid
	default:
		return fmt.Errorf("unknown consent level %q; must be one of none, aggregate, full", level)
	}
	return f.Consent.SetLevel(cl)
}

// ── Phase-3 unit collaboration ──────────────────────────────────────────────

// Unit_PromoteAsMergeRequest opens a merge request to promote unitID UP to
// toClassification (WP16). The source unit is left untouched; only the proposal
// travels (write-up-reviewed).
func (f *Impl) Unit_PromoteAsMergeRequest(ctx context.Context, unitID, toClassification, title, body string) (MergeRequestResult, error) {
	if f.Units == nil {
		return MergeRequestResult{}, ErrUnitsUnavailable
	}
	if f.Syncer == nil {
		return MergeRequestResult{}, ErrSyncerUnavailable
	}
	toClass := units.Classification(toClassification)
	switch toClass {
	case units.ClassTeam, units.ClassOrg:
		// valid promotion targets
	default:
		return MergeRequestResult{}, fmt.Errorf("invalid promote target %q; must be team or org", toClassification)
	}
	src, err := f.Units.Get(ctx, unitID)
	if err != nil {
		return MergeRequestResult{}, fmt.Errorf("fleet: promote-as-MR: %w", err)
	}
	mr, err := f.Syncer.CreateMergeRequestForPromote(ctx, src, toClass, title, body)
	if err != nil {
		return MergeRequestResult{}, err
	}
	return MergeRequestResult{
		ID:                 mr.ID,
		UnitNodeID:         mr.UnitNodeID,
		FromClassification: mr.FromClassification,
		ToClassification:   mr.ToClassification,
		ProposedVersion:    mr.ProposedVersion,
		Title:              mr.Title,
		Body:               mr.Body,
		Status:             mr.Status,
		CreatedAt:          mr.CreatedAt,
	}, nil
}

// Unit_ListConflicts returns the syncer's surfaced same-unit pull conflicts.
func (f *Impl) Unit_ListConflicts(_ context.Context) ([]UnitConflictView, error) {
	if f.Syncer == nil {
		return nil, nil // offline → no conflicts surfaced yet
	}
	conflicts := f.Syncer.Conflicts()
	out := make([]UnitConflictView, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, UnitConflictView{
			UnitID:        c.UnitID,
			NodeID:        c.NodeID,
			LocalVersion:  c.LocalVersion,
			SyncedVersion: c.SyncedVersion,
			ServerVersion: c.ServerVersion,
		})
	}
	return out, nil
}

// Unit_ResolveMerge applies a whole-body MERGE resolution (WP17a).
func (f *Impl) Unit_ResolveMerge(ctx context.Context, unitID, resolvedBody string) error {
	if f.Units == nil {
		return ErrUnitsUnavailable
	}
	if _, err := f.Units.ResolveMerge(ctx, unitID, resolvedBody, nil); err != nil {
		return err
	}
	// Clear the surfaced conflict for this unit now that it has been resolved.
	if f.Syncer != nil {
		f.Syncer.ClearConflict(unitID)
	}
	return nil
}

// Unit_ResolveEnshrine applies an ENSHRINE resolution (WP17b): a coexisting unit
// plus a conflicts_with marker edge.
func (f *Impl) Unit_ResolveEnshrine(ctx context.Context, srcUnitID, enshrinedTitle, enshrinedBody, reason string) (string, error) {
	if f.Units == nil {
		return "", ErrUnitsUnavailable
	}
	newUnit, _, err := f.Units.ResolveEnshrine(ctx, srcUnitID, enshrinedTitle, enshrinedBody, reason)
	if err != nil {
		return "", err
	}
	if f.Syncer != nil {
		f.Syncer.ClearConflict(srcUnitID)
	}
	return newUnit.ID, nil
}

// Unit_ResolveLoadable returns the precedence-ordered loadable set with
// enshrined conflicts flagged (WP18).
func (f *Impl) Unit_ResolveLoadable(ctx context.Context, scope, scopeID string) ([]ResolvedUnitView, error) {
	if f.Units == nil {
		return nil, ErrUnitsUnavailable
	}
	filter := units.UnitFilter{ScopeID: scopeID}
	if scope != "" {
		filter.Scope = units.Scope(scope)
	}
	resolved, err := f.Units.ResolveLoadable(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedUnitView, 0, len(resolved))
	for _, r := range resolved {
		out = append(out, ResolvedUnitView{
			UnitID:         r.Unit.ID,
			Title:          r.Unit.Title,
			Body:           r.Unit.Body,
			Scope:          string(r.Unit.Scope),
			Classification: string(r.Unit.Classification),
			Flagged:        r.Flagged,
			PeerUnitID:     r.Marker.PeerUnitID,
			Reason:         r.Marker.Reason,
			Precedence:     r.Precedence,
		})
	}
	return out, nil
}
