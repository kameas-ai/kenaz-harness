// Package fleet — context_graph_sync.go
//
// ContextGraphSync is the client for the fleet context-graph API:
//
//   POST /api/v1/context/push    — push-up team/org shared entries (WP01/WP03)
//   GET  /api/v1/context/pull    — cursor-based delta pull + merge (WP02)
//   POST /api/v1/context/promote — team_shared → org_shared (WP05)
//
// Layer ↔ classification mapping (FR-001 / spec §2a):
//
//	personal  → never synced (local-only)
//	team      → team_shared
//	org       → org_shared
//
// Capability gating (WP04):
//
//	team_shared  → CapSharedTeamGraph
//	org_shared   → CapSharedTeamGraph + org-graph awareness (same cap for v0)
//
// Opaque payload posture (NFR-002): the wire shapes reference json.RawMessage
// for body/metadata so the OSS client carries them without embedding the full
// fleet schema. The structs below match the fleet api_types.go wire types
// exactly (field names, JSON tags, types) — verified against the server.
//
// Fork recipe (NFR-005): deleting this file + the RPC view methods
// (Context_Publish, Context_Promote, Context_SyncStatus) leaves the Contexts
// feature local-only and building.
//
// (fleet-context-graph-sync-01NDFSEX17)
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// ── Layer ↔ classification mapping ────────────────────────────────────────────

// ContextClassification is the fleet-side classification string.
// Only team_shared and org_shared are valid for push; personal lives only
// in the harness local store and must never be pushed to fleet.
type ContextClassification string

const (
	ClassTeamShared ContextClassification = "team_shared"
	ClassOrgShared  ContextClassification = "org_shared"
)

// ClassificationForLayer maps a local context pack Layer to the fleet
// classification. Returns ("", false) for LayerPersonal — personal entries
// are local-only and must never be pushed.
func ClassificationForLayer(l contextpack.Layer) (ContextClassification, bool) {
	switch l {
	case contextpack.LayerTeam:
		return ClassTeamShared, true
	case contextpack.LayerOrg:
		return ClassOrgShared, true
	default:
		return "", false
	}
}

// LayerForClassification maps a fleet classification back to a local pack
// Layer. Returns ("", false) for any unrecognised classification.
func LayerForClassification(c ContextClassification) (contextpack.Layer, bool) {
	switch c {
	case ClassTeamShared:
		return contextpack.LayerTeam, true
	case ClassOrgShared:
		return contextpack.LayerOrg, true
	default:
		return "", false
	}
}

// CapForClassification returns the capability required to push the given
// classification. Both team_shared and org_shared require CapSharedTeamGraph
// for v0; org_shared additionally signals org intent which is gated server-side.
func CapForClassification(c ContextClassification) Capability {
	// Both classifications use the team-graph sharing cap as the harness-side
	// gate in v0. The server enforces the org-graph cap separately.
	return CapSharedTeamGraph
}

// ── Wire shapes (mirroring fleet api_types.go) ────────────────────────────────
//
// These match the fleet ContextNodeInput / ContextEdgeInput shapes exactly.
// json.RawMessage is used for body, metadata so the OSS client is opaque.

// contextNodeInput is the client-side push shape for a single node.
// Mirrors fleet's ContextNodeInput (which api_types.go declares via the
// ContextPushRequest.Nodes slice).
type contextNodeInput struct {
	ID             string                `json:"id"`
	Kind           string                `json:"kind"`
	Title          string                `json:"title"`
	Body           string                `json:"body"`
	Metadata       json.RawMessage       `json:"metadata,omitempty"`
	Classification ContextClassification `json:"classification"`
	TeamID         *string               `json:"team_id,omitempty"`
	Version        int                   `json:"version"`
}

