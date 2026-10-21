// Package fleet — unit_sync.go
//
// UnitSyncer is the Phase-2 fleet sync engine for the unified Unit model.
// It is the fleet-touching half of the harness sync plumbing; the OSS-side
// units package (core/units) stays fleet-free (DIRECTIVE_001), and the
// no-fleet-imports boundary holds because this file lives in core/fleet.
//
// Responsibilities (WP13/WP14):
//
//   - PushDirty: PUSH dirty team/org Units to POST /api/v1/context/push,
//     mapping Unit→node via UnitMapper, then recording the acked version in
//     the units sync sidecar. Personal units are never pushed (NFR-005).
//   - PullDown: PULL via GET /api/v1/context/pull and map results back into
//     the local unit store as read layers, NEVER overwriting local personal
//     units, surfacing conflicts (server_version vs sidecar synced_version)
//     rather than blind-upserting.
//   - StartPoller: a background read-down-auto loop (mirrors the context
//     StartPoller pattern: 60s base, backoff on failure) that periodically
//     PullDown's org/team units.
//
// It reuses the context-graph wire client (Client.PostJSON / Client.Get) and
// the same push/pull endpoints + conflict shape proven by
// context_graph_sync.go.
//
// (unified-context-artifacts-01NCTXU01 / Phase 2 / WP13-WP14)
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// UnitStore is the narrow slice of units.Manager the syncer depends on.
// Declared as an interface so tests can substitute a stub and so the
// dependency surface stays explicit. *units.Manager satisfies it.
type UnitStore interface {
	List(ctx context.Context, filter units.UnitFilter) ([]units.Unit, error)
	Get(ctx context.Context, id string) (units.Unit, error)
	Create(ctx context.Context, u units.Unit) (units.Unit, error)
	Update(ctx context.Context, id, body string, metadata json.RawMessage) (units.Unit, error)
	ListDirty(ctx context.Context, classification units.Classification) ([]units.Unit, error)
	ListEdges(ctx context.Context, unitID string) ([]units.Edge, error)
	GetSyncState(ctx context.Context, unitID string) (units.SyncState, error)
	GetSyncStateByNodeID(ctx context.Context, nodeID string) (units.SyncState, error)
	UpsertSyncState(ctx context.Context, st units.SyncState) (units.SyncState, error)
	// CreateWithSyncState / UpdateWithSyncState write a pulled unit and its
	// sync baseline in ONE storage transaction (units-debt-01UNITD01 FR-4):
	// the pull path never leaves a unit without its baseline.
	CreateWithSyncState(ctx context.Context, u units.Unit, st units.SyncState) (units.Unit, units.SyncState, error)
	UpdateWithSyncState(ctx context.Context, id string, baseVersion int, body string, metadata json.RawMessage, st units.SyncState) (units.Unit, units.SyncState, error)
	// DeleteWithSyncState removes a pulled team/org unit, its history, its
	// edges and its sidecar in ONE storage transaction: the pull path's
	// tombstone apply. units.ErrPersonalUnit for a personal unit.
	DeleteWithSyncState(ctx context.Context, unitID string) (hadUnpushedEdits bool, err error)
	// DemoteToPersonal reclassifies a unit as personal and drops its
	// sidecar in ONE transaction, keeping content and history: the pull
	// path's tombstone for the user's OWN node.
	DemoteToPersonal(ctx context.Context, unitID string) error
}

// UnitConflict records a pull-time divergence: the server advanced a unit
// to ServerVersion while the local clone last synced at SyncedVersion and
// has its own LocalVersion. Surfaced (not silently applied) so Phase-3
// merge/enshrine can resolve it. A conflict exists when the local unit has
// un-synced local edits (LocalVersion > SyncedVersion) AND the server also
// moved on (ServerVersion > SyncedVersion).
type UnitConflict struct {
	UnitID        string `json:"unit_id"`
	NodeID        string `json:"node_id"`
	LocalVersion  int    `json:"local_version"`
	SyncedVersion int    `json:"synced_version"`
	ServerVersion int    `json:"server_version"`
}

// UnitSyncer pushes dirty team/org units up and pulls shared units down
// into the local unit store as read layers. Safe for concurrent use.
type UnitSyncer struct {
	client  *Client
	store   UnitStore
	mapper  *UnitMapper
	caps    *CapabilityPoller
	dataDir string
	// ids derives wire UUIDs for units that have never synced (WP01,
	// wire_id.go). Immutable after construction.
	ids *WireIDs

	mu          sync.RWMutex
	cursor      string
	lastPullAt  time.Time
	lastPullErr string
	lastPushErr string
	pushCount   int
	pullCount   int
	conflicts   []UnitConflict
	// skippedUnknownKinds counts pulled nodes that are not Unit-lane kinds
	// (WP03: skip and count — never abort the cursor). skippedInvalid
	// counts unit-lane nodes the local store refused as invalid (unknown
	// scope / classification / load policy). Both cumulative.
	skippedUnknownKinds int
	skippedInvalid      int
	// pushRefused counts dirty units refused BEFORE the wire (non-pushable
	// kind, capability word in metadata); they stay dirty.
	pushRefused int
	// pushHeldLoadAlways counts load_policy=always units held back because
	// the identity's roles are unknown (review F9); they stay dirty.
	pushHeldLoadAlways int
	// Tombstone outcomes (see applyTombstone), cumulative:
	// tombstonesApplied — a non-owned unit was deleted;
	// tombstoneDroppedLocalEdits — the subset that had un-pushed local edits;
	// tombstonesDemoted — the user's OWN unit was kept as personal;
	// tombstonesHeldIdentityUnknown — a GAUGE, not cumulative: tombstones
	// held (unit kept) in the most recent pull because the signed-in
	// identity was unknown or for another org. While it is > 0 the cursor
	// is not advanced, so the page is retried on the next tick.
	tombstonesApplied             int
	tombstoneDroppedLocalEdits    int
	tombstonesDemoted             int
	tombstonesHeldIdentityUnknown int

	// selfIdentity returns the signed-in fleet identity (zero = unknown).
	// Read per tombstone from the enrolled identity under dataDir, the same
	// source the Contexts view uses to recognise the user's own pulled
	// entries (WithSelfIdentityFromDataDir). Tests may override it.
	selfIdentity func() Identity

	stopCh chan struct{}
	once   sync.Once

	// lanes receives the read-down poll's health (fleet-session-truth-
	// 01DOGF0A FR-6). Set via SetLanes before StartPoller; nil = unreported.
	lanes *SyncLanes
}

