package fleet

import (
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// TestMapUnitToNode_PersonalNeverPushed is the load-bearing invariant
// (NFR-005): a personal-classification unit must never produce a pushable
// node — the mapper returns ok=false so the syncer skips it.
func TestMapUnitToNode_PersonalNeverPushed(t *testing.T) {
	m := NewUnitMapper("")
	u := units.Unit{
		ID:             "u-personal",
		Kind:           units.KindDoc,
		Scope:          units.ScopeProject,
		ScopeID:        "proj-1",
		Classification: units.ClassPersonal,
		LoadPolicy:     units.LoadAlways,
		Title:          "secret notes",
		Body:           "local only",
	}
	node, ok, err := m.MapUnitToNode(u)
	if err != nil {
		t.Fatalf("MapUnitToNode: %v", err)
	}
	if ok {
		t.Fatalf("personal unit must NOT be pushable, got node %+v", node)
	}
}

func TestClassificationForUnit(t *testing.T) {
	cases := []struct {
		in     units.Classification
		want   ContextClassification
		wantOK bool
	}{
		{units.ClassTeam, ClassTeamShared, true},
		{units.ClassOrg, ClassOrgShared, true},
		{units.ClassPersonal, "", false},
	}
	for _, c := range cases {
		got, ok := ClassificationForUnit(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("ClassificationForUnit(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// TestMapUnitToNode_CarriesAllFields verifies the node carries kind, body,
// classification, version and that scope/scope_id/load_policy are folded
// into metadata (FR-030 "carrying kind, scope, classification, version,
// load_policy").
func TestMapUnitToNode_CarriesAllFields(t *testing.T) {
	m := NewUnitMapper("team-42")
	u := units.Unit{
		ID:             "u-team",
		Kind:           units.KindRoot,
		Scope:          units.ScopeProject,
		ScopeID:        "proj-9",
		Classification: units.ClassTeam,
		LoadPolicy:     units.LoadAlways,
		Version:        3,
		Title:          "Team root",
		Body:           "# root",
		Metadata:       json.RawMessage(`{"custom":"keep"}`),
	}
	node, ok, err := m.MapUnitToNode(u)
	if err != nil || !ok {
		t.Fatalf("MapUnitToNode: ok=%v err=%v", ok, err)
	}
	if node.ID != "u-team" || node.Kind != "root" || node.Body != "# root" {
		t.Errorf("node basic fields wrong: %+v", node)
	}
	if node.Classification != ClassTeamShared {
		t.Errorf("classification = %q, want team_shared", node.Classification)
	}
	if node.Version != 3 {
		t.Errorf("version = %d, want 3", node.Version)
	}
	if node.TeamID == nil || *node.TeamID != "team-42" {
		t.Errorf("teamID = %v, want team-42", node.TeamID)
	}
	// Metadata must carry the custom key AND the folded _unit envelope.
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(node.Metadata, &meta); err != nil {
		t.Fatalf("node metadata not an object: %v", err)
	}
	if _, ok := meta["custom"]; !ok {
		t.Errorf("custom metadata key lost: %s", node.Metadata)
	}
	envRaw, ok := meta[unitMetaKey]
	if !ok {
		t.Fatalf("_unit envelope missing: %s", node.Metadata)
	}
	var env unitMetaEnvelope
	if err := json.Unmarshal(envRaw, &env); err != nil {
		t.Fatalf("envelope parse: %v", err)
	}
	if env.Scope != "project" || env.ScopeID != "proj-9" || env.LoadPolicy != "always" {
		t.Errorf("envelope = %+v, want project/proj-9/always", env)
	}
}

// TestPulledNodeToUnit_RoundTrip verifies a node maps back to a Unit with all
// fields recovered and the _unit envelope stripped from the surfaced metadata.
func TestPulledNodeToUnit_RoundTrip(t *testing.T) {
	m := NewUnitMapper("")
	orig := units.Unit{
		ID:             "u-rt",
		Kind:           units.KindDoc,
		Scope:          units.ScopeSession,
		ScopeID:        "sess-7",
		Classification: units.ClassOrg,
		LoadPolicy:     units.LoadOnDemand,
		Version:        5,
		Title:          "doc",
		Body:           "body",
		Metadata:       json.RawMessage(`{"k":"v"}`),
	}
	node, ok, err := m.MapUnitToNode(orig)
	if err != nil || !ok {
		t.Fatalf("MapUnitToNode: %v ok=%v", err, ok)
	}

	pulled := ContextPulledNode{
		ID:             node.ID,
		Kind:           node.Kind,
		Title:          node.Title,
		Body:           node.Body,
		Metadata:       node.Metadata,
		Classification: node.Classification,
		Version:        node.Version,
	}
	got, ok, err := m.PulledNodeToUnit(pulled)
	if err != nil || !ok {
		t.Fatalf("PulledNodeToUnit: %v ok=%v", err, ok)
	}
	if got.Kind != units.KindDoc || got.Scope != units.ScopeSession || got.ScopeID != "sess-7" {
		t.Errorf("scope/kind not recovered: %+v", got)
	}
	if got.Classification != units.ClassOrg || got.LoadPolicy != units.LoadOnDemand || got.Version != 5 {
		t.Errorf("class/loadpolicy/version not recovered: %+v", got)
	}
	// The surfaced metadata must NOT contain the internal _unit envelope.
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got.Metadata, &meta); err != nil {
		t.Fatalf("recovered metadata: %v", err)
	}
	if _, leaked := meta[unitMetaKey]; leaked {
		t.Errorf("_unit envelope leaked into recovered metadata: %s", got.Metadata)
	}
	if string(meta["k"]) != `"v"` {
		t.Errorf("custom metadata lost: %s", got.Metadata)
	}
}

// TestPulledNodeToUnit_UnknownClassificationSkipped ensures a node with a
// classification that does not map locally is skipped (ok=false), not errored.
func TestPulledNodeToUnit_UnknownClassificationSkipped(t *testing.T) {
	m := NewUnitMapper("")
	_, ok, err := m.PulledNodeToUnit(ContextPulledNode{ID: "x", Classification: "mystery"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("unknown classification must be skipped")
	}
}
