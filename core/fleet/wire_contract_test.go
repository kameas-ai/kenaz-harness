package fleet

// wire_contract_test.go — the harness↔fleet push contract test fleet asked
// for (owner ruling 2026-10-06, WP01).
//
// Every harness fake before this one decoded the harness's push body with the
// HARNESS's own structs, so a body fleet refuses (btoa / ULID / "ctxb-" ids,
// fields fleet does not know) kept CI green — audit §0-A. This file mirrors
// fleet's REQUEST structs verbatim (each field cites the kenaz-fleet
// file:line it mirrors, at fleet main 42012d5) and decodes REAL harness push
// bodies captured off the wire with them, the way fleet's handlers do:
//
//   - uuid.UUID fields decode only from UUID strings (fleet
//     handlers_context.go:199-205 returns 400 invalid_request otherwise);
//   - unit_node_id goes through uuid.Parse (handlers_unit_merge.go:117-120);
//   - classification must be team_shared | org_shared
//     (handlers_context.go:216-245).
//
// Decoding uses DisallowUnknownFields — STRICTER than fleet (which ignores
// unknown fields): a harness field fleet silently drops is a contract bug
// too (the merge request's title/body went nowhere for exactly that reason).
//
// If fleet changes a request struct, update the mirror here IN THE SAME
// change that updates the harness client.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// fleetContextNodeInput mirrors kenaz-fleet service/lookups_context.go:93-112
// (ContextNodeInput).
type fleetContextNodeInput struct {
	ID             uuid.UUID      `json:"id"`                    // lookups_context.go:94
	Kind           string         `json:"kind"`                  // :95
	Title          string         `json:"title"`                 // :96
	Body           string         `json:"body"`                  // :97
	Metadata       map[string]any `json:"metadata"`              // :98
	Classification string         `json:"classification"`        // :99
	TeamID         *uuid.UUID     `json:"team_id,omitempty"`     // :100
	Version        int            `json:"version"`               // :101
	DeletedAt      *time.Time     `json:"deleted_at,omitempty"`  // :102
	UnitKind       string         `json:"unit_kind,omitempty"`   // :108
	UnitScope      string         `json:"unit_scope,omitempty"`  // :109
	ScopeID        string         `json:"scope_id,omitempty"`    // :110
	LoadPolicy     string         `json:"load_policy,omitempty"` // :111
}

// fleetContextEdgeInput mirrors service/lookups_context.go:117-128
// (ContextEdgeInput).
type fleetContextEdgeInput struct {
	ID             uuid.UUID  `json:"id"`                  // lookups_context.go:118
	FromNodeID     uuid.UUID  `json:"from_node_id"`        // :119
	ToNodeID       uuid.UUID  `json:"to_node_id"`          // :120
	Kind           string     `json:"kind"`                // :121
	Classification string     `json:"classification"`      // :122
	TeamID         *uuid.UUID `json:"team_id,omitempty"`   // :123
	Version        int        `json:"version"`             // :124
	UnitKind       string     `json:"unit_kind,omitempty"` // :127
}

// fleetContextPushRequest mirrors service/api_types.go:520-523
// (ContextPushRequest).
type fleetContextPushRequest struct {
	Nodes []fleetContextNodeInput `json:"nodes"` // api_types.go:521
	Edges []fleetContextEdgeInput `json:"edges"` // api_types.go:522
}

// fleetMergeRequestCreateRequest mirrors service/api_types.go:633-639
// (MergeRequestCreateRequest). unit_node_id is a string on the struct; the
// handler uuid.Parse's it (handlers_unit_merge.go:117-120).
type fleetMergeRequestCreateRequest struct {
	UnitNodeID       string         `json:"unit_node_id"`                // api_types.go:634
	ToClassification string         `json:"to_classification"`           // :635
	ProposedTitle    string         `json:"proposed_title,omitempty"`    // :636
	ProposedBody     string         `json:"proposed_body,omitempty"`     // :637
	ProposedMetadata map[string]any `json:"proposed_metadata,omitempty"` // :638
}

// fleetPushClassifications is fleet's accepted set
// (handlers_context.go:114-123 classificationCapability).
var fleetPushClassifications = map[string]bool{"team_shared": true, "org_shared": true}

