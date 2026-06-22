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

	mu          sync.RWMutex
	cursor      string
	lastPullAt  time.Time
	lastPullErr string
	lastPushErr string
	pushCount   int
	pullCount   int
	conflicts   []UnitConflict

	stopCh chan struct{}
	once   sync.Once
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
		stopCh:  make(chan struct{}),
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
	if len(dirty) == 0 {
		return 0, nil
	}

	nodes := make([]contextNodeInput, 0, len(dirty))
	pushed := make([]units.Unit, 0, len(dirty))
	seenEdge := map[string]bool{}
	edges := make([]contextEdgeInput, 0)

	for _, u := range dirty {
		node, ok, err := s.mapper.MapUnitToNode(u)
		if err != nil {
			return 0, fmt.Errorf("fleet: unit push: map %s: %w", u.ID, err)
		}
		if !ok {
			continue // personal — defensively skipped (ListDirty already filters)
		}
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
			seenEdge[e.ID] = true
			edges = append(edges, wire)
		}
	}
	if len(nodes) == 0 {
		return 0, nil
	}

	req := contextPushRequest{Nodes: nodes, Edges: edges}
	resp, err := s.client.PostJSON(ctx, "/api/v1/context/push", req)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit push: %w", err)
	}
	defer drain(resp)

	if resp.StatusCode == http.StatusForbidden {
		return 0, fmt.Errorf("%w: server refused unit push", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fleet: unit push status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("fleet: unit push read: %w", err)
	}
	var result ContextPushResult
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("fleet: unit push parse: %w", err)
	}

	// Index conflicts so we don't advance the sidecar for conflicted nodes.
	conflicted := map[string]bool{}
	for _, c := range result.Conflicts {
		conflicted[c.NodeID] = true
	}

	classStr := ""
	if cls, ok := ClassificationForUnit(class); ok {
		classStr = string(cls)
	}
	now := time.Now().UTC()
	for _, u := range pushed {
		if conflicted[u.ID] {
			continue // server rejected this version — leave sidecar untouched
		}
		// After a successful push-ack both baselines advance to the pushed
		// unit's local version. The server echoes back the same version we
		// sent (the push is our version), so SyncedServerVersion = SyncedLocalVersion
		// = u.Version immediately after a push.
		if _, err := s.store.UpsertSyncState(ctx, units.SyncState{
			UnitID:              u.ID,
			NodeID:              u.ID, // harness reuses unit id as node id on push
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

	urlPath := "/api/v1/context/pull"
	if cursor != "" {
		urlPath += "?since=" + cursor
	}

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
	for _, n := range pullResp.Nodes {
		ok, err := s.applyPulledNode(ctx, n)
		if err != nil {
			return applied, err
		}
		if ok {
			applied++
		}
	}

	s.mu.Lock()
	if pullResp.Cursor != "" {
		s.cursor = pullResp.Cursor
		if s.dataDir != "" {
			if saveErr := saveUnitCursor(s.dataDir, s.cursor); saveErr != nil {
				logging.L().Warn("fleet.unit.cursor_save_failed", "err", saveErr.Error())
			}
		}
	}
	s.pullCount += len(pullResp.Nodes)
	s.lastPullAt = time.Now().UTC()
	s.lastPullErr = ""
	s.mu.Unlock()

	return applied, nil
}

// applyPulledNode maps one pulled node into the local store and returns
// whether a write was applied. It resolves the local unit via the sidecar
// node-id index (no re-discovery). Conflict rules (3-way baseline):
//
//   - Tombstones (DeletedAt != nil) are skipped (Phase-3 handles deletes).
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
		return false, nil // tombstone — defer to Phase-3
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
		// First sight of this server node → create a fresh local read layer.
		created, cerr := s.store.Create(ctx, mapped)
		if cerr != nil {
			return false, fmt.Errorf("fleet: unit pull: create %s: %w", n.ID, cerr)
		}
		// Record BOTH baselines so the first subsequent delta compares in the
		// correct counter spaces. SyncedServerVersion = server's version at
		// creation; SyncedLocalVersion = the local unit's Version after Create
		// (always 0 since Create forces Version=0, but using created.Version
		// is self-documenting and safe against future Create changes).
		if _, serr := s.store.UpsertSyncState(ctx, units.SyncState{
			UnitID:              created.ID,
			NodeID:              n.ID,
			SyncedServerVersion: n.Version,
			SyncedLocalVersion:  created.Version,
			Classification:      string(n.Classification),
			LastSynced:          time.Now().UTC(),
		}); serr != nil {
			return false, fmt.Errorf("fleet: unit pull: sidecar create %s: %w", n.ID, serr)
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
	// Apply the server body and advance BOTH baselines.
	if _, err := s.store.Update(ctx, local.ID, mapped.Body, mapped.Metadata); err != nil {
		return false, fmt.Errorf("fleet: unit pull: update %s: %w", local.ID, err)
	}
	updated, err := s.store.Get(ctx, local.ID)
	if err != nil {
		return false, err
	}
	if _, err := s.store.UpsertSyncState(ctx, units.SyncState{
		UnitID:              local.ID,
		NodeID:              n.ID,
		SyncedServerVersion: n.Version,       // server counter at this pull
		SyncedLocalVersion:  updated.Version, // local counter after the fast-forward apply
		Classification:      string(n.Classification),
		LastSynced:          time.Now().UTC(),
	}); err != nil {
		return false, fmt.Errorf("fleet: unit pull: sidecar update %s: %w", local.ID, err)
	}
	return true, nil
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
			if _, err := s.PullDown(ctx); err != nil {
				consecutiveErrors++
				logging.L().Warn("fleet.unit.poll.pull_failed",
					"err", err.Error(),
					"consecutive", consecutiveErrors,
				)
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
			timer.Reset(interval)
		}
	}
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

// UnitSyncStatus is the status view for the RPC layer.
type UnitSyncStatus struct {
	Cursor        string    `json:"cursor"`
	LastPullAt    time.Time `json:"last_pull_at"`
	LastPullErr   string    `json:"last_pull_err"`
	LastPushErr   string    `json:"last_push_err"`
	PushCount     int       `json:"push_count"`
	PullCount     int       `json:"pull_count"`
	ConflictCount int       `json:"conflict_count"`
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
