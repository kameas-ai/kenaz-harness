package cedar

import (
	"strings"
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

// SetFleetPolicyMeta records the metadata of the fleet rule set just
// installed (core/fleet's cedar applier calls it after
// ReplaceHarnessSnippets; nil when the snapshot cleared every fleet rule).
// Decisions produced by a fleet rule carry it, so a denial can say whose
// policy blocked the call (FleetDenialMessage).
func (e *Engine) SetFleetPolicyMeta(m *FleetPolicyMeta) {
	if e == nil {
		return
	}
	if m != nil {
		cp := *m
		m = &cp
	}
	e.fleetMeta.Store(m)
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
	if d.Outcome != Deny || !IsFleetDecision(d) {
		return "", false
	}
	return FleetDenialMessage(d.fleetMeta), true
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
