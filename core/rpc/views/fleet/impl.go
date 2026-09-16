package fleet

import (
	"context"
	"errors"
	"fmt"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// optInPusher is the subset of *corefleet.TelemetryOptInPusher that Impl
// needs to push a consent tier's implied per-class opt-ins
// (corefleet.TierOptInUpdates) to the fleet store. Defined as an interface
// so impl_test.go can substitute a fake instead of spinning up a real fleet
// HTTP server for every SetTelemetryConsent test.
type optInPusher interface {
	Push(ctx context.Context, level corefleet.ConsentLevel) error
}

// Impl implements FleetAPI backed by core/fleet.TelemetryConsent and the
// Phase-3 unit-collaboration plumbing (units.Manager + corefleet.UnitSyncer).
//
// Units and Syncer may be nil on the test chassis / offline path; every unit
// method nil-guards and returns ErrUnitsUnavailable so the surface degrades
// gracefully rather than panicking.
type Impl struct {
	Consent *corefleet.TelemetryConsent

	// OptIns pushes the per-class opt-in vector a consent tier implies
	// (corefleet.TierOptInUpdates) to the fleet store whenever the tier
	// changes — see SetTelemetryConsent. Nil on the test chassis / OSS
	// build / fleet-disabled path, in which case SetTelemetryConsent stays
	// local-only (unchanged pre-fix behaviour).
	OptIns optInPusher

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

// SetTelemetryConsent validates the level string, delegates to
// TelemetryConsent.SetLevel (which enforces tier gating and is the LOCAL,
// offline-first source of truth for the consent level), and then pushes the
// per-class opt-in vector that level implies to the fleet store.
//
// # Failure semantics (owner ruling 2026-09-16)
//
// The local tier save always happens first and is unaffected by anything
// below it: this is an offline-first app, and the consent gate itself must
// never depend on a network round trip. Once SetLevel succeeds:
//
//   - fleet unreachable/disabled (corefleet.ErrFleetDisabled, e.g. signed
//     out or OSS build): treated as "nothing to push right now", not an
//     error — returns nil. TelemetryOptInPusher's own confirmed-state stays
//     untouched, so a later Reconcile (fleetEnroll, on next app start) or a
//     later tier change will retry once fleet becomes reachable.
//   - any other push failure (offline, 5xx, capability-gated, ...): SURFACED
//     as a returned error, wrapping the underlying failure, so the caller
//     (FleetTelemetryPanel.vue's saveConsent, via the existing errorMsg
//     display) sees it — silently dropping this under a different name is
//     the exact bug class this fix exists to end. The tier itself is still
//     saved; retry is durable (TelemetryOptInPusher persists the mismatch to
//     disk) via the same two triggers: the next tier change, or the next app
//     start (fleetEnroll calls Reconcile).
func (f *Impl) SetTelemetryConsent(ctx context.Context, level string) error {
	cl := corefleet.ConsentLevel(level)
	switch cl {
	case corefleet.ConsentNone, corefleet.ConsentAggregate, corefleet.ConsentFull:
		// valid
	default:
		return fmt.Errorf("unknown consent level %q; must be one of none, aggregate, full", level)
	}
	if err := f.Consent.SetLevel(cl); err != nil {
		// Tier-gating error (e.g. ErrTierInsufficient): local state is
		// unchanged, nothing to push.
		return err
	}
	if f.OptIns == nil {
		return nil // no fleet client wired — local-only path, unchanged behaviour
	}
	if err := f.OptIns.Push(ctx, cl); err != nil {
		if errors.Is(err, corefleet.ErrFleetDisabled) {
			return nil
		}
		return fmt.Errorf(
			"telemetry tier %q saved locally, but syncing per-class opt-ins to fleet failed "+
				"(will retry at the next tier change or app start): %w", level, err)
	}
	return nil
}

// ── Phase-3 unit collaboration ──────────────────────────────────────────────

// Unit_PromoteAsMergeRequest opens a merge request to promote unitID UP to
// toClassification (WP16). The source unit is left untouched; only the proposal
// travels (write-up-reviewed).
func (f *Impl) Unit_PromoteAsMergeRequest(ctx context.Context, unitID, toClassification, title, body string) (MergeRequestResult, error) {
	if f.Units == nil {
		return MergeRequestResult{}, ErrUnitsUnavailable
	}
	// Validate the target classification before checking the syncer so that
	// callers (and tests) always get the invalid-target error regardless of
	// whether fleet sync is enabled — no network round-trip needed to reject
	// a nonsensical target.
	toClass := units.Classification(toClassification)
	switch toClass {
	case units.ClassTeam, units.ClassOrg:
		// valid promotion targets
	default:
		return MergeRequestResult{}, fmt.Errorf("invalid promote target %q; must be team or org", toClassification)
	}
	if f.Syncer == nil {
		return MergeRequestResult{}, ErrSyncerUnavailable
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

// Unit_SyncStatus returns a snapshot of the unit syncer state. Returns a
// zero-value view when the syncer is nil (fleet disabled / offline).
func (f *Impl) Unit_SyncStatus(_ context.Context) (UnitSyncStatusView, error) {
	if f.Syncer == nil {
		return UnitSyncStatusView{}, nil
	}
	st := f.Syncer.Status()
	v := UnitSyncStatusView{
		Cursor:        st.Cursor,
		LastPullErr:   st.LastPullErr,
		LastPushErr:   st.LastPushErr,
		PushCount:     st.PushCount,
		PullCount:     st.PullCount,
		ConflictCount: st.ConflictCount,
	}
	if !st.LastPullAt.IsZero() {
		v.LastPullAt = st.LastPullAt.UTC().Format(time.RFC3339)
	}
	return v, nil
}
