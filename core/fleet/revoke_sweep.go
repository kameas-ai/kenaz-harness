// Package fleet — revoke_sweep.go
//
// RevocationSweeper (skill-library-01SKLIB01 WP03; fleet skill-library spec
// §5 H4, owner ruling OQ-5 = H4a): when the org REVOKES a catalog version,
// copies the user installed themselves (skills with Source=catalog,
// workflows with catalog install provenance) are uninstalled — via the
// UNSIGNED catalog list, never via a signed-bundle change.
//
// Safety rules (each pinned by a test):
//
//   - Uninstall ONLY on an explicit lifecycle="revoked" row for that catalog
//     id in GET /catalog/list. A row that is missing, active, deprecated, or
//     in an unknown state is never revocation. Fleet keeps revoked rows in
//     the no-param list by default for every caller who can see the item
//     (fleet answer OQ-2, 2026-10-06) — so the list is requested with NO
//     lifecycle filter (Client.List never sends one).
//   - A list failure of any kind skips the cycle: nothing is uninstalled on
//     ambiguity. 401/403/404 (signed out, tier lapse, a fleet without the
//     route) back off for a growing number of cycles.
//   - Never an org-mandated copy (the signed bundle + MandatedApplier own
//     those: revoke-of-pinned already drops the item from the bundle) and
//     never a user-authored skill/workflow (no catalog id). The candidate set
//     is catalog provenance only, and each uninstall RE-CHECKS provenance at
//     removal time, so a mandate or another version that took the local id
//     in between is left alone.
//
// Cadence: no timer of its own. The sweep piggybacks the config poller
// (ConfigPoller.SetAfterPoll), which runs every 5 minutes (with its own
// backoff on fetch failures). Chosen over the capability poller because a
// revoke of a PINNED version reaches devices through that same bundle poll:
// running the sweep right after it means the mandated copy and the user's
// own copies of a revoked version converge in the same tick (a mandate that
// had taken over the user's copy hands it back on withdrawal, and the sweep
// then removes the handed-back revoked copy). Offline devices catch up on
// their next poll — the accepted residual of the H4a ruling.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// LaneCatalogRevocation is the sync-lane health of the revocation sweep.
const LaneCatalogRevocation LaneName = "catalog_revocation"

// revocationMaxSkip caps the 401/403/404 backoff at 32 skipped config-poll
// cycles (~2.7h at the 5-minute cadence).
const revocationMaxSkip = 32

// RevocationTarget is one locally installed item with user catalog
// provenance.
type RevocationTarget struct {
	Kind      CatalogItemKind
	CatalogID string
	Version   string
	// LocalID is the skill store id or the workflow id.
	LocalID string
}

// RevocationWorkflows is the workflow half of the candidate set (the rpc
// layer adapts the workflows view; core/fleet must not import it).
type RevocationWorkflows interface {
	// CatalogInstalledWorkflows lists workflows whose install provenance is
	// the USER's catalog install (Source=catalog) with a recorded catalog
	// id. An unreadable provenance store is an error, never an empty list.
	CatalogInstalledWorkflows(ctx context.Context) ([]RevocationTarget, error)
	// RemoveRevokedWorkflow removes workflowID only while its provenance is
	// still the user's catalog install of catalogID. removed=false, nil
	// when anything else now owns the id.
	RemoveRevokedWorkflow(ctx context.Context, workflowID, catalogID string) (removed bool, err error)
}

// RevocationSweepResult summarises one Sweep.
type RevocationSweepResult struct {
	// Skipped is true when the cycle did not consult fleet (backoff, or no
	// catalog installs to check).
	Skipped bool
	// Checked is how many local catalog installs were compared.
	Checked int
	// Uninstalled lists what was removed.
	Uninstalled []RevocationTarget
}

// RevocationSweeper diffs local catalog installs against the catalog list.
// One instance per process; safe for concurrent use (Sweep serialises).
type RevocationSweeper struct {
	Client    *Client
	Skills    *slashcmd.SkillStore
	Registry  *slashcmd.Registry
	Workflows RevocationWorkflows
	Lanes     *SyncLanes
	Emitter   AuditEmitter
	// OnUninstalled, when set, is told about each uninstall (the rpc layer
	// announces capability:uninstalled so open surfaces repaint).
	OnUninstalled func(RevocationTarget)

	mu          sync.Mutex
	skip        int // cycles left to skip (401/403/404 backoff)
	nextSkip    int
	consecutive int
}