// decodeFleetPush decodes a captured harness push body exactly as fleet's
// handleContextPush would (plus DisallowUnknownFields) and applies fleet's
// per-node classification + team_id rules. Returns the decoded request.
func decodeFleetPush(t *testing.T, raw []byte) fleetContextPushRequest {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req fleetContextPushRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fleet would 400 invalid_request on this harness push body: %v\nbody: %s", err, raw)
	}
	for _, n := range req.Nodes {
		if n.ID == uuid.Nil {
			t.Errorf("node id is the nil UUID")
		}
		if msg := fleetValidateNodeKinds(n); msg != "" {
			t.Errorf("node %s: fleet knowledge boundary would 400: %s", n.ID, msg)
		}
		if !fleetPushClassifications[n.Classification] {
			t.Errorf("node %s classification %q ∉ {team_shared, org_shared}", n.ID, n.Classification)
		}
		// handlers_context.go: team_shared needs a team_id; org_shared must not carry one.
		if n.Classification == "org_shared" && n.TeamID != nil {
			t.Errorf("org_shared node %s carries a team_id", n.ID)
		}
	}
	for _, e := range req.Edges {
		if !fleetPushClassifications[e.Classification] {
			t.Errorf("edge %s classification %q ∉ {team_shared, org_shared}", e.ID, e.Classification)
		}
	}
	return req
}

// fleetValidateNodeKinds re-implements kenaz-fleet's knowledge-boundary
// validation (feat/context-kind-boundary 6980280, service/context_kinds.go
// validateContextNodeKinds + loadPolicyAlwaysDenied for a NON-admin caller).
// Returns "" when fleet would accept the node.
func fleetValidateNodeKinds(n fleetContextNodeInput) string {
	pushable := map[string]bool{"doc": true, "snippet": true, "tool_output": true, "root": true}
	knowledge := map[string]bool{
		"glossary": true, "explanation": true, "guidance": true, "procedure": true,
		"root": true, "doc": true, "snippet": true, "tool_output": true,
		"project": true, "person": true, "system": true, "glossary_term": true,
		"work_item": true, "document": true,
		"fact": true, "preference": true, "entity": true, "directive": true, "doc-ref": true,
	}
	reserved := map[string]bool{"skill": true, "workflow": true, "pack": true, "bundle": true, "agent_pack": true, "mcp": true, "recipe": true}
	if !pushable[n.UnitKind] {
		return "invalid_unit_kind " + n.UnitKind
	}
	kind := strings.ToLower(strings.TrimSpace(n.Kind))
	if reserved[kind] {
		return "kind_not_knowledge " + n.Kind
	}
	if n.Kind != kind || !knowledge[kind] {
		return "invalid_kind " + n.Kind
	}
	if u, ok := n.Metadata["_unit"].(map[string]any); ok {
		if mk, _ := u["unit_kind"].(string); mk != "" && mk != n.UnitKind {
			return "unit_kind_mismatch"
		}
		if ms, _ := u["unit_scope"].(string); ms != "" && ms != n.UnitScope {
			return "unit_scope_mismatch"
		}
		if lp, _ := u["load_policy"].(string); strings.EqualFold(strings.TrimSpace(lp), "always") {
			return "load_policy_requires_admin (_unit)"
		}
	}
	if n.LoadPolicy == "always" {
		return "load_policy_requires_admin"
	}
	for _, key := range []string{"type", "kind", "category"} {
		if v, _ := n.Metadata[key].(string); reserved[strings.ToLower(strings.TrimSpace(v))] {
			return "metadata " + key + " is a capability word"
		}
	}
	return ""
}

// wireCapture is a fake fleet that records raw request bodies by path and
// answers with fleet's real response shapes.
type wireCapture struct {
	mu     sync.Mutex
	bodies map[string][][]byte
}