// contextEdgeInput is the client-side push shape for a single edge.
// Mirrors fleet's ContextEdgeInput.
type contextEdgeInput struct {
	ID             string                `json:"id"`
	FromNodeID     string                `json:"from_node_id"`
	ToNodeID       string                `json:"to_node_id"`
	Kind           string                `json:"kind"`
	Classification ContextClassification `json:"classification"`
	TeamID         *string               `json:"team_id,omitempty"`
	Version        int                   `json:"version"`
}

// contextPushRequest is the body for POST /api/v1/context/push.
// Two-phase ordering: nodes must be included before edges referencing them.
type contextPushRequest struct {
	Nodes []contextNodeInput `json:"nodes"`
	Edges []contextEdgeInput `json:"edges"`
}

// ContextPushConflict is one per-node version conflict returned by the server.
type ContextPushConflict struct {
	NodeID        string `json:"node_id"`
	ServerVersion int    `json:"server_version"`
	ClientVersion int    `json:"client_version"`
}

// ContextPushResult is the response from POST /api/v1/context/push.
type ContextPushResult struct {
	AcceptedNodes int                   `json:"accepted_nodes"`
	AcceptedEdges int                   `json:"accepted_edges"`
	Conflicts     []ContextPushConflict `json:"conflicts"`
}

