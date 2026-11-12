package cedar

import (
	"strings"
	"sync/atomic"
	"time"
)

// FleetPolicyMeta is what the org's signed bundle says about the fleet
// policy currently installed: who published it and when. Both fields are
// optional (the bundle's cedar_delta carries published_at today; a
// publisher is shown only when Fleet sends one).
type FleetPolicyMeta struct {
	PublishedBy string
	PublishedAt time.Time
}

// fleetMetaIndex is the publish metadata of the installed fleet rule set,
// keyed by the team_rule_ids it covers. It is process-wide rather than on
// Engine or Decision: cedar.Decision is a Wails-bound type (its declaration
// is pinned by check-codegen's wailsjs hash), and production has exactly one
// engine (check-cedar-engine-singleton). Keying by rule id means metadata is
// only ever attached to a decision from the snapshot it describes.
type fleetMetaIndex struct {
	rules map[string]struct{}
	meta  FleetPolicyMeta
}

var fleetMeta atomic.Pointer[fleetMetaIndex]

// SetFleetPolicyMeta records the publish metadata of the fleet rule
// snapshot just installed (core/fleet's cedar applier calls it after
// ReplaceHarnessSnippets) for the team_rule_ids it installed. A nil meta or
// no rule ids (the snapshot cleared every fleet rule) clears it.
func SetFleetPolicyMeta(ruleIDs []string, m *FleetPolicyMeta) {
	if m == nil || len(ruleIDs) == 0 {
		fleetMeta.Store(nil)
		return
	}
	idx := &fleetMetaIndex{rules: make(map[string]struct{}, len(ruleIDs)), meta: *m}
	for _, id := range ruleIDs {
		idx.rules[id] = struct{}{}
	}
	fleetMeta.Store(idx)
}

// fleetMetaFor returns the metadata for a fleet rule id, nil when unknown.
func fleetMetaFor(ruleID string) *FleetPolicyMeta {
	idx := fleetMeta.Load()
	if idx == nil {
		return nil
	}
	if _, ok := idx.rules[ruleID]; !ok {
		return nil
	}
	m := idx.meta
	return &m
}

// FleetDenialText is the user-facing copy for a tool call (or any action)
// blocked by a rule from the org's Fleet policy (E2E-11). The Cedar
// internals — action, resource UID, matched policy id — stay in logs and
// the raw error, never in this text.
const FleetDenialText = "Blocked by your organization's Fleet policy"

// FleetDenialMessage renders FleetDenialText plus the publisher / publish
// time when the bundle metadata carries them.
func FleetDenialMessage(m *FleetPolicyMeta) string {
	if m == nil {
		return FleetDenialText
	}
	var parts []string
	if by := strings.TrimSpace(m.PublishedBy); by != "" {
		parts = append(parts, "published by "+by)
	}
	if !m.PublishedAt.IsZero() {
		at := m.PublishedAt.UTC().Format("2006-01-02 15:04 UTC")
		if len(parts) == 0 {
			parts = append(parts, "published "+at)
		} else {
			parts[0] += " on " + at
		}
	}
	if len(parts) == 0 {
		return FleetDenialText
	}
	return FleetDenialText + " (" + strings.Join(parts, "") + ")"
}

// UserFacingDenial returns the user-facing message for a Deny decision
// produced by a FLEET rule, and false for every other decision (allow,
// not-applicable, or a deny from a local / embedded rule — callers keep
// their existing copy for those).
func UserFacingDenial(d Decision) (string, bool) {
	if d.Outcome != Deny {
		return "", false
	}
	ruleID, ok := FleetRuleIDFromPolicyID(d.MatchedPolicy)
	if !ok {
		return "", false
	}
	return FleetDenialMessage(fleetMetaFor(ruleID)), true
}

// UserMessage is the user-facing text for this denial: FleetDenialMessage
// when a fleet rule denied it, "" otherwise (use Error() there). Matched
// structurally (interface{ UserMessage() string }) by packages that must
// not import cedar, e.g. core/agentgraph's tool dispatch.
func (e *PolicyDeniedError) UserMessage() string {
	if e == nil {
		return ""
	}
	msg, _ := UserFacingDenial(e.Decision)
	return msg
}