// SetLanes wires the shared lane board the poll reports into. Call before
// StartPoller.
func (s *UnitSyncer) SetLanes(l *SyncLanes) {
	s.mu.Lock()
	s.lanes = l
	s.mu.Unlock()
}

// NewUnitSyncer constructs a UnitSyncer. The syncer does not poll until
// StartPoller is called. The cursor is restored from disk (reusing the unit
// cursor file under dataDir).
func NewUnitSyncer(client *Client, store UnitStore, mapper *UnitMapper, caps *CapabilityPoller, dataDir string) *UnitSyncer {
	s := &UnitSyncer{
		client:  client,
		store:   store,
		mapper:  mapper,
		caps:    caps,
		dataDir: dataDir,
		ids:     NewWireIDs(dataDir),
		stopCh:  make(chan struct{}),
	}
	s.selfIdentity = func() Identity {
		if dataDir == "" {
			return Identity{}
		}
		id, err := LoadIdentity(dataDir)
		if err != nil {
			return Identity{}
		}
		return id
	}
	if c, err := loadUnitCursor(dataDir); err == nil {
		s.cursor = c
	}
	return s
}

// ── Capability gating ──────────────────────────────────────────────────────

func (s *UnitSyncer) canSync() error {
	if s.client == nil || s.client.isNop {
		return ErrFleetDisabled
	}
	if s.caps == nil {
		return ErrCapabilityNotInTier
	}
	cur := s.caps.Current()
	return cur.Require(CapSharedTeamGraph)
}

// orgPausedNow reports a staff pause hold from the capability snapshot or
// the client's last org_paused refusal.
func (s *UnitSyncer) orgPausedNow() bool {
	if s.caps != nil && s.caps.Current().Paused {
		return true
	}
	return s.client != nil && s.client.OrgPause().Paused
}

// ── Push-up ─────────────────────────────────────────────────────────────────

// PushDirty pushes every dirty team and org unit to the fleet context graph,
// recording the acked version in the sidecar. Personal units are never
// touched. Returns the number of nodes accepted by the server across both
// classifications. Server-reported version conflicts are returned (callers
// may inspect the result) but do NOT advance the sidecar.
func (s *UnitSyncer) PushDirty(ctx context.Context) (int, error) {
	if err := s.canSync(); err != nil {
		if errors.Is(err, ErrFleetDisabled) || errors.Is(err, ErrNotSignedIn) {
			return 0, nil // offline / signed-out → local-only
		}
		return 0, nil // capability absent → local-only, no error
	}

	total := 0
	for _, class := range []units.Classification{units.ClassTeam, units.ClassOrg} {
		n, err := s.pushClass(ctx, class)
		if err != nil {
			s.mu.Lock()
			s.lastPushErr = err.Error()
			s.mu.Unlock()
			return total, err
		}
		total += n
	}
	s.mu.Lock()
	s.lastPushErr = ""
	s.pushCount += total
	s.mu.Unlock()
	return total, nil
}

// pushClass pushes all dirty units of one classification in a single
// two-phase (nodes then edges) push request.
func (s *UnitSyncer) pushClass(ctx context.Context, class units.Classification) (int, error) {
	dirty, err := s.store.ListDirty(ctx, class)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit push: list dirty: %w", err)
	}
	return s.pushUnits(ctx, class, dirty)
}

// PushUnit pushes ONE team/org unit now (dirty or not) — the "ensure the
// node exists on fleet" step before a merge request (review F2). Returns
// the unit's wire node id. Personal units are refused.
func (s *UnitSyncer) PushUnit(ctx context.Context, unitID string) (string, error) {
	if err := s.canSync(); err != nil {
		return "", err
	}
	u, err := s.store.Get(ctx, unitID)
	if err != nil {
		return "", fmt.Errorf("fleet: push unit: %w", err)
	}
	if _, ok := ClassificationForUnit(u.Classification); !ok {
		return "", ErrPersonalLayerNotSyncable
	}
	n, err := s.pushUnits(ctx, u.Classification, []units.Unit{u})
	if err != nil {
		return "", err
	}
	if n == 0 {
		if st, gerr := s.store.GetSyncState(ctx, u.ID); gerr != nil || st.SyncedLocalVersion != u.Version {
			return "", fmt.Errorf("fleet: push unit %s: not accepted by fleet (refused locally, conflicted or rejected)", u.ID)
		}
	}
	return s.WireNodeID(ctx, u.ID), nil
}

