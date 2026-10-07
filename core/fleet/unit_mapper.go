// Package fleet — unit_mapper.go
//
// UnitMapper translates between the OSS-side, fleet-free units.Unit /
// units.Edge model (core/units) and the fleet context-graph node/edge wire
// shapes already proven by context_graph_sync.go. It is the single place
// that knows BOTH vocabularies, keeping core/units fleet-free
// (DIRECTIVE_001) and the no-fleet-imports boundary intact:
//
//	core/units  →  (mapper, here in core/fleet)  →  context node/edge wire
//
// Classification mapping (spec §2a / FR-001 / NFR-005):
//
//	personal  → local-only, NEVER pushed   (MapUnitToNode returns ok=false)
//	team      → team_shared
//	org       → org_shared
//
// Unit fields on the wire (owner ruling 2026-10-06, WP03): push writes the
// TOP-LEVEL unit_kind / unit_scope / scope_id / load_policy columns fleet
// validates (and its admin-only load_policy=always guard reads), and — for
// one release of compat — ALSO the metadata "_unit" envelope, built from the
// very same values so the two can never diverge (fleet 400s a contradiction:
// unit_kind_mismatch / unit_scope_mismatch).
//
// Pull reads the TOP-LEVEL fields. load_policy is read ONLY from top level
// (absent → on_demand); "_unit" is consulted only as a scope / scope_id
// fallback for pre-Phase-2 nodes, decoded with EXACT key matching — never a
// case-insensitive struct decode, which let {"_unit":{"LOAD_POLICY":
// "always"}} through fleet's old guard. "_unit" is always stripped from the
// local metadata.
//
// (unified-context-artifacts-01NCTXU01 / Phase 2 / WP13)
package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/logging"
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
	// UnitKind / UnitScope mirror the top-level columns (fleet compares
	// these two keys against top level). Write-only compat for one release.
	UnitKind  string `json:"unit_kind"`
	UnitScope string `json:"unit_scope"`
}

// UnitMapper maps units.Unit/Edge ↔ context node/edge wire shapes. It is
// stateless and safe for concurrent use. teamID, when non-nil, is attached
// to pushed nodes/edges for team_shared classification (the fleet server
// scopes team_shared rows by team).
type UnitMapper struct {
	teamID *string
	// roleCheck reports whether the signed-in user may push
	// load_policy=always (fleet: org_admin / org_owner only — 403
	// load_policy_requires_admin otherwise) and whether the identity's
	// roles are KNOWN at all. nil = no identity source wired (tests /
	// offline): treated as known non-admin.
	roleCheck func() (isAdmin, rolesKnown bool)

	// strippedUnitKeys counts case-variant "_unit" metadata keys removed
	// before push (review F13). Fleet 400s a case-variant _unit key for the
	// WHOLE batch; the harness's own envelope is the only "_unit" sent.
	strippedUnitKeys atomic.Int64
}

// StrippedUnitKeys is the cumulative count of stripped case-variant "_unit"
// metadata keys.
func (m *UnitMapper) StrippedUnitKeys() int64 {
	return m.strippedUnitKeys.Load()
}

// ErrLoadPolicyRolesUnknown: a load_policy=always unit cannot be pushed yet
// because the enrolled identity carries no roles (a pre-roles enroll), so
// whether the user is an org admin is unknown. Downgrading it to on_demand
// would silently rewrite an admin's policy on the server (review F9); the
// unit is held back — kept dirty, counted — until a re-enroll brings roles.
var ErrLoadPolicyRolesUnknown = errors.New("fleet: load_policy=always unit held: identity roles unknown (re-sign-in to refresh)")

// SetRoleCheck wires the admin check consulted when a unit with
// load_policy=always is pushed.
func (m *UnitMapper) SetRoleCheck(f func() (isAdmin, rolesKnown bool)) {
	m.roleCheck = f
}

// SetLoadAlwaysAllowed is SetRoleCheck for a check whose roles are always
// known.
func (m *UnitMapper) SetLoadAlwaysAllowed(f func() bool) {
	m.roleCheck = func() (bool, bool) { return f(), true }
}