// ContextPulledNode is a single node from GET /api/v1/context/pull.
// Body and Metadata are kept as opaque json.RawMessage (NFR-002).
type ContextPulledNode struct {
	ID             string                `json:"id"`
	OrgID          string                `json:"org_id"`
	TeamID         string                `json:"team_id,omitempty"`
	OwnerUserID    string                `json:"owner_user_id"`
	Kind           string                `json:"kind"`
	Title          string                `json:"title"`
	Body           string                `json:"body"`
	Metadata       json.RawMessage       `json:"metadata,omitempty"`
	Classification ContextClassification `json:"classification"`
	Scope          string                `json:"scope,omitempty"`
	Version        int                   `json:"version"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
	// DeletedAt is non-nil when the entry is a tombstone (FR-103).
	DeletedAt *string `json:"deleted_at,omitempty"`
}

// ContextPulledEdge is a single edge from the pull response.
type ContextPulledEdge struct {
	ID             string                `json:"id"`
	OrgID          string                `json:"org_id"`
	TeamID         string                `json:"team_id,omitempty"`
	FromNodeID     string                `json:"from_node_id"`
	ToNodeID       string                `json:"to_node_id"`
	Kind           string                `json:"kind"`
	Classification ContextClassification `json:"classification"`
	Scope          string                `json:"scope,omitempty"`
	OwnerUserID    string                `json:"owner_user_id"`
	Version        int                   `json:"version"`
	CreatedAt      string                `json:"created_at"`
}

// contextPullResponse is the body from GET /api/v1/context/pull.
type contextPullResponse struct {
	Nodes  []ContextPulledNode `json:"nodes"`
	Edges  []ContextPulledEdge `json:"edges"`
	// Cursor is the updated_at of the last returned node (RFC3339Nano).
	// Empty when no nodes were returned.
	Cursor string `json:"cursor,omitempty"`
}

// contextPromoteRequest is the body for POST /api/v1/context/promote.
type contextPromoteRequest struct {
	NodeID           string `json:"node_id"`
	ToClassification string `json:"to_classification"`
}

// ContextPromoteResult is the response from POST /api/v1/context/promote.
// Node is the updated node; Warnings are advisory lint findings.
type ContextPromoteResult struct {
	Node     ContextPulledNode `json:"node"`
	Warnings []json.RawMessage `json:"warnings"` // opaque lint.Finding slice
}

// ── ContextNodeEntry — harness-local entry representation ─────────────────────

// ContextNodeEntry is the harness-local, in-memory representation of a
// synced context entry. It carries enough information to rebuild a
// contextpack.ContextEntry for the merge layer and to push back to fleet.
type ContextNodeEntry struct {
	// ID is the stable UUID assigned on first publish and persisted locally.
	ID string `json:"id"`
	// Layer is the local layer this entry belongs to.
	Layer contextpack.Layer `json:"layer"`
	// Classification mirrors the fleet classification for the layer.
	Classification ContextClassification `json:"classification"`
	// Kind maps to contextpack.EntryKind (converted to fleet kind string on push).
	Kind string `json:"kind"`
	// Title is the entry's display name; used as the merge key.
	Title string `json:"title"`
	// Body is the entry text.
	Body string `json:"body"`
	// Metadata is opaque JSON attached to the node.
	Metadata json.RawMessage `json:"metadata,omitempty"`
	// TeamID is the fleet team UUID for team_shared entries.
	TeamID *string `json:"team_id,omitempty"`
	// Version is incremented on each edit. LWW resolution on the server.
	Version int `json:"version"`
	// SyncStatus is the local sync state.
	SyncStatus ContextSyncStatus `json:"sync_status"`
	// ServerVersion is the last version the server acknowledged.
	ServerVersion int `json:"server_version,omitempty"`
	// DeletedAt is non-nil when the entry is a server-side tombstone.
	DeletedAt *string `json:"deleted_at,omitempty"`
}

// ContextSyncStatus classifies the local sync state of an entry.
type ContextSyncStatus string

const (
	// SyncStatusLocal means the entry exists only locally (personal or unpublished).
	SyncStatusLocal ContextSyncStatus = "local-only"
	// SyncStatusPending means publish was called but not yet confirmed by the server.
	SyncStatusPending ContextSyncStatus = "pending"
	// SyncStatusSynced means the server accepted the last push.
	SyncStatusSynced ContextSyncStatus = "synced"
)

// ContextEdgeEntry is a harness-local edge between two ContextNodeEntries.
type ContextEdgeEntry struct {
	ID             string                `json:"id"`
	FromNodeID     string                `json:"from_node_id"`
	ToNodeID       string                `json:"to_node_id"`
	Kind           string                `json:"kind"` // references, supersedes, related, derived_from
	Classification ContextClassification `json:"classification"`
	TeamID         *string               `json:"team_id,omitempty"`
	Version        int                   `json:"version"`
}

// ── ContextGraphSyncer ────────────────────────────────────────────────────────

// ContextGraphSyncer manages push-up, pull-down, and promote for the
// fleet context graph. It is safe for concurrent use; all state is
// protected by a mutex.
type ContextGraphSyncer struct {
	client  *Client
	dataDir string
	caps    *CapabilityPoller

	mu      sync.RWMutex
	// pulled is the in-memory cache of team/org entries received from fleet.
	pulled  []ContextNodeEntry
	// pulledEdges is the in-memory cache of edges received from fleet.
	pulledEdges []ContextEdgeEntry
	// cursor is the RFC3339Nano timestamp for the next pull call.
	cursor  string
	// lastPullAt is the wall-clock time of the last successful pull.
	lastPullAt time.Time
	// lastPullErr is the last pull error string (empty on success).
	lastPullErr string
	// lastPushErr is the last push error string (empty on success).
	lastPushErr string
	// lastConflicts holds the per-node version conflicts (server_version vs
	// client_version) from the most recent push that returned any. Surfaced
	// via Status so the Contexts view can prompt the user to reconcile.
	lastConflicts []ContextPushConflict
	// pullCount is the total number of entries received from fleet (cumulative).
	pullCount int

	// pollOnce guards StartPoller so the background loop starts at most once.
	pollOnce sync.Once
	// stopCh signals the background poll loop to exit (closed by Stop).
	stopCh chan struct{}
}

// NewContextGraphSyncer constructs a ContextGraphSyncer. The syncer does
// not start polling automatically — see StartPoller for the background loop.
func NewContextGraphSyncer(client *Client, dataDir string, caps *CapabilityPoller) *ContextGraphSyncer {
	s := &ContextGraphSyncer{
		client:  client,
		dataDir: dataDir,
		caps:    caps,
	}
	// Restore cursor from disk.
	if c, err := loadContextCursor(dataDir); err == nil {
		s.cursor = c
	}
	return s
}

// ── Capability gating helpers ─────────────────────────────────────────────────

// canPushTeam returns nil if the team-graph capability is present.
func (s *ContextGraphSyncer) canPushTeam() error {
	if s.caps == nil {
		return ErrCapabilityNotInTier
	}
	cur := s.caps.Current()
	return cur.Require(CapSharedTeamGraph)
}

// canPushOrg returns nil if the team-graph capability is present.
// (Org-graph cap is gated server-side; client uses the team cap as its gate.)
func (s *ContextGraphSyncer) canPushOrg() error {
	return s.canPushTeam()
}

// canPull returns nil when we are signed in and have at least the team cap.
func (s *ContextGraphSyncer) canPull() error {
	if s.client == nil || s.client.isNop {
		return ErrFleetDisabled
	}
	return s.canPushTeam()
}

// ── Push-up ────────────────────────────────────────────────────────────────────

// PushEntry publishes a single local entry (and optionally its edges) to the
// fleet context graph. The entry must have layer = team or org. Personal
// entries are rejected with ErrPersonalLayerNotSyncable.
//
// Cap gate: team_shared requires CapSharedTeamGraph.
// Idempotent: re-pushing with the same version is a no-op on the server.
//
// FR-001, FR-002, FR-003, FR-004.
func (s *ContextGraphSyncer) PushEntry(ctx context.Context, entry ContextNodeEntry, edges []ContextEdgeEntry) (*ContextPushResult, error) {
	if entry.Layer == contextpack.LayerPersonal {
		return nil, ErrPersonalLayerNotSyncable
	}

	classification, ok := ClassificationForLayer(entry.Layer)
	if !ok {
		return nil, fmt.Errorf("fleet: context push: unsupported layer %q", entry.Layer)
	}

	// Capability gate.
	switch classification {
	case ClassTeamShared:
		if err := s.canPushTeam(); err != nil {
			return nil, err
		}
	case ClassOrgShared:
		if err := s.canPushOrg(); err != nil {
			return nil, err
		}
	}

	node := contextNodeInput{
		ID:             entry.ID,
		Kind:           entry.Kind,
		Title:          entry.Title,
		Body:           entry.Body,
		Metadata:       entry.Metadata,
		Classification: classification,
		TeamID:         entry.TeamID,
		Version:        entry.Version,
	}

	// Build edges slice (two-phase: nodes first, then edges — FR-003).
	edgeInputs := make([]contextEdgeInput, 0, len(edges))
	for _, e := range edges {
		ei := contextEdgeInput{
			ID:             e.ID,
			FromNodeID:     e.FromNodeID,
			ToNodeID:       e.ToNodeID,
			Kind:           e.Kind,
			Classification: e.Classification,
			TeamID:         e.TeamID,
			Version:        e.Version,
		}
		edgeInputs = append(edgeInputs, ei)
	}

	req := contextPushRequest{
		Nodes: []contextNodeInput{node},
		Edges: edgeInputs,
	}

	resp, err := s.client.PostJSON(ctx, "/api/v1/context/push", req)
	if err != nil {
		s.mu.Lock()
		s.lastPushErr = err.Error()
		s.mu.Unlock()
		return nil, fmt.Errorf("fleet: context push: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusForbidden {
		s.mu.Lock()
		s.lastPushErr = "capability_not_in_tier"
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: server refused push (likely org tier)", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK {
		s.mu.Lock()
		s.lastPushErr = fmt.Sprintf("push status %d", resp.StatusCode)
		s.mu.Unlock()
		return nil, fmt.Errorf("fleet: context push status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fleet: context push read body: %w", err)
	}

	var result ContextPushResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("fleet: context push parse response: %w", err)
	}

	s.mu.Lock()
	s.lastPushErr = ""
	// Surface server/client version conflicts so the Contexts view can prompt
	// the user to reconcile (FR: surface server_version vs client_version).
	if len(result.Conflicts) > 0 {
		s.lastConflicts = append([]ContextPushConflict(nil), result.Conflicts...)
	} else {
		s.lastConflicts = nil
	}
	s.mu.Unlock()

	return &result, nil
}

// ── Pull-down ─────────────────────────────────────────────────────────────────

// PullDelta fetches shared-graph deltas since the stored cursor, merges them
// into the in-memory pulled cache, persists the cursor, and returns the
// number of nodes received (including tombstones).
//
// First sync (cursor == ""): backfills the full entitled shared graph.
// Subsequent syncs: cursor-based delta (FR-101, FR-103, FR-104).
func (s *ContextGraphSyncer) PullDelta(ctx context.Context) (int, error) {
	if err := s.canPull(); err != nil {
		if errors.Is(err, ErrFleetDisabled) || errors.Is(err, ErrNotSignedIn) {
			return 0, nil // offline / signed-out → local-only (FR-104)
		}
		return 0, nil // capability absent → local-only, no error
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
		return 0, fmt.Errorf("fleet: context pull: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusForbidden {
		// Unentitled → local-only, no error surfaced (FR-104).
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		e := fmt.Errorf("fleet: context pull status %d", resp.StatusCode)
		s.mu.Lock()
		s.lastPullErr = e.Error()
		s.mu.Unlock()
		return 0, e
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("fleet: context pull read: %w", err)
	}

	var pullResp contextPullResponse
	if err := json.Unmarshal(rawBody, &pullResp); err != nil {
		return 0, fmt.Errorf("fleet: context pull parse: %w", err)
	}

	// Merge received nodes into the in-memory cache.
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, n := range pullResp.Nodes {
		layer, ok := LayerForClassification(n.Classification)
		if !ok {
			continue // skip unknown classifications
		}
		entry := ContextNodeEntry{
			ID:             n.ID,
			Layer:          layer,
			Classification: n.Classification,
			Kind:           n.Kind,
			Title:          n.Title,
			Body:           n.Body,
			Metadata:       n.Metadata,
			TeamID:         teamIDPtr(n.TeamID),
			Version:        n.Version,
			SyncStatus:     SyncStatusSynced,
			ServerVersion:  n.Version,
			DeletedAt:      n.DeletedAt,
		}
		s.upsertPulledLocked(entry)
	}

	// Merge edges.
	for _, e := range pullResp.Edges {
		edge := ContextEdgeEntry{
			ID:             e.ID,
			FromNodeID:     e.FromNodeID,
			ToNodeID:       e.ToNodeID,
			Kind:           e.Kind,
			Classification: e.Classification,
			TeamID:         teamIDPtr(e.TeamID),
			Version:        e.Version,
		}
		s.upsertPulledEdgeLocked(edge)
	}

	// Advance cursor.
	if pullResp.Cursor != "" {
		s.cursor = pullResp.Cursor
		if s.dataDir != "" {
			if saveErr := saveContextCursor(s.dataDir, s.cursor); saveErr != nil {
				log.Printf("fleet: context cursor save: %v", saveErr)
			}
		}
	}

	n := len(pullResp.Nodes)
	s.pullCount += n
	s.lastPullAt = time.Now()
	s.lastPullErr = ""

	return n, nil
}

// upsertPulledLocked inserts or replaces an entry in s.pulled.
// Must be called under s.mu.Lock().
func (s *ContextGraphSyncer) upsertPulledLocked(entry ContextNodeEntry) {
	for i, existing := range s.pulled {
		if existing.ID == entry.ID {
			s.pulled[i] = entry
			return
		}
	}
	s.pulled = append(s.pulled, entry)
}

// upsertPulledEdgeLocked inserts or replaces an edge in s.pulledEdges.
// Must be called under s.mu.Lock().
func (s *ContextGraphSyncer) upsertPulledEdgeLocked(edge ContextEdgeEntry) {
	for i, existing := range s.pulledEdges {
		if existing.ID == edge.ID {
			s.pulledEdges[i] = edge
			return
		}
	}
	s.pulledEdges = append(s.pulledEdges, edge)
}

// teamIDPtr returns a *string for a non-empty team_id string.
func teamIDPtr(s string) *string {
	if s == "" {
		return nil
	}
	cp := s
	return &cp
}

// ── Promote ────────────────────────────────────────────────────────────────────

// Promote elevates a team_shared entry to org_shared.
// Requires CapSharedTeamGraph (server enforces org-graph separately).
// FR-201, FR-202.
func (s *ContextGraphSyncer) Promote(ctx context.Context, nodeID string) (*ContextPromoteResult, error) {
	if err := s.canPushOrg(); err != nil {
		return nil, err
	}

	req := contextPromoteRequest{
		NodeID:           nodeID,
		ToClassification: string(ClassOrgShared),
	}

	resp, err := s.client.PostJSON(ctx, "/api/v1/context/promote", req)
	if err != nil {
		return nil, fmt.Errorf("fleet: context promote: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: server refused promote", ErrCapabilityNotInTier)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet: context promote status %d", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fleet: context promote read: %w", err)
	}

	var result ContextPromoteResult
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return nil, fmt.Errorf("fleet: context promote parse: %w", err)
	}

	// Update local cache: reclassify to org.
	s.mu.Lock()
	for i, e := range s.pulled {
		if e.ID == nodeID {
			s.pulled[i].Classification = ClassOrgShared
			s.pulled[i].Layer = contextpack.LayerOrg
			s.pulled[i].SyncStatus = SyncStatusSynced
		}
	}
	s.mu.Unlock()

	return &result, nil
}

// ── Search / Export thin clients (gap #5) ──────────────────────────────────────
//
// Thin clients over POST /api/v1/context/search and GET /api/v1/context/export.
// Wire shapes mirror kenaz-fleet service/api_types.go. Both gate on the same
// team-graph capability + sign-in as pull (canPull) so they no-op cleanly when
// fleet is disabled / signed-out / unentitled.

// ContextSearchHit is one search result (mirrors fleet ContextSearchHit).
// Node reuses the pulled-node shape; Snippet is an excerpt with the match
// wrapped in **bold markers**; Rank is the v0 match-count stand-in for the
// vector similarity score.
type ContextSearchHit struct {
	Node    ContextPulledNode `json:"node"`
	Snippet string            `json:"snippet"`
	Rank    float64           `json:"rank"`
}

// contextSearchRequest mirrors fleet ContextSearchRequest.
type contextSearchRequest struct {
	Query  string `json:"query"`
	TeamID string `json:"team_id,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// contextSearchResponse mirrors fleet ContextSearchResponse.
type contextSearchResponse struct {
	Results []ContextSearchHit `json:"results"`
}

// SearchContext runs a server-side search over the caller's visible context
// graph (title+body ILIKE in v0). teamID is optional; limit <= 0 lets the
// server pick a default. Returns nil + no error when fleet is disabled /
// unentitled (offline-graceful, matching the pull posture).
func (s *ContextGraphSyncer) SearchContext(ctx context.Context, query, teamID string, limit int) ([]ContextSearchHit, error) {
	if err := s.canPull(); err != nil {
		return nil, nil // offline / signed-out / unentitled → empty, no error
	}
	req := contextSearchRequest{Query: query, TeamID: teamID, Limit: limit}
	resp, err := s.client.PostJSON(ctx, "/api/v1/context/search", req)
	if err != nil {
		return nil, fmt.Errorf("fleet: context search: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusForbidden {
		return nil, nil // unentitled → empty
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: context search status %d: %s", resp.StatusCode, body)
	}
	var out contextSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: context search decode: %w", err)
	}
	return out.Results, nil
}

// ContextExport is the result of an export stream: the raw bytes plus the
// server's content type (application/x-ndjson or application/gzip).
type ContextExport struct {
	ContentType string
	Data        []byte
}

// ExportContext streams the caller's visible context graph. format is "jsonl"
// (default) or "tarball"; teamID optionally narrows to a single team. The
// whole stream is buffered into memory (the server caps synchronous exports at
// 100k nodes). Returns nil + no error when fleet is disabled / unentitled.
func (s *ContextGraphSyncer) ExportContext(ctx context.Context, teamID, format string) (*ContextExport, error) {
	if err := s.canPull(); err != nil {
		return nil, nil
	}
	q := url.Values{}
	if teamID != "" {
		q.Set("team_id", teamID)
	}
	if format != "" {
		q.Set("format", format)
	}
	path := "/api/v1/context/export"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := s.client.Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("fleet: context export: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusForbidden {
		return nil, nil
	}
	if resp.StatusCode == http.StatusRequestEntityTooLarge {
		return nil, fmt.Errorf("fleet: context export too large (use a team-scoped export)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: context export status %d: %s", resp.StatusCode, body)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fleet: context export read: %w", err)
	}
	return &ContextExport{
		ContentType: resp.Header.Get("Content-Type"),
		Data:        data,
	}, nil
}

// ── Snapshot / status helpers ─────────────────────────────────────────────────

// PulledEntries returns a snapshot of all pulled entries (team + org layers).
// Does NOT include tombstoned (soft-deleted) entries.
// The caller may use this to build context packs for the merge layer.
func (s *ContextGraphSyncer) PulledEntries() []ContextNodeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ContextNodeEntry, 0, len(s.pulled))
	for _, e := range s.pulled {
		if e.DeletedAt != nil {
			continue // tombstoned — do not surface
		}
		out = append(out, e)
	}
	return out
}