// pushUnits pushes the given units of one classification in a single
// two-phase (nodes then edges) request. Edges are included only when BOTH
// endpoints are in this push or already on fleet (a synced sidecar) — an
// edge to a personal / never-pushed unit would 400 the whole batch
// (missing_node_reference).
func (s *UnitSyncer) pushUnits(ctx context.Context, class units.Classification, dirty []units.Unit) (int, error) {
	if len(dirty) == 0 {
		return 0, nil
	}

	nodes := make([]contextNodeInput, 0, len(dirty))
	pushed := make([]units.Unit, 0, len(dirty))
	// wireOf maps a pushed unit id to the wire UUID it was sent as, so the
	// response's conflicts / rejections (keyed by wire id) and the sidecar
	// write resolve back to the local unit.
	wireOf := make(map[string]string, len(dirty))
	seenEdge := map[string]bool{}
	edges := make([]contextEdgeInput, 0)
	type edgeCand struct {
		wire     contextEdgeInput
		from, to string
	}
	var candidates []edgeCand

	for _, u := range dirty {
		node, ok, err := s.mapper.MapUnitToNode(u)
		if errors.Is(err, ErrLoadPolicyRolesUnknown) {
			logging.L().Warn("fleet.unit.push.held_load_always", "unit_id", u.ID)
			s.mu.Lock()
			s.pushHeldLoadAlways++
			s.mu.Unlock()
			continue
		}
		if errors.Is(err, ErrUnitKindNotPushable) || errors.Is(err, ErrKindNotKnowledge) {
			// Refused before the wire (fleet would 400 the WHOLE batch):
			// skip this unit, keep it dirty, keep pushing the rest.
			logging.L().Warn("fleet.unit.push.refused_locally", "unit_id", u.ID, "err", err.Error())
			s.mu.Lock()
			s.pushRefused++
			s.mu.Unlock()
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("fleet: unit push: map %s: %w", u.ID, err)
		}
		if !ok {
			continue // personal — defensively skipped (ListDirty already filters)
		}
		node.ID = s.WireNodeID(ctx, u.ID)
		wireOf[u.ID] = node.ID
		nodes = append(nodes, node)
		pushed = append(pushed, u)

		// Include lineage edges whose endpoints are both in this push set.
		ues, err := s.store.ListEdges(ctx, u.ID)
		if err != nil {
			return 0, fmt.Errorf("fleet: unit push: list edges %s: %w", u.ID, err)
		}
		for _, e := range ues {
			if seenEdge[e.ID] {
				continue
			}
			wire, ok := s.mapper.MapEdgeToWire(e, class)
			if !ok {
				continue
			}
			wire.ID = s.ids.For(WireLaneUnitEdge, e.ID)
			wire.FromNodeID = s.WireNodeID(ctx, e.FromID)
			wire.ToNodeID = s.WireNodeID(ctx, e.ToID)
			seenEdge[e.ID] = true
			candidates = append(candidates, edgeCand{wire: wire, from: e.FromID, to: e.ToID})
		}
	}
	if len(nodes) == 0 {
		return 0, nil
	}
	onFleet := func(unitID string) bool {
		if _, ok := wireOf[unitID]; ok {
			return true
		}
		st, err := s.store.GetSyncState(ctx, unitID)
		return err == nil && IsWireUUID(st.NodeID)
	}
	for _, c := range candidates {
		if onFleet(c.from) && onFleet(c.to) {
			edges = append(edges, c.wire)
		}
	}

	req := contextPushRequest{Nodes: nodes, Edges: edges}
	resp, err := s.client.PostJSON(ctx, "/api/v1/context/push", req)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit push: %w", err)
	}
	defer drain(resp)

	if resp.StatusCode != http.StatusOK {
		// Map by the envelope's code (not_team_member,
		// load_policy_requires_admin, capability_not_in_tier, lint_blocked,
		// …) — not every 403 is a tier problem (context_push_errors.go).
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return 0, parseContextPushError("unit push", resp.StatusCode, errBody)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit push read: %w", err)
	}
	var result ContextPushResult
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("fleet: unit push parse: %w", err)
	}

	// Index conflicts AND per-item rejections (kenaz-fleet PR #173) so we
	// don't advance the sidecar for either: a rejected unit was not stored
	// server-side and must stay dirty.
	conflicted := map[string]bool{}
	for _, c := range result.Conflicts {
		conflicted[c.NodeID] = true
	}
	for _, r := range result.Rejected {
		if r.Kind == "node" {
			conflicted[r.ID] = true
		}
	}
	if len(result.Rejected) > 0 {
		logging.L().Warn("fleet.unit.push.rejected", "rejected_count", len(result.Rejected))
	}

	classStr := ""
	if cls, ok := ClassificationForUnit(class); ok {
		classStr = string(cls)
	}
	now := time.Now().UTC()
	for _, u := range pushed {
		if conflicted[wireOf[u.ID]] {
			continue // conflicted or rejected — leave sidecar untouched (stays dirty)
		}
		// After a successful push-ack both baselines advance to the pushed
		// unit's local version. The server echoes back the same version we
		// sent (the push is our version), so SyncedServerVersion = SyncedLocalVersion
		// = u.Version immediately after a push.
		if _, err := s.store.UpsertSyncState(ctx, units.SyncState{
			UnitID:              u.ID,
			NodeID:              wireOf[u.ID], // the wire UUID (WP01) — pull resolves it back via GetSyncStateByNodeID
			SyncedServerVersion: u.Version,
			SyncedLocalVersion:  u.Version,
			Classification:      classStr,
			LastSynced:          now,
		}); err != nil {
			return 0, fmt.Errorf("fleet: unit push: sidecar %s: %w", u.ID, err)
		}
	}
	return result.AcceptedNodes, nil
}

// WireNodeID resolves the fleet node UUID for a local unit id (WP01). A unit
// that has synced before keeps the NodeID its sidecar recorded — for a
// pulled unit that is the server's id, for a pushed one the id it was pushed
// under — so the mapping is durable in the units store, not just derivable.
// A never-synced unit (or a legacy sidecar row whose NodeID is the raw ULID
// from before UUIDs, which fleet never accepted) gets the deterministic
// per-install UUIDv5 (wire_id.go).
func (s *UnitSyncer) WireNodeID(ctx context.Context, unitID string) string {
	if unitID == "" {
		return ""
	}
	if s.store != nil {
		if st, err := s.store.GetSyncState(ctx, unitID); err == nil && IsWireUUID(st.NodeID) {
			return st.NodeID
		}
	}
	return s.ids.For(WireLaneUnit, unitID)
}