// wireLoadPolicy is the load_policy a unit is pushed with: always only for
// an admin; a known non-admin pushes on_demand (fleet would 403 always);
// unknown roles hold the unit back (ErrLoadPolicyRolesUnknown).
func (m *UnitMapper) wireLoadPolicy(lp units.LoadPolicy) (string, error) {
	if lp != units.LoadAlways {
		return string(lp), nil
	}
	isAdmin, known := false, true
	if m.roleCheck != nil {
		isAdmin, known = m.roleCheck()
	}
	switch {
	case !known:
		return "", ErrLoadPolicyRolesUnknown
	case isAdmin:
		return string(lp), nil
	default:
		return string(units.LoadOnDemand), nil
	}
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
	if !unitKindPushable(u.Kind) {
		// artifact (device-local by design) or an unknown kind: fleet 400s
		// it (invalid_unit_kind), so it never reaches the wire.
		return contextNodeInput{}, false, fmt.Errorf("fleet: MapUnitToNode %s: %w: %q", u.ID, ErrUnitKindNotPushable, u.Kind)
	}
	if err := checkMetadataNotCapability(u.Metadata); err != nil {
		return contextNodeInput{}, false, fmt.Errorf("fleet: MapUnitToNode %s: %w", u.ID, err)
	}
	loadPolicy, err := m.wireLoadPolicy(u.LoadPolicy)
	if err != nil {
		return contextNodeInput{}, false, fmt.Errorf("fleet: MapUnitToNode %s: %w", u.ID, err)
	}
	meta, stripped, err := foldUnitMetadata(u, loadPolicy)
	if stripped > 0 {
		m.strippedUnitKeys.Add(int64(stripped))
		logging.L().Warn("fleet.unit.push.stripped_unit_keys", "unit_id", u.ID, "count", stripped)
	}
	if err != nil {
		return contextNodeInput{}, false, fmt.Errorf("fleet: MapUnitToNode: %w", err)
	}
	node := contextNodeInput{
		ID:             u.ID,
		Kind:           string(u.Kind), // unit lane: kind == unit_kind
		Title:          u.Title,
		Body:           u.Body,
		Metadata:       meta,
		Classification: classification,
		Version:        u.Version,
		UnitKind:       string(u.Kind),
		UnitScope:      string(u.Scope),
		ScopeID:        u.ScopeID,
		LoadPolicy:     loadPolicy,
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
	switch e.Kind {
	case units.EdgeReferences, units.EdgeDerivedFrom, units.EdgePromotedFrom, units.EdgeSupersedes:
		edge.UnitKind = string(e.Kind) // fleet's lineage enum; conflicts_with is not in it
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
	scope, scopeID, loadPolicy, cleanMeta, err := unfoldUnitMetadata(n)
	if err != nil {
		return units.Unit{}, false, fmt.Errorf("fleet: PulledNodeToUnit: %w", err)
	}
	// Artifact units are LOCAL-ONLY in both directions (the delegated
	// exception, core/units): egress is refused in views/fleet and by the
	// personal classification; this closes INGRESS too — a team node
	// claiming kind=artifact would otherwise land in the local Library and
	// media refcounts (adversarial-review F3, 2026-10-05). Skipped, not an
	// error: the pull continues past it.
	if units.Kind(n.Kind) == units.KindArtifact {
		return units.Unit{}, false, nil
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

// foldUnitMetadata merges the caller metadata with the reserved "_unit"
// envelope. loadPolicy is the WIRE load policy (after the admin downgrade),
// so the envelope always equals the top-level columns. nil/empty caller
// metadata is treated as an empty object; a caller "_unit" key is replaced
// and case-variant "_UNIT"/"_Unit" keys are stripped (returned count, F13).
func foldUnitMetadata(u units.Unit, loadPolicy string) (json.RawMessage, int, error) {
	obj := map[string]json.RawMessage{}
	if len(u.Metadata) > 0 {
		if err := json.Unmarshal(u.Metadata, &obj); err != nil {
			return nil, 0, fmt.Errorf("metadata not a JSON object: %w", err)
		}
	}
	stripped := stripUnitKeyVariants(obj, false)
	env := unitMetaEnvelope{
		Scope:      string(u.Scope),
		ScopeID:    u.ScopeID,
		LoadPolicy: loadPolicy,
		UnitKind:   string(u.Kind),
		UnitScope:  string(u.Scope),
	}
	envBytes, err := json.Marshal(env)
	if err != nil {
		return nil, stripped, err
	}
	obj[unitMetaKey] = envBytes
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, stripped, err
	}
	return out, stripped, nil
}

// stripUnitKeyVariants deletes every key that equals "_unit"
// case-insensitively but not exactly (and the exact key too when
// includeExact). Returns how many were removed.
func stripUnitKeyVariants(obj map[string]json.RawMessage, includeExact bool) int {
	n := 0
	for k := range obj {
		if strings.EqualFold(k, unitMetaKey) && (includeExact || k != unitMetaKey) {
			delete(obj, k)
			n++
		}
	}
	return n
}

// sanitizeCuratedMetadata removes every "_unit" key (any case) from
// Curated node metadata — the Curated lane carries no unit envelope, and a
// case variant 400s the batch on fleet (F13). Non-object metadata is
// returned unchanged. Returns the metadata and the stripped count.
func sanitizeCuratedMetadata(md json.RawMessage) (json.RawMessage, int) {
	if len(md) == 0 {
		return md, 0
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(md, &obj); err != nil {
		return md, 0
	}
	n := stripUnitKeyVariants(obj, true)
	if n == 0 {
		return md, 0
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return md, 0
	}
	return out, n
}

// unfoldUnitMetadata derives scope / scope_id / load_policy for a pulled node
// and returns its metadata with "_unit" stripped.
//
//   - load_policy: TOP-LEVEL ONLY. Absent or unrecognised → on_demand. The
//     "_unit" envelope is never read for it (WP03 security ruling).
//   - scope / scope_id: top-level unit_scope / scope_id; when unit_scope is
//     absent (pre-Phase-2 node), the "_unit" envelope's exact "scope" /
//     "scope_id" keys (strictUnitEnvelope), then the legacy wire scope.
func unfoldUnitMetadata(n ContextPulledNode) (units.Scope, string, units.LoadPolicy, json.RawMessage, error) {
	loadPolicy := units.LoadOnDemand
	if n.LoadPolicy == string(units.LoadAlways) {
		loadPolicy = units.LoadAlways
	}
	scope := units.ScopeGlobal
	scopeID := n.ScopeID
	fromTop := n.UnitScope != ""
	if fromTop {
		scope = units.Scope(n.UnitScope)
	} else if n.Scope != "" && validUnitScope(n.Scope) {
		scope = units.Scope(n.Scope)
	}

	raw := n.Metadata
	if len(raw) == 0 {
		return scope, scopeID, loadPolicy, json.RawMessage("{}"), nil
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Metadata is opaque/non-object — pass it through untouched.
		return scope, scopeID, loadPolicy, raw, nil
	}
	if envRaw, ok := obj[unitMetaKey]; ok {
		if !fromTop {
			if env, ok := strictUnitEnvelope(envRaw); ok {
				if v := env["scope"]; v != "" && validUnitScope(v) {
					scope = units.Scope(v)
				}
				if scopeID == "" {
					scopeID = env["scope_id"]
				}
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

// unitEnvelopeKeys are the only keys strictUnitEnvelope reads, matched
// EXACTLY (byte-for-byte, lower case).
var unitEnvelopeKeys = map[string]bool{
	"scope": true, "scope_id": true, "load_policy": true, "unit_kind": true, "unit_scope": true,
}

// strictUnitEnvelope decodes a "_unit" envelope with exact key matching:
// json.Unmarshal into a struct matches tags case-insensitively (so
// "LOAD_POLICY" would fill load_policy) — this does not. An envelope with a
// case variant of a known key, a duplicate known key, or a non-string value
// for one is rejected wholesale (ok=false) as hostile.
func strictUnitEnvelope(raw json.RawMessage) (map[string]string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, false
	}
	out := map[string]string{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, _ := kt.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		lower := strings.ToLower(key)
		if !unitEnvelopeKeys[lower] {
			continue // unrelated key
		}
		if key != lower {
			return nil, false // case variant of a known key
		}
		if _, dup := out[key]; dup {
			return nil, false // duplicate known key
		}
		var s string
		if err := json.Unmarshal(val, &s); err != nil {
			return nil, false // non-string value
		}
		out[key] = s
	}
	if lp, ok := out["load_policy"]; ok && lp != "" && lp != string(units.LoadAlways) && lp != string(units.LoadOnDemand) {
		return nil, false
	}
	return out, true
}

func validUnitScope(s string) bool {
	switch units.Scope(s) {
	case units.ScopeGlobal, units.ScopeProject, units.ScopeSession:
		return true
	}
	return false
}