// PulledEdges returns a snapshot of all pulled edges. Edges referencing
// tombstoned nodes are NOT filtered here — callers may filter by node liveness.
func (s *ContextGraphSyncer) PulledEdges() []ContextEdgeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ContextEdgeEntry, len(s.pulledEdges))
	copy(out, s.pulledEdges)
	return out
}

// ContextSyncStatusSnapshot is the view returned to the RPC layer.
type ContextSyncStatusSnapshot struct {
	// Cursor is the RFC3339Nano timestamp for the next pull delta.
	Cursor string `json:"cursor"`
	// LastPullAt is the wall-clock time of the last successful pull, or zero.
	LastPullAt time.Time `json:"last_pull_at"`
	// LastPullErr is the last pull error (empty on success).
	LastPullErr string `json:"last_pull_err"`
	// LastPushErr is the last push error (empty on success).
	LastPushErr string `json:"last_push_err"`
	// PullCount is the total number of entries ever received from fleet.
	PullCount int `json:"pull_count"`
	// TeamCapEnabled is true when the team-graph capability is active.
	TeamCapEnabled bool `json:"team_cap_enabled"`
	// Conflicts holds the per-node server/client version conflicts from the
	// most recent push (empty when the last push had none). Lets the Contexts
	// view prompt the user to reconcile.
	Conflicts []ContextPushConflict `json:"conflicts,omitempty"`
}

