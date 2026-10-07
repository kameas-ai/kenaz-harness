// Package fleet — knowledge_kinds.go
//
// The harness side of fleet's context-graph knowledge boundary (owner
// rulings 2026-10-06, WP03; kenaz-fleet service/context_kinds.go). The
// context graph carries KNOWLEDGE only — capabilities (skills, workflows,
// packs, bundles, MCP recipes) never enter it. Fleet enforces this on push;
// the harness enforces the same rules BEFORE the wire so a bad node is a
// named local error instead of a whole-batch 400, and splits /context/pull
// into one consumer per lane by node `kind`.
//
// Lanes (the discriminator is `kind`, NOT unit_kind — a Curated entry and a
// doc unit both carry unit_kind=doc):
//
//	Curated (Knowledge › Curated)  kind ∈ glossary | explanation | guidance | procedure
//	                               unit_kind = doc
//	Unit    (Library units)        kind ∈ root | doc | snippet | tool_output
//	                               kind == unit_kind
//
// Each lane pulls with ?kind=<its set>; a server that ignores the filter
// returns everything, so each lane ALSO skips-and-counts nodes of other kinds
// (never aborting its cursor, never mislisting another lane's nodes).
package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// curatedKinds is the Curated lane's node-kind set (context-pack entry
// kinds; pack "skill" was renamed "procedure" 2026-10-06).
var curatedKinds = []string{"glossary", "explanation", "guidance", "procedure"}

// unitLaneKinds is the Unit lane's node-kind set (pushable unit kinds;
// artifact is device-local and never on the wire).
var unitLaneKinds = []string{"root", "doc", "snippet", "tool_output"}

// curatedUnitKind is the unit_kind every Curated node carries on push.
const curatedUnitKind = "doc"

// reservedCapabilityKinds are capability words fleet refuses as a node kind
// (400 kind_not_knowledge) and as metadata type/kind/category (case-
// insensitive). Mirrors kenaz-fleet service/context_kinds.go.
var reservedCapabilityKinds = map[string]bool{
	"skill": true, "workflow": true, "pack": true, "bundle": true,
	"agent_pack": true, "mcp": true, "recipe": true,
}

// ErrKindNotKnowledge: a node kind (or metadata type/kind/category) names a
// capability. Refused before the wire.
var ErrKindNotKnowledge = errors.New("fleet: kind names a capability, not knowledge — capabilities never enter the context graph")

// ErrKindNotInLane: a node kind is not one the pushing lane owns (the
// receiving lane would never pull it back). Refused before the wire.
var ErrKindNotInLane = errors.New("fleet: node kind is not a kind this sync lane carries")

// ErrUnitKindNotPushable: a unit kind fleet refuses on push (artifact).
var ErrUnitKindNotPushable = errors.New("fleet: unit kind is not pushable (artifacts are device-local)")

func inSet(set []string, k string) bool {
	for _, s := range set {
		if s == k {
			return true
		}
	}
	return false
}

// laneKindQuery is the pull query string selecting a lane's kinds.
func laneKindQuery(kinds []string) string {
	return "kind=" + url.QueryEscape(strings.Join(kinds, ","))
}

// laneQuery joins the lane filter and an optional cursor into a pull path.
func lanePullPath(kinds []string, cursor string) string {
	p := "/api/v1/context/pull?" + laneKindQuery(kinds)
	if cursor != "" {
		p += "&since=" + url.QueryEscape(cursor)
	}
	return p
}

// NormalizeCuratedKind maps a Curated push kind to its wire form: the legacy
// pack spelling "skill" becomes "procedure"; a capability word is refused
// (ErrKindNotKnowledge); anything outside the Curated set is refused
// (ErrKindNotInLane). Empty defaults to "guidance" (the Share default).
func NormalizeCuratedKind(kind string) (string, error) {
	k := strings.TrimSpace(kind)
	if k == "" {
		return "guidance", nil
	}
	if strings.EqualFold(k, "skill") {
		return "procedure", nil
	}
	if reservedCapabilityKinds[strings.ToLower(k)] {
		return "", fmt.Errorf("%w: %q", ErrKindNotKnowledge, kind)
	}
	if !inSet(curatedKinds, k) {
		return "", fmt.Errorf("%w: %q is not a Curated kind (%s)", ErrKindNotInLane, kind, strings.Join(curatedKinds, ", "))
	}
	return k, nil
}

// checkMetadataNotCapability refuses node metadata whose top-level type,
// kind or category names a capability (fleet 400s it). Non-object or empty
// metadata passes.
func checkMetadataNotCapability(md json.RawMessage) error {
	if len(md) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(md, &obj); err != nil {
		return nil
	}
	for key, raw := range obj {
		switch strings.ToLower(key) {
		case "type", "kind", "category":
		default:
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) == nil && reservedCapabilityKinds[strings.ToLower(strings.TrimSpace(v))] {
			return fmt.Errorf("%w: metadata.%s = %q", ErrKindNotKnowledge, key, v)
		}
	}
	return nil
}

// unitLaneAccepts reports whether a pulled node belongs to the Unit lane.
func unitLaneAccepts(n ContextPulledNode) bool {
	if !inSet(unitLaneKinds, n.Kind) {
		return false
	}
	// kind == unit_kind on this lane; a node that says otherwise belongs to
	// another lane (or none). Absent unit_kind = a pre-Phase-2 node.
	return n.UnitKind == "" || n.UnitKind == n.Kind
}

// curatedLaneAccepts reports whether a pulled node belongs to the Curated lane.
func curatedLaneAccepts(n ContextPulledNode) bool {
	return inSet(curatedKinds, n.Kind)
}

// unitKindPushable reports whether k may go on the wire as a unit_kind.
func unitKindPushable(k units.Kind) bool {
	return inSet(unitLaneKinds, string(k))
}