// Rewire updates the sweeper's dependencies under its lock (the rpc layer
// re-reads them each cycle: the client changes across sign-in).
func (s *RevocationSweeper) Rewire(fn func(*RevocationSweeper)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// ResetBackoff clears the 401/403/404 backoff (a fresh sign-in).
func (s *RevocationSweeper) ResetBackoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skip, s.nextSkip, s.consecutive = 0, 0, 0
}

// Sweep runs one cycle. It returns an error only for a failure worth
// reporting (list/enumeration/uninstall); every error path uninstalls
// nothing it was not certain about.
func (s *RevocationSweeper) Sweep(ctx context.Context) (RevocationSweepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.skip > 0 {
		s.skip--
		return RevocationSweepResult{Skipped: true}, nil
	}

	targets, err := s.candidatesLocked(ctx)
	if err != nil {
		s.failLocked("enumerate_failed", err)
		return RevocationSweepResult{}, err
	}
	if len(targets) == 0 {
		// Nothing installed from the catalog: no reason to call fleet.
		s.consecutive, s.nextSkip = 0, 0
		s.Lanes.RecordSuccess(LaneCatalogRevocation)
		return RevocationSweepResult{Skipped: true}, nil
	}

	items, err := s.Client.List(ctx, CatalogFilter{})
	if err != nil {
		return RevocationSweepResult{}, s.listFailedLocked(err)
	}
	revoked := make(map[string]CatalogItem)
	for _, it := range items {
		if it.IsRevoked() && it.ID != "" {
			revoked[it.ID] = it
		}
	}

	res := RevocationSweepResult{Checked: len(targets)}
	var errs []error
	for _, t := range targets {
		row, ok := revoked[t.CatalogID]
		if !ok {
			continue // active, deprecated, unknown, or absent: never revocation
		}
		removed, uerr := s.uninstallLocked(ctx, t)
		if uerr != nil {
			errs = append(errs, uerr)
			logging.L().Warn("fleet.revocation.uninstall_failed",
				"kind", string(t.Kind), "catalog_id", t.CatalogID, "err", uerr.Error())
			continue
		}
		if !removed {
			continue // provenance changed under us: someone else owns it now
		}
		res.Uninstalled = append(res.Uninstalled, t)
		logging.L().Info("fleet.revocation.uninstalled",
			"kind", string(t.Kind), "catalog_id", t.CatalogID, "version", t.Version, "local_id", t.LocalID,
			"revoked_version", row.Version)
		if s.Emitter != nil {
			_ = s.Emitter.EmitFleetEvent(ctx, contextaudit.KindFleetCatalogRevokedUninstalled,
				contextaudit.FleetCatalogRevokedUninstalledPayload{
					CatalogID: t.CatalogID, Kind: string(t.Kind), Version: t.Version,
					LocalID: t.LocalID, Reason: CatalogLifecycleRevoked,
				})
		}
		if s.OnUninstalled != nil {
			s.OnUninstalled(t)
		}
	}
	if err := errors.Join(errs...); err != nil {
		s.failLocked("uninstall_failed", err)
		return res, err
	}
	s.consecutive, s.nextSkip = 0, 0
	s.Lanes.RecordSuccess(LaneCatalogRevocation)
	return res, nil
}