// ── Pull-down (read-down-auto) ───────────────────────────────────────────────

// PullDown fetches shared-graph deltas since the stored cursor and maps them
// into the local unit store as read layers. It NEVER overwrites a local
// personal unit and NEVER blind-upserts over un-synced local edits: when the
// server advanced a unit that the local clone has also edited since the last
// sync, the divergence is recorded as a UnitConflict (surfaced via
// Conflicts()) and the local body is left intact for Phase-3 resolution.
//
// Returns the number of nodes applied (created or fast-forwarded).
func (s *UnitSyncer) PullDown(ctx context.Context) (int, error) {
	if err := s.canSync(); err != nil {
		if errors.Is(err, ErrFleetDisabled) || errors.Is(err, ErrNotSignedIn) {
			return 0, nil
		}
		return 0, nil
	}

	s.mu.RLock()
	cursor := s.cursor
	s.mu.RUnlock()

	// The Unit lane pulls only unit kinds (WP03 lane split by kind).
	urlPath := lanePullPath(unitLaneKinds, cursor)

	resp, err := s.client.Get(ctx, urlPath)
	if err != nil {
		if errors.Is(err, ErrNotSignedIn) {
			return 0, nil
		}
		s.mu.Lock()
		s.lastPullErr = err.Error()
		s.mu.Unlock()
		return 0, fmt.Errorf("fleet: unit pull: %w", err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusForbidden {
		return 0, nil // unentitled → local-only
	}
	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("fleet: unit pull status %d", resp.StatusCode)
		s.mu.Lock()
		s.lastPullErr = e.Error()
		s.mu.Unlock()
		return 0, e
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit pull read: %w", err)
	}
	var pullResp contextPullResponse
	if err := json.Unmarshal(raw, &pullResp); err != nil {
		return 0, fmt.Errorf("fleet: unit pull parse: %w", err)
	}

	applied := 0
	skippedKinds, skippedInvalid, held := 0, 0, 0
	for _, n := range pullResp.Nodes {
		// Another lane's node — a Curated "guidance", a bootstrap
		// "project", or a kind this build has never heard of (a server that
		// ignores ?kind= sends them all). Skip and count: before WP03 one
		// such node made applyPulledNode fail and PullDown return BEFORE the
		// cursor advanced, stalling the lane on that page forever (§0-E).
		if !unitLaneAccepts(n) {
			skippedKinds++
			continue
		}
		ok, err := s.applyPulledNode(ctx, n)
		if err != nil {
			if errors.Is(err, errTombstoneHeld) {
				// Ownership undecidable yet (identity not enrolled, or for
				// another org): the unit is kept, the rest of the page is
				// applied, and the cursor is NOT advanced below so this
				// page is retried once the identity exists.
				held++
				continue
			}
			if isUnitValidationErr(err) {
				// The node itself is invalid for the local store (unknown
				// scope / classification / load policy): a permanent
				// property of the node, so retrying cannot help. Skip it.
				logging.L().Warn("fleet.unit.pull.skipped_invalid", "node_id", n.ID, "err", err.Error())
				skippedInvalid++
				continue
			}
			// A storage failure is transient: abort before the cursor
			// advances so the page is retried.
			return applied, err
		}
		if ok {
			applied++
		}
	}

	s.mu.Lock()
	wasHeld := s.tombstonesHeldIdentityUnknown > 0
	s.tombstonesHeldIdentityUnknown = held
	switch {
	case held > 0 && !wasHeld:
		logging.L().Info("fleet.unit.pull.held_identity_pending", "held", held)
	case held == 0 && wasHeld:
		logging.L().Info("fleet.unit.pull.held_identity_cleared")
	}
	if held == 0 {
		// Counted only when the page is committed: a held page is re-applied
		// next tick (idempotently: first-sight rows now have a sidecar,
		// fast-forwards are at the baseline, applied tombstones have no
		// sidecar left), and would otherwise be counted twice.
		s.skippedUnknownKinds += skippedKinds
		s.skippedInvalid += skippedInvalid
		s.pullCount += len(pullResp.Nodes)
		if pullResp.Cursor != "" {
			s.cursor = pullResp.Cursor
			if s.dataDir != "" {
				if saveErr := saveUnitCursor(s.dataDir, s.cursor); saveErr != nil {
					logging.L().Warn("fleet.unit.cursor_save_failed", "err", saveErr.Error())
				}
			}
		}
	}
	s.lastPullAt = time.Now().UTC()
	s.lastPullErr = ""
	s.mu.Unlock()

	return applied, nil
}

// isUnitValidationErr reports whether err is the local store refusing a node
// as invalid (a permanent property of the node, not a transient failure).
func isUnitValidationErr(err error) bool {
	return errors.Is(err, units.ErrUnsupportedKind) || errors.Is(err, units.ErrUnsupportedScope) ||
		errors.Is(err, units.ErrUnsupportedClassification) || errors.Is(err, units.ErrUnsupportedLoadPolicy)
}

// applyPulledNode maps one pulled node into the local store and returns
// whether a write was applied. It resolves the local unit via the sidecar
// node-id index (no re-discovery). Conflict rules (3-way baseline):
//
//   - Tombstones (DeletedAt != nil) remove the local copy: see applyTombstone.
//   - A node mapping to a local personal unit is never applied (defensive —
//     personal units are never pushed, so this should not occur).
//   - First sight (no sidecar): create the unit locally as a read layer and
//     record BOTH baselines (SyncedServerVersion = server.Version,
//     SyncedLocalVersion = created.Version).
//   - Known unit: derive serverChanged and localChanged from the two
//     independent baselines:
//     serverChanged = server.Version > st.SyncedServerVersion
//     localChanged  = local.Version  > st.SyncedLocalVersion
//     - !serverChanged: no-op (server hasn't advanced; local may be dirty and
//       will push on the next PushDirty cycle).
//     - serverChanged && localChanged: CONFLICT — surface, do NOT apply.
//     - serverChanged && !localChanged: clean fast-forward — apply + update
//       BOTH baselines.
//
// The two-counter invariant prevents the pre-1102 bug where a unit pulled at
// server version N was created with local Version=0 and sidecar baseline=N;
// after a local edit (local.Version=1) a later server delta checked
// local.Version(1) > baseline(N) = FALSE and silently overwrote the edit.
func (s *UnitSyncer) applyPulledNode(ctx context.Context, n ContextPulledNode) (bool, error) {
	if n.DeletedAt != nil {
		return s.applyTombstone(ctx, n)
	}
	mapped, ok, err := s.mapper.PulledNodeToUnit(n)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // classification doesn't map locally — skip
	}

	st, err := s.store.GetSyncStateByNodeID(ctx, n.ID)
	if errors.Is(err, units.ErrSyncStateNotFound) {
		// A unit already holding this id with no sidecar:
		//   - personal: the user's own unit an owner tombstone demoted
		//     (applyTombstone). It stays; the re-appearing node gets a fresh
		//     local id beside it rather than a primary-key clash that would
		//     fail (and stall) the pull.
		//   - team/org: a legacy orphan (its sidecar was lost). Adopt it
		//     (adoptOrphan): never edited locally → clean fast-forward;
		//     edited → kept as an un-synced local edit, and a conflict if it
		//     differs from the server, so nothing is overwritten.
		if mapped.ID != "" {
			existing, gerr := s.store.Get(ctx, mapped.ID)
			switch {
			case gerr == nil && existing.Classification == units.ClassPersonal:
				mapped.ID = ""
			case gerr == nil:
				return s.adoptOrphan(ctx, n, existing, mapped)
			case !errors.Is(gerr, units.ErrUnitNotFound):
				return false, fmt.Errorf("fleet: unit pull: id check %s: %w", n.ID, gerr)
			}
		}
		// First sight of this server node → create a fresh local read layer
		// AND record BOTH baselines, in one storage transaction
		// (units-debt-01UNITD01 FR-4, C-review D-1). Two separate writes
		// could leave a unit with no sidecar row: the next PushDirty would
		// read it as never-synced and push it back as locally-new, and the
		// next pull could not resolve this node id to it and would try to
		// create it again. SyncedServerVersion = server's version at
		// creation; SyncedLocalVersion is set by the store to the created
		// unit's Version (always 0 — Create forces Version=0).
		if _, _, cerr := s.store.CreateWithSyncState(ctx, mapped, units.SyncState{
			NodeID:              n.ID,
			SyncedServerVersion: n.Version,
			Classification:      string(n.Classification),
			LastSynced:          time.Now().UTC(),
		}); cerr != nil {
			return false, fmt.Errorf("fleet: unit pull: create %s: %w", n.ID, cerr)
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("fleet: unit pull: sidecar lookup %s: %w", n.ID, err)
	}

	local, err := s.store.Get(ctx, st.UnitID)
	if err != nil {
		return false, fmt.Errorf("fleet: unit pull: get local %s: %w", st.UnitID, err)
	}
	// Never touch a local personal unit (defensive; should not happen).
	if local.Classification == units.ClassPersonal {
		return false, nil
	}

	// 3-way baseline comparison. Each counter space is tracked independently.
	serverChanged := n.Version > st.SyncedServerVersion
	localChanged := local.Version > st.SyncedLocalVersion

	if !serverChanged {
		// Server hasn't advanced beyond our last sync — nothing to apply.
		// Local edits (localChanged) will be offered to the server on the
		// next PushDirty cycle.
		return false, nil
	}

	if localChanged {
		// Both sides moved: genuine conflict. Surface it, do NOT blind-upsert.
		s.recordConflict(UnitConflict{
			UnitID:        local.ID,
			NodeID:        n.ID,
			LocalVersion:  local.Version,
			SyncedVersion: st.SyncedServerVersion, // caller-facing "synced" = server baseline
			ServerVersion: n.Version,
		})
		return false, nil
	}

	// Clean fast-forward: server advanced, local is unchanged since last sync.
	// Apply the server body and advance BOTH baselines in one storage
	// transaction (units-debt-01UNITD01 FR-4). Split, a failure after the
	// update left the local Version ahead of SyncedLocalVersion: the next
	// pull read the server's own body as an un-synced local edit and
	// surfaced a conflict of the unit against itself. The store sets
	// SyncedLocalVersion to the unit's Version after the bump.
	//
	// local.Version is passed as the base and re-checked inside the write
	// transaction: a local edit that lands after the Get above makes the
	// fast-forward NOT clean, and it takes the same path as any other
	// both-sides-moved case — surfaced as a conflict, local body kept,
	// sidecar not advanced.
	if _, _, err := s.store.UpdateWithSyncState(ctx, local.ID, local.Version, mapped.Body, mapped.Metadata, units.SyncState{
		NodeID:              n.ID,
		SyncedServerVersion: n.Version, // server counter at this pull
		Classification:      string(n.Classification),
		LastSynced:          time.Now().UTC(),
	}); err != nil {
		if errors.Is(err, units.ErrVersionConflict) {
			latest := local.Version + 1 // at least one local edit raced in
			if cur, gerr := s.store.Get(ctx, local.ID); gerr == nil {
				latest = cur.Version
			}
			s.recordConflict(UnitConflict{
				UnitID:        local.ID,
				NodeID:        n.ID,
				LocalVersion:  latest,
				SyncedVersion: st.SyncedServerVersion,
				ServerVersion: n.Version,
			})
			return false, nil
		}
		return false, fmt.Errorf("fleet: unit pull: update %s: %w", local.ID, err)
	}
	return true, nil
}

// applyTombstone handles a node the server now serves as a tombstone
// (deleted_at set). Fleet sends that shape for:
//
//   - FR-103 delete, by the author or by someone else: a projects:admin
//     project delete soft-deletes every project-scoped node regardless of
//     author, and a retention downgrade sets deleted_at without a version
//     bump. So the OWNER does receive delete tombstones for their own nodes.
//   - Quarantine (kenaz-fleet #325): a node whose content may contain a
//     secret reaches every member but its author as a content-free
//     tombstone (deleted_at = quarantined_at, version unchanged). The
//     author receives the full node and keeps it. A quarantine tombstone is
//     therefore never sent to the owner.
//
// Version is deliberately NOT compared: neither quarantine nor a retention
// downgrade bumps it.
//
// Rule, decided on the node's owner_user_id against the signed-in user:
//
//   - Owner (owner_user_id == the user): DEMOTE. The unit is the user's own
//     work, so it is reclassified personal and only its sidecar is removed,
//     in one transaction: body, history, edges and any un-pushed edits stay;
//     it is never pushed again and the node id no longer maps to it (spec
//     008's owner-directed default: a downgrade "demotes the node to
//     personal"). A later re-appearance is a first sight and arrives as a
//     separate team/org unit beside the personal copy.
//   - Not the owner: DELETE the unit, its whole version history, every edge
//     touching it and its sidecar in one transaction, so the withdrawn
//     content is gone, including when the unit carries un-pushed local
//     edits (counted, logged by ids). Those edits could not have reached
//     the team: Fleet accepts an edit to a node only from its owner. Deleting
//     the sidecar makes a clean re-appearance (the author's lint-passing edit
//     clears a quarantine) take the first-sight path and recreate the unit.
//   - Identity unknown (no enrolled user id or org id) or for a different
//     org than the node: HOLD. Non-ownership cannot be proven, so the
//     user's data wins: the unit is kept untouched and errTombstoneHeld
//     tells PullDown not to advance the cursor, so the page is retried
//     every tick until the identity exists (the lane reports
//     ReasonIdentityPending, not an error).
//
// Any pending conflict for the unit is cleared when it is deleted or
// demoted. Personal units are never touched (they never get a sidecar row,
// and the store refuses to delete one). Nothing here logs a title or body.
// errTombstoneHeld reports a tombstone whose ownership cannot be decided
// yet; PullDown keeps the cursor so it is retried. Never surfaced.
var errTombstoneHeld = errors.New("fleet: unit pull: tombstone held until the fleet identity is known")

// ReasonIdentityPending is the unit-poll lane's "off" reason while a pulled
// page is held for an unknown fleet identity (see applyTombstone). Not a
// failure: the poll retries every tick and clears it once enrolled.
const ReasonIdentityPending = "identity_pending"

func (s *UnitSyncer) applyTombstone(ctx context.Context, n ContextPulledNode) (bool, error) {
	if n.ID == "" {
		return false, nil
	}
	st, err := s.store.GetSyncStateByNodeID(ctx, n.ID)
	if errors.Is(err, units.ErrSyncStateNotFound) {
		return false, nil // never pulled here (or personal): nothing to remove
	}
	if err != nil {
		return false, fmt.Errorf("fleet: unit pull: tombstone sidecar lookup %s: %w", n.ID, err)
	}
	local, err := s.store.Get(ctx, st.UnitID)
	switch {
	case errors.Is(err, units.ErrUnitNotFound):
		// Orphaned sidecar: no content to keep or remove; drop the mapping.
		if _, derr := s.store.DeleteWithSyncState(ctx, st.UnitID); derr != nil {
			return false, fmt.Errorf("fleet: unit pull: tombstone orphan sidecar %s: %w", st.UnitID, derr)
		}
		return false, nil
	case err != nil:
		return false, fmt.Errorf("fleet: unit pull: tombstone get local %s: %w", st.UnitID, err)
	}
	if local.Classification == units.ClassPersonal {
		return false, nil
	}

	var self Identity
	if s.selfIdentity != nil {
		self = s.selfIdentity()
	}
	if self.UserID == "" || self.OrgID == "" || !strings.EqualFold(self.OrgID, n.OrgID) {
		// Unknown (not enrolled yet, sign-in still in flight, identity
		// write failed) or for another org: ownership cannot be decided,
		// so the user's data wins. PullDown keeps the cursor so this
		// tombstone is retried, not lost.
		return false, errTombstoneHeld // logged once per transition by PullDown
	}

	if n.OwnerUserID != "" && strings.EqualFold(n.OwnerUserID, self.UserID) {
		if err := s.store.DemoteToPersonal(ctx, st.UnitID); err != nil {
			return false, fmt.Errorf("fleet: unit pull: tombstone demote %s: %w", st.UnitID, err)
		}
		s.mu.Lock()
		s.tombstonesDemoted++
		s.dropConflictLocked(st.UnitID)
		s.mu.Unlock()
		logging.L().Info("fleet.unit.pull.tombstone_demoted_own_unit",
			"unit_id", st.UnitID, "node_id", n.ID)
		return true, nil
	}

	hadUnpushed, err := s.store.DeleteWithSyncState(ctx, st.UnitID)
	if err != nil {
		if errors.Is(err, units.ErrPersonalUnit) {
			return false, nil
		}
		return false, fmt.Errorf("fleet: unit pull: tombstone delete %s: %w", st.UnitID, err)
	}
	s.mu.Lock()
	s.tombstonesApplied++
	if hadUnpushed {
		s.tombstoneDroppedLocalEdits++
	}
	s.dropConflictLocked(st.UnitID)
	s.mu.Unlock()
	if hadUnpushed {
		logging.L().Warn("fleet.unit.pull.tombstone_dropped_local_edits",
			"unit_id", st.UnitID, "node_id", n.ID)
	}
	return true, nil
}

// dropConflictLocked forgets any surfaced conflict for unitID. Caller holds
// s.mu.
func (s *UnitSyncer) dropConflictLocked(unitID string) {
	kept := s.conflicts[:0]
	for _, c := range s.conflicts {
		if c.UnitID != unitID {
			kept = append(kept, c)
		}
	}
	s.conflicts = kept
}

// adoptOrphan attaches a pulled node to a team/org unit that already holds
// its id but has no sidecar (a legacy orphan). The unit's own history decides,
// since there is no baseline to compare against:
//
//   - Version 0 (never edited here): clean fast-forward to the server's body
//     with both baselines, in one transaction (UpdateWithSyncState).
//   - Edited (Version > 0) and identical to the server: baselined as clean.
//   - Edited and different: the sidecar records the server version with
//     SyncedLocalVersion 0, so the unit stays localChanged and every later
//     server change is a conflict, never an overwrite; a conflict is recorded
//     now so the user resolves it. Nothing is overwritten.
func (s *UnitSyncer) adoptOrphan(ctx context.Context, n ContextPulledNode, existing, mapped units.Unit) (bool, error) {
	base := units.SyncState{
		NodeID:              n.ID,
		SyncedServerVersion: n.Version,
		Classification:      string(n.Classification),
		LastSynced:          time.Now().UTC(),
	}
	if existing.Version == 0 {
		_, _, err := s.store.UpdateWithSyncState(ctx, existing.ID, 0, mapped.Body, mapped.Metadata, base)
		if err == nil {
			logging.L().Warn("fleet.unit.pull.adopted_orphan_unit",
				"unit_id", existing.ID, "node_id", n.ID, "mode", "fast_forward")
			return true, nil
		}
		if !errors.Is(err, units.ErrVersionConflict) {
			return false, fmt.Errorf("fleet: unit pull: adopt orphan %s: %w", n.ID, err)
		}
		// A local edit raced in: treat it as edited.
		if cur, gerr := s.store.Get(ctx, existing.ID); gerr == nil {
			existing = cur
		} else {
			existing.Version++
		}
	}
	same := existing.Body == mapped.Body && sameMetadata(existing.Metadata, mapped.Metadata)
	base.UnitID = existing.ID
	if same {
		base.SyncedLocalVersion = existing.Version
	}
	if _, err := s.store.UpsertSyncState(ctx, base); err != nil {
		return false, fmt.Errorf("fleet: unit pull: adopt orphan %s: %w", n.ID, err)
	}
	mode := "clean"
	if !same {
		mode = "conflict"
		s.recordConflict(UnitConflict{
			UnitID:        existing.ID,
			NodeID:        n.ID,
			LocalVersion:  existing.Version,
			SyncedVersion: 0, // no known common baseline
			ServerVersion: n.Version,
		})
	}
	logging.L().Warn("fleet.unit.pull.adopted_orphan_unit",
		"unit_id", existing.ID, "node_id", n.ID, "mode", mode)
	return false, nil
}

// sameMetadata compares two metadata documents semantically (empty / null
// equal "{}"; key order and whitespace ignored).
func sameMetadata(a, b json.RawMessage) bool {
	norm := func(r json.RawMessage) any {
		if len(r) == 0 {
			return map[string]any{}
		}
		var v any
		if err := json.Unmarshal(r, &v); err != nil {
			return string(r)
		}
		if v == nil {
			return map[string]any{}
		}
		return v
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

func (s *UnitSyncer) recordConflict(c UnitConflict) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.conflicts {
		if existing.UnitID == c.UnitID {
			s.conflicts[i] = c // keep the latest observation
			return
		}
	}
	s.conflicts = append(s.conflicts, c)
}

// ── Background poller (read-down-auto) ───────────────────────────────────────

const (
	unitPollBaseInterval = 60 * time.Second
	unitPollBackoff1     = 300 * time.Second
	unitPollBackoff2     = 1800 * time.Second
	unitPollMaxErrors    = 2

	// unitPollWarnThreshold is the consecutive-failure count at which a
	// failing poll becomes a WARN and a degraded unit-poll lane
	// (fleet-session-truth-01DOGF0A, dogfood B3b). Below it a failure is
	// Debug: the counter is reset by the next SUCCESSFUL PullDown (any nil
	// return — including the no-op returns for signed-out / unentitled /
	// 403), so isolated failures between successes are transient by
	// construction. Three in a row is also where the poll has already
	// backed off to unitPollBackoff1 — i.e. read-down has been failing for
	// several minutes, which is when "not syncing" stops being noise.
	unitPollWarnThreshold = 3
)

// StartPoller begins a background read-down-auto loop that periodically
// PullDown's org/team units into the local store. Mirrors the context
// StartPoller backoff pattern (60s base → 300s → 1800s on repeated failure).
// Context cancellation or Stop terminates the loop. Idempotent.
func (s *UnitSyncer) StartPoller(ctx context.Context) {
	s.once.Do(func() {
		go s.pollLoop(ctx)
	})
}

// Stop terminates a running poller loop.
func (s *UnitSyncer) Stop() {
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
}

func (s *UnitSyncer) pollLoop(ctx context.Context) {
	consecutiveErrors := 0
	timer := time.NewTimer(unitPollBaseInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-timer.C:
			_, err := s.PullDown(ctx)
			if err != nil {
				if ctx.Err() != nil {
					// Shutdown, not a failure.
					return
				}
				consecutiveErrors++
			} else {
				consecutiveErrors = 0
			}
			var interval time.Duration
			switch {
			case consecutiveErrors >= unitPollMaxErrors*2:
				interval = unitPollBackoff2
			case consecutiveErrors >= unitPollMaxErrors:
				interval = unitPollBackoff1
			default:
				interval = unitPollBaseInterval
			}
			s.reportPoll(err, consecutiveErrors, time.Now().Add(interval))
			timer.Reset(interval)
		}
	}
}

// reportPoll logs and reports one poll outcome. Split out of pollLoop so
// the threshold is testable without a ticker.
func (s *UnitSyncer) reportPoll(err error, consecutive int, nextRetry time.Time) {
	s.mu.RLock()
	lanes := s.lanes
	s.mu.RUnlock()
	if IsOrgPaused(err) || (err == nil && s.orgPausedNow()) {
		// A staff pause hold (kenaz-fleet #206): not a server error and not
		// "ok" — the lane is off with the honest reason. Transient: the
		// loop keeps its cadence and resumes on its own when the pause
		// lifts (canSync passes again).
		lanes.RecordOff(LaneUnitPoll, ReasonOrgPaused)
		return
	}
	if err == nil {
		s.mu.RLock()
		held := s.tombstonesHeldIdentityUnknown
		s.mu.RUnlock()
		if held > 0 {
			lanes.RecordOff(LaneUnitPoll, ReasonIdentityPending)
			return
		}
		lanes.RecordSuccess(LaneUnitPoll)
		return
	}
	if consecutive < unitPollWarnThreshold {
		logging.L().Debug("fleet.unit.poll.pull_failed",
			"err", err.Error(), "consecutive", consecutive)
		return
	}
	if consecutive == unitPollWarnThreshold {
		// Once per threshold crossing (review F8); the lane carries the
		// running count from here on.
		logging.L().Warn("fleet.unit.poll.pull_failed",
			"err", err.Error(), "consecutive", consecutive)
	} else {
		logging.L().Debug("fleet.unit.poll.pull_failed",
			"err", err.Error(), "consecutive", consecutive)
	}
	reason := "server_error"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrFleetUnreachable) {
		reason = "network"
	}
	lanes.RecordFailure(LaneUnitPoll, reason, err, consecutive, nextRetry)
}