// Status returns a snapshot of the syncer state.
func (s *ContextGraphSyncer) Status() ContextSyncStatusSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	teamCap := false
	if s.caps != nil {
		cur := s.caps.Current()
		teamCap = cur.Has(CapSharedTeamGraph)
	}
	var conflicts []ContextPushConflict
	if len(s.lastConflicts) > 0 {
		conflicts = append([]ContextPushConflict(nil), s.lastConflicts...)
	}
	return ContextSyncStatusSnapshot{
		Cursor:         s.cursor,
		LastPullAt:     s.lastPullAt,
		LastPullErr:    s.lastPullErr,
		LastPushErr:    s.lastPushErr,
		PullCount:      s.pullCount,
		TeamCapEnabled: teamCap,
		Conflicts:      conflicts,
	}
}

// ── Background pull poller (harness-fleet-sync-activation-01NSYNC01 gap #2) ─────

const (
	contextPullBaseInterval = 60 * time.Second
	contextPullBackoff1     = 300 * time.Second  // 5 min
	contextPullBackoff2     = 1800 * time.Second // 30 min
	contextPullMaxConsecErr = 2
)

// StartPoller launches a background loop that periodically PullDelta()s the
// f->h context-graph delta (team/org read layers) and merges it into the local
// pulled cache, which the Contexts view surfaces via PulledEntries /
// PulledEdges. Personal entries are never pulled (they stay device-local).
//
// The loop self-gates: PullDelta returns (0, nil) when the user is signed out,
// offline, or lacks the team-graph capability, so the poller is a cheap no-op
// in those states. On transient pull errors it backs off 60s → 300s → 1800s,
// mirroring the settings Syncer. Context cancellation (or Stop) ends the loop.
//
// Idempotent: only the first call starts the goroutine.
func (s *ContextGraphSyncer) StartPoller(ctx context.Context) {
	s.pollOnce.Do(func() {
		if s.stopCh == nil {
			s.stopCh = make(chan struct{})
		}
		go s.pollLoop(ctx)
	})
}