func (c *wireCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	if c.bodies == nil {
		c.bodies = map[string][][]byte{}
	}
	c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], raw)
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/context/push":
		var req struct {
			Nodes []json.RawMessage `json:"nodes"`
			Edges []json.RawMessage `json:"edges"`
		}
		_ = json.Unmarshal(raw, &req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted_nodes": len(req.Nodes), "accepted_edges": len(req.Edges), "conflicts": []any{},
		})
	case "/api/v1/context/merge-requests":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"merge_request":{"id":"7e3d2c1b-0f9e-4a6c-9b8d-12a3b4c5d6e7","status":"open"}}`)
	default:
		http.NotFound(w, r)
	}
}

func (c *wireCapture) snapshot(path string) [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.bodies[path]))
	copy(out, c.bodies[path])
	return out
}

func newWireCaptureClient(t *testing.T) (*wireCapture, *Client) {
	t.Helper()
	cap := &wireCapture{}
	srv := httptest.NewServer(cap)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	return cap, makeTestClient(t, srv.URL)
}

// TestWireContract_CuratedPublish_DecodesWithFleetTypes — Knowledge › Curated
// "Share": the local id is a library path; the body on the wire must decode
// with fleet's ContextPushRequest, and republishing the same path must reuse
// the same node id (update, not a new node).
func TestWireContract_CuratedPublish_DecodesWithFleetTypes(t *testing.T) {
	cap, client := newWireCaptureClient(t)
	s := NewContextGraphSyncer(client, t.TempDir(), makeCapPollerWithTeamCap(t))
	ctx := context.Background()

	entry := ContextNodeEntry{
		ID: "guidance/style.md", Layer: contextpack.LayerOrg, Kind: "guidance",
		Title: "style", Body: "Use tabs.", Version: 1,
	}
	edge := ContextEdgeEntry{
		ID: "guidance/style.md->guidance/tone.md", FromNodeID: "guidance/style.md", ToNodeID: "guidance/tone.md",
		Kind: "references", Classification: ClassOrgShared, Version: 1,
	}
	if _, err := s.PushEntry(ctx, entry, []ContextEdgeEntry{edge}); err != nil {
		t.Fatalf("PushEntry: %v", err)
	}
	entry.Version = 2
	if _, err := s.PushEntry(ctx, entry, nil); err != nil {
		t.Fatalf("PushEntry (republish): %v", err)
	}

	bodies := cap.snapshot("/api/v1/context/push")
	if len(bodies) != 2 {
		t.Fatalf("push bodies = %d, want 2", len(bodies))
	}
	first := decodeFleetPush(t, bodies[0])
	second := decodeFleetPush(t, bodies[1])
	if first.Nodes[0].ID != second.Nodes[0].ID {
		t.Errorf("republish minted a new node id %s (first %s) — must update the same node", second.Nodes[0].ID, first.Nodes[0].ID)
	}
	if first.Edges[0].FromNodeID != first.Nodes[0].ID {
		t.Errorf("edge from %s does not point at the pushed node %s", first.Edges[0].FromNodeID, first.Nodes[0].ID)
	}
}

// TestWireContract_UnitPush_DecodesWithFleetTypes — unit push (team + org
// classes, with a lineage edge): ULIDs stay local, the wire carries UUIDs,
// and the edge endpoints are the wire ids of the pushed units.
func TestWireContract_UnitPush_DecodesWithFleetTypes(t *testing.T) {
	cap, client := newWireCaptureClient(t)
	m := newUnitTestManager()
	ctx := context.Background()
	teamA := seedTeamUnit(t, m, "a", "alpha")
	teamB := seedTeamUnit(t, m, "b", "beta")
	if _, err := m.AddEdge(ctx, units.Edge{FromID: teamA.ID, ToID: teamB.ID, Kind: units.EdgeDerivedFrom}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := m.Create(ctx, units.Unit{
		Kind: units.KindSnippet, Scope: units.ScopeGlobal, Classification: units.ClassOrg,
		LoadPolicy: units.LoadOnDemand, Title: "org", Body: "org body",
	}); err != nil {
		t.Fatalf("Create org: %v", err)
	}
	// A real fleet team id (fleet decodes team_id as *uuid.UUID).
	s := NewUnitSyncer(client, m, NewUnitMapper("5e4f3d2c-1b0a-4998-8877-665544332211"), makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty: %v", err)
	}

	bodies := cap.snapshot("/api/v1/context/push")
	if len(bodies) != 2 {
		t.Fatalf("push bodies = %d, want 2 (one per classification)", len(bodies))
	}
	wireIDs := map[uuid.UUID]bool{}
	var edges []fleetContextEdgeInput
	for _, raw := range bodies {
		req := decodeFleetPush(t, raw)
		for _, n := range req.Nodes {
			wireIDs[n.ID] = true
		}
		edges = append(edges, req.Edges...)
	}
	for _, local := range []string{teamA.ID, teamB.ID} {
		if wireIDs[uuid.MustParse(s.WireNodeID(ctx, local))] != true {
			t.Errorf("unit %s not pushed under its wire id", local)
		}
	}
	if len(edges) == 0 {
		t.Fatal("lineage edge was not pushed")
	}
	for _, e := range edges {
		if !wireIDs[e.FromNodeID] || !wireIDs[e.ToNodeID] {
			t.Errorf("edge %s endpoints %s→%s are not pushed node ids", e.ID, e.FromNodeID, e.ToNodeID)
		}
	}
}

// TestWireContract_MergeRequest_DecodesWithFleetTypes — unit_node_id is the
// unit's wire UUID and every field is one fleet reads.
func TestWireContract_MergeRequest_DecodesWithFleetTypes(t *testing.T) {
	cap, client := newWireCaptureClient(t)
	m := newUnitTestManager()
	ctx := context.Background()
	src := seedTeamUnit(t, m, "promote me", "body")
	s := NewUnitSyncer(client, m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.CreateMergeRequestForPromote(ctx, src, units.ClassOrg, "", ""); err != nil {
		t.Fatalf("CreateMergeRequestForPromote: %v", err)
	}
	bodies := cap.snapshot("/api/v1/context/merge-requests")
	if len(bodies) != 1 {
		t.Fatalf("merge-request bodies = %d, want 1", len(bodies))
	}
	dec := json.NewDecoder(bytes.NewReader(bodies[0]))
	dec.DisallowUnknownFields()
	var req fleetMergeRequestCreateRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fleet would refuse this merge-request body: %v\nbody: %s", err, bodies[0])
	}
	if _, err := uuid.Parse(req.UnitNodeID); err != nil {
		t.Fatalf("unit_node_id %q: fleet uuid.Parse fails: %v", req.UnitNodeID, err)
	}
	if !fleetPushClassifications[req.ToClassification] {
		t.Errorf("to_classification %q ∉ {team_shared, org_shared}", req.ToClassification)
	}
}

// TestWireContract_PlantedLegacyIDs_FleetRefuses is the planted-violation
// proof: the id shapes the harness USED to send (btoa(path), a 26-char ULID,
// a "ctxb-…" slug) must fail the mirrored decode, or the contract test above
// is vacuous.
func TestWireContract_PlantedLegacyIDs_FleetRefuses(t *testing.T) {
	for _, bad := range []string{
		"Z3VpZGFuY2Uvc3R5bGUubWQ=",   // btoa("guidance/style.md")
		"01M49Q1JB6DJ42Z17F50ZV3T1Y", // unit ULID
		"ctxb-github-readme",         // bootstrap slug
	} {
		body := `{"nodes":[{"id":"` + bad + `","kind":"guidance","title":"t","body":"b","classification":"org_shared","version":1}],"edges":[]}`
		var req fleetContextPushRequest
		if err := json.NewDecoder(strings.NewReader(body)).Decode(&req); err == nil {
			t.Errorf("legacy id %q decoded — the mirror no longer models fleet's uuid.UUID id", bad)
		}
	}
	// And the classification guard catches "personal" (the old bootstrap push).
	raw := []byte(`{"nodes":[{"id":"7e3d2c1b-0f9e-4a6c-9b8d-12a3b4c5d6e7","kind":"k","title":"t","body":"b","classification":"personal","version":1}],"edges":[]}`)
	var req fleetContextPushRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fleetPushClassifications[req.Nodes[0].Classification] {
		t.Error("personal accepted by the classification guard")
	}
}

// TestWireIDs_StableAndInstallScoped pins the derivation's properties: the
// same (install, lane, local id) is stable; a different install or lane
// gives a different id; UUIDs and "_fleet/<uuid>" paths pass through.
func TestWireIDs_StableAndInstallScoped(t *testing.T) {
	a := newWireIDsFromInstall("INSTALL-A")
	a2 := newWireIDsFromInstall("INSTALL-A")
	b := newWireIDsFromInstall("INSTALL-B")
	const path = "guidance/style.md"
	if a.For(WireLaneCurated, path) != a2.For(WireLaneCurated, path) {
		t.Error("same install + lane + id is not stable")
	}
	if a.For(WireLaneCurated, path) == b.For(WireLaneCurated, path) {
		t.Error("two installs share a wire id for the same path — cross-user collision")
	}
	if a.For(WireLaneCurated, path) == a.For(WireLaneUnit, path) {
		t.Error("lanes collide")
	}
	if !IsWireUUID(a.For(WireLaneUnit, "01M49Q1JB6DJ42Z17F50ZV3T1Y")) {
		t.Error("derived id is not a UUID")
	}
	const u = "7e3d2c1b-0f9e-4a6c-9b8d-12a3b4c5d6e7"
	if a.For(WireLaneCurated, u) != u || a.For(WireLaneCurated, "team/_fleet/"+u) != u {
		t.Error("wire UUIDs / _fleet paths must pass through")
	}
	if a.For(WireLaneCurated, "") != "" {
		t.Error("empty local id must map to empty")
	}
	// Persisted install id: two derivers over one dataDir agree.
	dir := t.TempDir()
	if NewWireIDs(dir).For(WireLaneCurated, path) != NewWireIDs(dir).For(WireLaneCurated, path) {
		t.Error("derivation not stable across restarts of the same profile")
	}
}
