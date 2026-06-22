// Package fleet — unit_mapper.go
//
// UnitMapper translates between the OSS-side, fleet-free units.Unit /
// units.Edge model (core/units) and the fleet context-graph node/edge wire
// shapes already proven by context_graph_sync.go. It is the single place
// that knows BOTH vocabularies, keeping core/units fleet-free
// (DIRECTIVE_001) and the no-fleet-imports boundary intact:
//
//   core/units  →  (mapper, here in core/fleet)  →  context node/edge wire
//
// Classification mapping (spec §2a / FR-001 / NFR-005):
//
//	personal  → local-only, NEVER pushed   (MapUnitToNode returns ok=false)
//	team      → team_shared
//	org       → org_shared
//
// The existing wire node carries id/kind/title/body/metadata/classification/
// version but has no first-class scope or load_policy column. To carry the
// unified Unit fields (FR-030: "carrying kind, scope, classification,
// version, load_policy") losslessly, scope/scope_id/load_policy are folded
// into the node Metadata under a reserved "_unit" envelope, alongside any
// caller metadata. PulledNodeToUnit reverses the fold. This keeps the wire
// schema unchanged while round-tripping every Unit field.
//
// (unified-context-artifacts-01NCTXU01 / Phase 2 / WP13)
package fleet

import (
	"encoding/json"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// unitMetaKey is the reserved metadata key under which the mapper folds the
// Unit fields the wire node has no column for (scope, scope_id, load_policy).
const unitMetaKey = "_unit"

// unitMetaEnvelope is the JSON shape stored under metadata["_unit"]. It
// carries exactly the Unit fields that the context node wire shape does not
// already model as top-level columns.
type unitMetaEnvelope struct {
	Scope      string `json:"scope"`
	ScopeID    string `json:"scope_id,omitempty"`
	LoadPolicy string `json:"load_policy"`
}

// UnitMapper maps units.Unit/Edge ↔ context node/edge wire shapes. It is
// stateless and safe for concurrent use. teamID, when non-nil, is attached
// to pushed nodes/edges for team_shared classification (the fleet server
// scopes team_shared rows by team).
type UnitMapper struct {
	teamID *string
}

// NewUnitMapper constructs a UnitMapper. teamID may be empty (no team
// scoping); when set it is attached to team_shared pushes.
func NewUnitMapper(teamID string) *UnitMapper {
	var tid *string
	if teamID != "" {
		cp := teamID
		tid = &cp
	}
	return &UnitMapper{teamID: tid}
}

// ClassificationForUnit maps a units.Classification to the fleet sync
// classification. Returns ("", false) for ClassPersonal — personal units
// are local-only and must never be pushed (NFR-005).
func ClassificationForUnit(c units.Classification) (ContextClassification, bool) {
	switch c {
	case units.ClassTeam:
		return ClassTeamShared, true
	case units.ClassOrg:
		return ClassOrgShared, true
	default:
		// personal (and any unknown) → never synced.
		return "", false
	}
}

// UnitClassificationForFleet maps a fleet classification back to a
// units.Classification. Returns ("", false) for unrecognised values.
func UnitClassificationForFleet(c ContextClassification) (units.Classification, bool) {
	switch c {
	case ClassTeamShared:
		return units.ClassTeam, true
	case ClassOrgShared:
		return units.ClassOrg, true
	default:
		return "", false
	}
}

// MapUnitToNode converts a units.Unit to the push wire node shape. It
// returns ok=false (with no error) for personal-classification units: they
// are local-only and the syncer must skip them. The node id is the unit id
// (the harness reuses the unit id as the fleet node id on first push);
// scope/scope_id/load_policy are folded into metadata under "_unit".
func (m *UnitMapper) MapUnitToNode(u units.Unit) (contextNodeInput, bool, error) {
	classification, ok := ClassificationForUnit(u.Classification)
	if !ok {
		return contextNodeInput{}, false, nil // personal → never pushed
	}
	meta, err := foldUnitMetadata(u)
	if err != nil {
		return contextNodeInput{}, false, fmt.Errorf("fleet: MapUnitToNode: %w", err)
	}
	node := contextNodeInput{
		ID:             u.ID,
		Kind:           string(u.Kind),
		Title:          u.Title,
		Body:           u.Body,
		Metadata:       meta,
		Classification: classification,
		Version:        u.Version,
	}
	if classification == ClassTeamShared {
		node.TeamID = m.teamID
	}
	return node, true, nil
}

// MapEdgeToWire converts a units.Edge to the push wire edge shape, given the
// classification of its owning units. Returns ok=false for personal
// classification (personal edges never sync).
func (m *UnitMapper) MapEdgeToWire(e units.Edge, c units.Classification) (contextEdgeInput, bool) {
	classification, ok := ClassificationForUnit(c)
	if !ok {
		return contextEdgeInput{}, false
	}
	edge := contextEdgeInput{
		ID:             e.ID,
		FromNodeID:     e.FromID,
		ToNodeID:       e.ToID,
		Kind:           string(e.Kind),
		Classification: classification,
		Version:        e.Version,
	}
	if classification == ClassTeamShared {
		edge.TeamID = m.teamID
	}
	return edge, true
}

// PulledNodeToUnit converts a pulled fleet node back to a units.Unit. The
// scope/scope_id/load_policy are unfolded from the "_unit" metadata
// envelope (with safe defaults when absent — older nodes pushed before this
// fold default to global/on_demand). The returned Unit's Version mirrors the
// server version; the caller decides whether to apply it (read-down) based
// on the sidecar baseline. Returns ok=false for classifications that do not
// map to a local unit classification.
func (m *UnitMapper) PulledNodeToUnit(n ContextPulledNode) (units.Unit, bool, error) {
	class, ok := UnitClassificationForFleet(n.Classification)
	if !ok {
		return units.Unit{}, false, nil
	}
	scope, scopeID, loadPolicy, cleanMeta, err := unfoldUnitMetadata(n.Metadata, n.Scope)
	if err != nil {
		return units.Unit{}, false, fmt.Errorf("fleet: PulledNodeToUnit: %w", err)
	}
	u := units.Unit{
		ID:             n.ID,
		Kind:           units.Kind(n.Kind),
		Scope:          scope,
		ScopeID:        scopeID,
		Classification: class,
		Version:        n.Version,
		LoadPolicy:     loadPolicy,
		Title:          n.Title,
		Body:           n.Body,
		Metadata:       cleanMeta,
	}
	return u, true, nil
}

// foldUnitMetadata merges the caller metadata with a reserved "_unit"
// envelope carrying scope/scope_id/load_policy. nil/empty caller metadata
// is treated as an empty object.
func foldUnitMetadata(u units.Unit) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(u.Metadata) > 0 {
		if err := json.Unmarshal(u.Metadata, &obj); err != nil {
			return nil, fmt.Errorf("metadata not a JSON object: %w", err)
		}
	}
	env := unitMetaEnvelope{
		Scope:      string(u.Scope),
		ScopeID:    u.ScopeID,
		LoadPolicy: string(u.LoadPolicy),
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	obj[unitMetaKey] = envBytes
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// unfoldUnitMetadata extracts scope/scope_id/load_policy from the "_unit"
// envelope and returns the caller metadata with the envelope stripped. When
// the envelope is absent it falls back to wireScope (the node's top-level
// scope column, if the server set one) for scope and ScopeGlobal /
// LoadOnDemand otherwise — a conservative default for nodes pushed before
// the fold existed.
func unfoldUnitMetadata(raw json.RawMessage, wireScope string) (units.Scope, string, units.LoadPolicy, json.RawMessage, error) {
	scope := units.ScopeGlobal
	if wireScope != "" {
		scope = units.Scope(wireScope)
	}
	loadPolicy := units.LoadOnDemand
	scopeID := ""

	if len(raw) == 0 {
		return scope, scopeID, loadPolicy, json.RawMessage("{}"), nil
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Metadata is opaque/non-object — pass it through untouched with
		// defaulted scope/load_policy rather than failing the whole pull.
		return scope, scopeID, loadPolicy, raw, nil
	}
	if envRaw, ok := obj[unitMetaKey]; ok {
		var env unitMetaEnvelope
		if err := json.Unmarshal(envRaw, &env); err == nil {
			if env.Scope != "" {
				scope = units.Scope(env.Scope)
			}
			scopeID = env.ScopeID
			if env.LoadPolicy != "" {
				loadPolicy = units.LoadPolicy(env.LoadPolicy)
			}
		}
		delete(obj, unitMetaKey)
	}
	clean, err := json.Marshal(obj)
	if err != nil {
		return scope, scopeID, loadPolicy, nil, err
	}
	return scope, scopeID, loadPolicy, clean, nil
}