// Stop signals the background poll loop to exit. Safe to call once.
func (s *ContextGraphSyncer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopCh != nil {
		select {
		case <-s.stopCh:
			// already closed
		default:
			close(s.stopCh)
		}
	}
}

func (s *ContextGraphSyncer) pollLoop(ctx context.Context) {
	consecutiveErrors := 0
	timer := time.NewTimer(contextPullBaseInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-timer.C:
			if _, err := s.PullDelta(ctx); err != nil {
				consecutiveErrors++
				log.Printf("fleet: context pull poll failed (consecutive=%d): %v", consecutiveErrors, err)
			} else {
				consecutiveErrors = 0
			}
			var interval time.Duration
			switch {
			case consecutiveErrors >= contextPullMaxConsecErr*2:
				interval = contextPullBackoff2
			case consecutiveErrors >= contextPullMaxConsecErr:
				interval = contextPullBackoff1
			default:
				interval = contextPullBaseInterval
			}
			timer.Reset(interval)
		}
	}
}

// ResetCursor drops the stored cursor (forcing a full backfill on next pull).
// Used in tests.
func (s *ContextGraphSyncer) ResetCursor() {
	s.mu.Lock()
	s.cursor = ""
	s.pulled = nil
	s.pulledEdges = nil
	s.pullCount = 0
	s.mu.Unlock()
	if s.dataDir != "" {
		_ = os.Remove(contextCursorPath(s.dataDir))
	}
}

// ── Sentinel errors ────────────────────────────────────────────────────────────

// ErrPersonalLayerNotSyncable is returned when a caller tries to push a
// personal-layer entry to fleet. Personal entries are device-local.
var ErrPersonalLayerNotSyncable = errors.New("fleet: personal layer entries are local-only and cannot be pushed to fleet")

// ── Cursor persistence ─────────────────────────────────────────────────────────

func contextCursorPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "context_cursor.txt")
}

func loadContextCursor(dataDir string) (string, error) {
	if dataDir == "" {
		return "", nil
	}
	b, err := os.ReadFile(contextCursorPath(dataDir))
	if err != nil {
		return "", err
	}
	s := string(b)
	// Trim trailing newlines.
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s, nil
}

func saveContextCursor(dataDir, cursor string) error {
	if dataDir == "" {
		return nil
	}
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir context cursor: %w", err)
	}
	return atomicWriteFile(contextCursorPath(dataDir), cursor+"\n")
}
