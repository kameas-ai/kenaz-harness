package fleet

// knowledge_kinds_test.go — WP03 (owner rulings 2026-10-06): top-level unit
// fields, lane split by kind with skip-and-count, the Curated push guard, and
// the strict metadata._unit decode.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// lanePullServer serves a fixed pull page to every GET — like a server that
// IGNORES ?kind= — and records the query of each request.
type lanePullServer struct {
	mu      sync.Mutex
	page    contextPullResponse
	queries []string
	pushes  int
}

func (s *lanePullServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/pull":
		s.queries = append(s.queries, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode(s.page)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
		s.pushes++
		_, _ = w.Write([]byte(`{"accepted_nodes":1,"accepted_edges":0,"conflicts":[]}`))
	default:
		http.NotFound(w, r)
	}
}

func (s *lanePullServer) snapshot() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...), s.pushes
}

func pulledNode(id, kind, unitKind string) ContextPulledNode {
	return ContextPulledNode{
		ID: id, Kind: kind, UnitKind: unitKind, Title: id, Body: "b",
		Classification: ClassOrgShared, Version: 1, UpdatedAt: "2026-10-06T10:00:00Z",
	}
}

// mixedPage is one pull page holding every lane's nodes plus unknowns.
func mixedPage() contextPullResponse {
	return contextPullResponse{
		Nodes: []ContextPulledNode{
			pulledNode("6b2f6f0e-0000-4000-8000-000000000001", "doc", "doc"),            // unit lane
			pulledNode("6b2f6f0e-0000-4000-8000-000000000002", "guidance", "doc"),       // Curated (unit_kind doc!)
			pulledNode("6b2f6f0e-0000-4000-8000-000000000003", "procedure", "doc"),      // Curated
			pulledNode("6b2f6f0e-0000-4000-8000-000000000004", "project", ""),           // bootstrap taxonomy
			pulledNode("6b2f6f0e-0000-4000-8000-000000000005", "skill", ""),             // capability word (pre-boundary row)
			pulledNode("6b2f6f0e-0000-4000-8000-000000000006", "artifact", "artifact"),  // never local
			pulledNode("6b2f6f0e-0000-4000-8000-000000000007", "snippet", "snippet"),    // unit lane
			pulledNode("6b2f6f0e-0000-4000-8000-000000000008", "brand-new-kind", "doc"), // unknown
		},
		Cursor: "2026-10-06T10:00:00.5Z",
	}
}

// The unit lane asks for its kinds, skips + counts the rest, applies its own,
// and ADVANCES the cursor — one foreign node used to stall it forever (§0-E).
func TestUnitSyncer_PullDown_LaneFilterSkipAndCount(t *testing.T) {
	srv := &lanePullServer{page: mixedPage()}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := newUnitTestManager()
	s := NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())

	applied, err := s.PullDown(context.Background())
	if err != nil {
		t.Fatalf("PullDown: %v (a foreign kind must never fail the lane)", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2 (doc + snippet)", applied)
	}
	st := s.Status()
	if st.SkippedUnknownKinds != 6 {
		t.Errorf("SkippedUnknownKinds = %d, want 6", st.SkippedUnknownKinds)
	}
	if st.Cursor != "2026-10-06T10:00:00.5Z" {
		t.Errorf("cursor = %q — the lane did not advance", st.Cursor)
	}
	queries, _ := srv.snapshot()
	if len(queries) != 1 || !strings.Contains(queries[0], "kind=root%2Cdoc%2Csnippet%2Ctool_output") {
		t.Errorf("unit lane pull query = %v, want the unit kind filter", queries)
	}
	// A Curated node sharing unit_kind=doc never became a local unit.
	all, _ := m.List(context.Background(), units.UnitFilter{})
	for _, u := range all {
		if u.Title == "6b2f6f0e-0000-4000-8000-000000000002" {
			t.Error("a Curated guidance node was created as a unit")
		}
	}
}