// candidatesLocked is every skill with Source=catalog and a catalog id, and
// every workflow with user catalog provenance. Mandated (Source=mandated or
// OrgManaged) and user-authored (no catalog id) items are excluded here AND
// re-checked at uninstall.
func (s *RevocationSweeper) candidatesLocked(ctx context.Context) ([]RevocationTarget, error) {
	var out []RevocationTarget
	if s.Skills != nil {
		skills, err := s.Skills.List()
		if err != nil {
			return nil, fmt.Errorf("fleet/revocation: list skills: %w", err)
		}
		for _, sk := range skills {
			if isUserCatalogSkill(sk) {
				out = append(out, RevocationTarget{Kind: CatalogKindSkill, CatalogID: sk.CatalogID, Version: sk.Version, LocalID: sk.ID})
			}
		}
	}
	if s.Workflows != nil {
		wfs, err := s.Workflows.CatalogInstalledWorkflows(ctx)
		if err != nil {
			return nil, fmt.Errorf("fleet/revocation: list catalog workflows: %w", err)
		}
		for _, w := range wfs {
			if w.CatalogID != "" && w.LocalID != "" {
				w.Kind = CatalogKindWorkflow
				out = append(out, w)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].LocalID < out[j].LocalID
	})
	return out, nil
}

func isUserCatalogSkill(sk slashcmd.Skill) bool {
	return sk.Source == slashcmd.SkillSourceCatalog && !sk.OrgManaged && sk.CatalogID != ""
}

// uninstallLocked removes t through the consumer's existing path, after
// re-checking that it is still the user's catalog copy of t.CatalogID.
func (s *RevocationSweeper) uninstallLocked(ctx context.Context, t RevocationTarget) (bool, error) {
	switch t.Kind {
	case CatalogKindSkill:
		if s.Skills == nil || s.Registry == nil {
			return false, fmt.Errorf("fleet/revocation: skill %s: slash store not wired", t.CatalogID)
		}
		sk, err := s.Skills.Get(t.LocalID)
		if err != nil {
			return false, nil // already gone
		}
		if !isUserCatalogSkill(sk) || sk.CatalogID != t.CatalogID {
			return false, nil // a mandate, another version, or a local skill holds the id now
		}
		if err := slashcmd.LiveUnregister(s.Skills, s.Registry, t.LocalID); err != nil {
			if errors.Is(err, slashcmd.ErrSkillNotFound) {
				return false, nil
			}
			return false, fmt.Errorf("fleet/revocation: uninstall skill %s (%q): %w", t.CatalogID, t.LocalID, err)
		}
		return true, nil
	case CatalogKindWorkflow:
		if s.Workflows == nil {
			return false, fmt.Errorf("fleet/revocation: workflow %s: workflows consumer not wired", t.CatalogID)
		}
		return s.Workflows.RemoveRevokedWorkflow(ctx, t.LocalID, t.CatalogID)
	default:
		return false, nil
	}
}

// listFailedLocked classifies a list failure. Nothing is uninstalled.
func (s *RevocationSweeper) listFailedLocked(err error) error {
	if errors.Is(err, ErrFleetDisabled) {
		s.Lanes.RecordOff(LaneCatalogRevocation, "fleet_disabled")
		return nil
	}
	reason := ""
	var se *CatalogStatusError
	switch {
	case errors.Is(err, ErrTokenExpired), errors.Is(err, ErrNotSignedIn):
		// The client's own 401 handling failed to refresh (review F1): the
		// error never reaches a status code, but it is the same signed-out
		// state as a bare 401.
		reason = "signed_out"
	case errors.As(err, &se) && se.Status == http.StatusUnauthorized:
		reason = "signed_out"
	case errors.As(err, &se) && se.Status == http.StatusForbidden:
		reason = "not_entitled"
	case errors.As(err, &se) && se.Status == http.StatusNotFound:
		reason = "fleet_endpoint_unsupported"
	}
	if reason == "" {
		s.failLocked("list_failed", err)
		return err
	}
	// Signed out, tier lapse, or a fleet without the route: back off
	// (doubling, capped) instead of re-asking every cycle.
	if s.nextSkip == 0 {
		s.nextSkip = 1
	} else if s.nextSkip < revocationMaxSkip {
		s.nextSkip *= 2
	}
	s.skip = s.nextSkip
	s.consecutive++
	s.Lanes.RecordFailure(LaneCatalogRevocation, reason, err, s.consecutive,
		time.Now().Add(time.Duration(s.skip)*configPollInterval))
	return err
}

func (s *RevocationSweeper) failLocked(reason string, err error) {
	s.consecutive++
	s.Lanes.RecordFailure(LaneCatalogRevocation, reason, err, s.consecutive, time.Time{})
}