// ── Snapshot / status ────────────────────────────────────────────────────────

// Conflicts returns a snapshot of the unresolved pull-time conflicts.
func (s *UnitSyncer) Conflicts() []UnitConflict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UnitConflict, len(s.conflicts))
	copy(out, s.conflicts)
	return out
}

// ClearConflict drops the surfaced conflict for unitID once it has been
// resolved (merge or enshrine, Phase-3 WP17). A no-op when no conflict is
// recorded for the unit. Safe for concurrent use.
func (s *UnitSyncer) ClearConflict(unitID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.conflicts {
		if c.UnitID == unitID {
			s.conflicts = append(s.conflicts[:i], s.conflicts[i+1:]...)
			return
		}
	}
}

// UnitSyncStatus is the status view for the RPC layer.
type UnitSyncStatus struct {
	Cursor        string    `json:"cursor"`
	LastPullAt    time.Time `json:"last_pull_at"`
	LastPullErr   string    `json:"last_pull_err"`
	LastPushErr   string    `json:"last_push_err"`
	PushCount     int       `json:"push_count"`
	PullCount     int       `json:"pull_count"`
	ConflictCount int       `json:"conflict_count"`
	// SkippedUnknownKinds / SkippedInvalid / PushRefused — WP03 counters
	// (see the UnitSyncer fields).
	SkippedUnknownKinds int `json:"skipped_unknown_kinds"`
	SkippedInvalid      int `json:"skipped_invalid"`
	PushRefused         int `json:"push_refused"`
	PushHeldLoadAlways  int `json:"push_held_load_always"`
	StrippedUnitKeys    int `json:"stripped_unit_keys"`
	// Tombstone outcomes (see applyTombstone): TombstonesApplied — a unit
	// the user does not own was removed; TombstoneDroppedLocalEdits — the
	// subset with un-pushed local edits; TombstonesDemoted — the user's own
	// unit kept as personal; TombstonesHeldIdentityUnknown — tombstones
	// held (unit kept, cursor not advanced, retried next tick) in the most
	// recent pull because the signed-in identity was unknown (a gauge).
	TombstonesApplied             int `json:"tombstones_applied"`
	TombstoneDroppedLocalEdits    int `json:"tombstone_dropped_local_edits"`
	TombstonesDemoted             int `json:"tombstones_demoted"`
	TombstonesHeldIdentityUnknown int `json:"tombstones_held_identity_unknown"`
}