// A unit-lane node the local store rejects as invalid is skipped and counted,
// not a lane-stalling error.
func TestUnitSyncer_PullDown_InvalidNodeSkipped(t *testing.T) {
	bad := pulledNode("6b2f6f0e-0000-4000-8000-0000000000aa", "doc", "doc")
	bad.UnitScope = "galaxy"
	srv := &lanePullServer{page: contextPullResponse{Nodes: []ContextPulledNode{bad}, Cursor: "c2"}}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewUnitSyncer(makeTestClient(t, hs.URL), newUnitTestManager(), NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.PullDown(context.Background()); err != nil {
		t.Fatalf("PullDown: %v", err)
	}
	if st := s.Status(); st.SkippedInvalid != 1 || st.Cursor != "c2" {
		t.Errorf("status = %+v, want 1 invalid skipped and the cursor advanced", st)
	}
}

// The Curated lane asks for its kinds and never lists another lane's node
// under Knowledge › Curated.
func TestContextGraphSyncer_PullDelta_LaneFilterSkipAndCount(t *testing.T) {
	srv := &lanePullServer{page: mixedPage()}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewContextGraphSyncer(makeTestClient(t, hs.URL), t.TempDir(), makeCapPollerWithTeamCap(t))
	if _, err := s.PullDelta(context.Background()); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	got := s.PulledEntries()
	if len(got) != 2 {
		t.Fatalf("PulledEntries = %d, want 2 (guidance + procedure)", len(got))
	}
	for _, e := range got {
		if e.Kind != "guidance" && e.Kind != "procedure" {
			t.Errorf("non-Curated kind %q listed under Curated", e.Kind)
		}
	}
	if n := s.Status().SkippedUnknownKinds; n != 6 {
		t.Errorf("SkippedUnknownKinds = %d, want 6", n)
	}
	queries, _ := srv.snapshot()
	if len(queries) != 1 || !strings.Contains(queries[0], "kind=glossary%2Cexplanation%2Cguidance%2Cprocedure") {
		t.Errorf("Curated pull query = %v, want the Curated kind filter", queries)
	}
}

// Curated push: legacy "skill" goes out as "procedure", capability words and
// foreign kinds never reach the wire, unit_kind is always doc.
func TestContextGraphSyncer_PushEntry_KindGuard(t *testing.T) {
	cap, client := newWireCaptureClient(t)
	s := NewContextGraphSyncer(client, t.TempDir(), makeCapPollerWithTeamCap(t))
	ctx := context.Background()
	entry := ContextNodeEntry{ID: "how/deploy.md", Layer: contextpack.LayerOrg, Kind: "skill", Title: "deploy", Body: "steps", Version: 1}
	if _, err := s.PushEntry(ctx, entry, nil); err != nil {
		t.Fatalf("PushEntry(skill): %v", err)
	}
	bodies := cap.snapshot("/api/v1/context/push")
	req := decodeFleetPush(t, bodies[0])
	if req.Nodes[0].Kind != "procedure" || req.Nodes[0].UnitKind != "doc" {
		t.Errorf("wire kind/unit_kind = %q/%q, want procedure/doc", req.Nodes[0].Kind, req.Nodes[0].UnitKind)
	}

	for _, k := range []string{"workflow", "Pack", "agent_pack", "mcp", "recipe", "bundle"} {
		entry.Kind = k
		if _, err := s.PushEntry(ctx, entry, nil); !errors.Is(err, ErrKindNotKnowledge) {
			t.Errorf("kind %q: err = %v, want ErrKindNotKnowledge", k, err)
		}
	}
	entry.Kind = "fact"
	if _, err := s.PushEntry(ctx, entry, nil); !errors.Is(err, ErrKindNotInLane) {
		t.Errorf("kind fact: err = %v, want ErrKindNotInLane", err)
	}
	entry.Kind = "guidance"
	entry.Metadata = json.RawMessage(`{"Category":"Skill"}`)
	if _, err := s.PushEntry(ctx, entry, nil); !errors.Is(err, ErrKindNotKnowledge) {
		t.Errorf("capability metadata: err = %v, want ErrKindNotKnowledge", err)
	}
	if got := len(cap.snapshot("/api/v1/context/push")); got != 1 {
		t.Errorf("%d push bodies reached the wire, want only the first", got)
	}
}

// Unit push: the top-level fields and metadata._unit are built from the same
// values and NEVER diverge — across every kind × scope × load-policy × role.
func TestMapUnitToNode_TopLevelAndUnitEnvelopeNeverDiverge(t *testing.T) {
	for _, admin := range []bool{false, true} {
		m := NewUnitMapper("")
		isAdmin := admin
		m.SetLoadAlwaysAllowed(func() bool { return isAdmin })
		for _, k := range []units.Kind{units.KindRoot, units.KindDoc, units.KindSnippet, units.KindToolOutput} {
			for _, sc := range []units.Scope{units.ScopeGlobal, units.ScopeProject, units.ScopeSession} {
				for _, lp := range []units.LoadPolicy{units.LoadAlways, units.LoadOnDemand} {
					u := units.Unit{ID: "u", Kind: k, Scope: sc, ScopeID: "s", Classification: units.ClassTeam, LoadPolicy: lp,
						Metadata: json.RawMessage(`{"_unit":{"unit_kind":"artifact","load_policy":"always"}}`)}
					node, ok, err := m.MapUnitToNode(u)
					if err != nil || !ok {
						t.Fatalf("map: %v", err)
					}
					var meta map[string]map[string]string
					if err := json.Unmarshal(node.Metadata, &meta); err != nil {
						t.Fatal(err)
					}
					env := meta["_unit"]
					if env["unit_kind"] != node.UnitKind || env["unit_scope"] != node.UnitScope ||
						env["scope"] != node.UnitScope || env["scope_id"] != node.ScopeID || env["load_policy"] != node.LoadPolicy {
						t.Errorf("diverged: top=%s/%s/%s/%s env=%v", node.UnitKind, node.UnitScope, node.ScopeID, node.LoadPolicy, env)
					}
					if node.Kind != node.UnitKind {
						t.Errorf("unit lane kind %q != unit_kind %q", node.Kind, node.UnitKind)
					}
					if !admin && node.LoadPolicy == "always" {
						t.Error("a non-admin push carries load_policy=always (fleet 403s the batch)")
					}
				}
			}
		}
	}
}

// Artifacts never reach the wire; the rest of the batch still pushes.
func TestUnitSyncer_PushDirty_ArtifactRefusedLocally(t *testing.T) {
	srv := &lanePullServer{}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := &stubDirtyStore{dirty: []units.Unit{
		{ID: "a1", Kind: units.KindArtifact, Scope: units.ScopeGlobal, Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand},
		{ID: "d1", Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam, LoadPolicy: units.LoadOnDemand},
	}}
	s := NewUnitSyncer(makeTestClient(t, hs.URL), m, NewUnitMapper(""), makeCapPollerWithTeamCap(t), t.TempDir())
	if _, err := s.PushDirty(context.Background()); err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	if _, pushes := srv.snapshot(); pushes != 1 {
		t.Errorf("pushes = %d, want 1 (the doc)", pushes)
	}
	if st := s.Status(); st.PushRefused != 1 {
		t.Errorf("PushRefused = %d, want 1 (the artifact)", st.PushRefused)
	}
}

// stubDirtyStore is a minimal UnitStore returning a fixed dirty set (it can
// hold a team-classified artifact, which the real store never produces).
type stubDirtyStore struct {
	UnitStore
	dirty []units.Unit
}

func (s *stubDirtyStore) ListDirty(_ context.Context, c units.Classification) ([]units.Unit, error) {
	var out []units.Unit
	for _, u := range s.dirty {
		if u.Classification == c {
			out = append(out, u)
		}
	}
	return out, nil
}
func (s *stubDirtyStore) ListEdges(context.Context, string) ([]units.Edge, error) { return nil, nil }
func (s *stubDirtyStore) GetSyncState(context.Context, string) (units.SyncState, error) {
	return units.SyncState{}, units.ErrSyncStateNotFound
}
func (s *stubDirtyStore) UpsertSyncState(_ context.Context, st units.SyncState) (units.SyncState, error) {
	return st, nil
}

// Strict _unit decode (security): a case-variant LOAD_POLICY is NOT honoured,
// and load_policy is never read from _unit at all — top level only.
func TestPulledNodeToUnit_UnitEnvelopeStrict(t *testing.T) {
	m := NewUnitMapper("")
	base := pulledNode("6b2f6f0e-0000-4000-8000-0000000000bb", "doc", "doc")

	n := base
	n.Metadata = json.RawMessage(`{"_unit":{"LOAD_POLICY":"always","scope":"project","scope_id":"p1"}}`)
	u, ok, err := m.PulledNodeToUnit(n)
	if err != nil || !ok {
		t.Fatalf("map: %v", err)
	}
	if u.LoadPolicy != units.LoadOnDemand {
		t.Errorf("LOAD_POLICY case variant honoured: %q", u.LoadPolicy)
	}
	if u.Scope != units.ScopeGlobal {
		t.Errorf("an envelope with a case-variant key must be rejected wholesale; scope = %q", u.Scope)
	}

	n.Metadata = json.RawMessage(`{"_unit":{"load_policy":"always"}}`)
	if u, _, _ = m.PulledNodeToUnit(n); u.LoadPolicy != units.LoadOnDemand {
		t.Error("load_policy read from _unit — top level is the only read path")
	}
	n.LoadPolicy = "always"
	if u, _, _ = m.PulledNodeToUnit(n); u.LoadPolicy != units.LoadAlways {
		t.Error("top-level load_policy=always not honoured")
	}

	n = base
	n.Metadata = json.RawMessage(`{"_unit":{"scope":"project","scope":"session"}}`)
	if u, _, _ = m.PulledNodeToUnit(n); u.Scope != units.ScopeGlobal {
		t.Errorf("duplicate-key envelope honoured: scope %q", u.Scope)
	}
	n.Metadata = json.RawMessage(`{"_unit":{"scope":7}}`)
	if u, _, _ = m.PulledNodeToUnit(n); u.Scope != units.ScopeGlobal {
		t.Errorf("non-string envelope value honoured: scope %q", u.Scope)
	}
	// Exact keys still work as the pre-Phase-2 scope fallback, and _unit
	// never leaks into local metadata.
	n.Metadata = json.RawMessage(`{"_unit":{"scope":"project","scope_id":"p9"},"k":"v"}`)
	u, _, _ = m.PulledNodeToUnit(n)
	if u.Scope != units.ScopeProject || u.ScopeID != "p9" || strings.Contains(string(u.Metadata), "_unit") {
		t.Errorf("fallback/strip wrong: %+v %s", u, u.Metadata)
	}
	// Top-level unit_scope wins over the envelope.
	n.UnitScope, n.ScopeID = "session", "s1"
	if u, _, _ = m.PulledNodeToUnit(n); u.Scope != units.ScopeSession || u.ScopeID != "s1" {
		t.Errorf("top-level scope not authoritative: %+v", u)
	}
}