// Status returns a snapshot of the syncer state.
func (s *UnitSyncer) Status() UnitSyncStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return UnitSyncStatus{
		Cursor:        s.cursor,
		LastPullAt:    s.lastPullAt,
		LastPullErr:   s.lastPullErr,
		LastPushErr:   s.lastPushErr,
		PushCount:     s.pushCount,
		PullCount:     s.pullCount,
		ConflictCount: len(s.conflicts),

		SkippedUnknownKinds: s.skippedUnknownKinds,
		SkippedInvalid:      s.skippedInvalid,
		PushRefused:         s.pushRefused,
		PushHeldLoadAlways:  s.pushHeldLoadAlways,
		StrippedUnitKeys:    int(s.mapper.StrippedUnitKeys()),

		TombstonesApplied:             s.tombstonesApplied,
		TombstoneDroppedLocalEdits:    s.tombstoneDroppedLocalEdits,
		TombstonesDemoted:             s.tombstonesDemoted,
		TombstonesHeldIdentityUnknown: s.tombstonesHeldIdentityUnknown,
	}
}

// drain closes an HTTP response body after discarding any remaining bytes.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// ── Cursor persistence ───────────────────────────────────────────────────────

func unitCursorPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "unit_cursor.txt")
}

func loadUnitCursor(dataDir string) (string, error) {
	if dataDir == "" {
		return "", nil
	}
	b, err := os.ReadFile(unitCursorPath(dataDir))
	if err != nil {
		return "", err
	}
	out := string(b)
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out, nil
}

func saveUnitCursor(dataDir, cursor string) error {
	if dataDir == "" {
		return nil
	}
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir unit cursor: %w", err)
	}
	return atomicWriteFile(unitCursorPath(dataDir), cursor+"\n")
}
