// Package fleet — cedar_apply.go
//
// CedarBundleApplier installs the fleet-distributed Cedar policy snapshot
// into the local Cedar engine. The bundle's cedar_delta carries Cedar source
// keyed by a team_rule_id; each rule is installed under the snippet name
// "fleet-team/<team_rule_id>" (PolicyIDs "harness-self/fleet-team/<id>#<n>").
//
// Snapshot semantics (fleet-policy push-down, contract #1, 2026-10-31):
// despite its name, cedar_delta is the org's COMPLETE current fleet rule
// set. Every apply REPLACES all previously-installed fleet-team rules
// atomically (Engine.ReplaceHarnessSnippets builds a fresh PolicySet and
// swaps it in). An absent/null cedar_delta, or one with no rules, removes
// every fleet-installed rule. An unparseable delta is an apply error and
// the previous fleet rules stay in force.
//
// Override semantics (spec WP03): a local forbid rule
// `when { context.team_rule_id == "X" }` is meant to cancel team rule "X";
// the frontend Override button writes exactly this statement.
//
// (fleet-config-pull-01NDFSEX10 WP03)
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// fleetTeamSnippetPrefix namespaces every fleet-installed Cedar snippet.
// The constant lives in core/policy/cedar (FleetTeamSnippetPrefix) so a
// decision can be attributed to a FLEET rule (cedar.IsFleetDecision) —
// served-mode bash availability and the "Blocked by your organization's
// Fleet policy" denial copy — without that package importing fleet.
const fleetTeamSnippetPrefix = cedarpolicy.FleetTeamSnippetPrefix

// legacyRuleTextID is the team_rule_id given to a cedar_delta that carries
// only the pre-2026-10-31 Fleet shape {"rule_text": "..."} with no "rules"
// map (see CedarDelta.RuleText).
const legacyRuleTextID = "rule-text"

// CedarDelta is the decoded form of Bundle.CedarDelta.
//
// Wire shape:
//
//	{
//	  "rules": { "<team_rule_id>": "<cedar source>" },
//	  "rule_text": "<cedar source>",       // legacy; ignored when rules is set
//	  "published_at": "<RFC3339>"          // informational; ignored
//	}
//
// Rules is the full snapshot (the union of its entries is the org's fleet
// rule set). RuleText is the shape Fleet emitted before it added Rules; a
// bundle carrying only rule_text is applied as the single rule
// "rule-text" so it is enforced rather than ACKed as applied and dropped.
type CedarDelta struct {
	// Rules maps team_rule_id → Cedar policy source (one or more statements).
	Rules map[string]string `json:"rules"`
	// RuleText is the legacy single-rule field; used only when Rules is empty.
	RuleText string `json:"rule_text,omitempty"`
}

// snapshot returns the rule set this delta installs.
func (d *CedarDelta) snapshot() map[string]string {
	if d == nil {
		return nil
	}
	if len(d.Rules) > 0 {
		return d.Rules
	}
	if strings.TrimSpace(d.RuleText) != "" {
		return map[string]string{legacyRuleTextID: d.RuleText}
	}
	return nil
}

// Sanitized Cedar apply failure categories. An apply error carries one of
// these plus the team_rule_id, NEVER the parser message: cedar-go's
// messages can quote rule text, and the error reaches lastError (served
// status), logs, and the ACK.
const (
	CedarErrInvalidCedar   = "invalid_cedar"   // a rule failed to parse
	CedarErrMalformedDelta = "malformed_delta" // cedar_delta JSON did not decode
	CedarErrEmptyRuleID    = "empty_rule_id"   // a rules entry had an empty key
	CedarErrNoEngine       = "no_engine"       // no Cedar engine wired
	CedarErrInstall        = "install_failed"  // engine refused the snapshot
)

// CedarApplyError is a sanitized cedar_delta apply failure.
type CedarApplyError struct {
	RuleID   string // team_rule_id, "" when not rule-specific
	Category string // one of the CedarErr* constants
}

func (e *CedarApplyError) Error() string {
	if e.RuleID != "" {
		return fmt.Sprintf("cedar apply: rule %q: %s", e.RuleID, e.Category)
	}
	return "cedar apply: " + e.Category
}

// CedarBundleApplier applies fleet-distributed Cedar policy snapshots to a
// cedar.Engine.
type CedarBundleApplier struct {
	engine *cedarpolicy.Engine
}

// NewCedarBundleApplier constructs a CedarBundleApplier backed by engine.
func NewCedarBundleApplier(engine *cedarpolicy.Engine) *CedarBundleApplier {
	return &CedarBundleApplier{engine: engine}
}

// SetTeamBundle replaces every installed fleet-team rule with delta's
// snapshot, all-or-nothing: a rule that fails to parse aborts the whole
// apply and the previous fleet rules stay installed. A nil/empty delta
// removes every fleet-team rule.
func (a *CedarBundleApplier) SetTeamBundle(ctx context.Context, delta *CedarDelta) error {
	if a.engine == nil {
		return &CedarApplyError{Category: CedarErrNoEngine}
	}
	rules := delta.snapshot()
	snippets := make(map[string][]byte, len(rules))
	for ruleID, src := range rules {
		if ruleID == "" {
			return &CedarApplyError{Category: CedarErrEmptyRuleID}
		}
		snippets[fleetTeamSnippetPrefix+ruleID] = []byte(src)
	}
	if err := a.engine.ReplaceHarnessSnippets(fleetTeamSnippetPrefix, snippets); err != nil {
		var pe *cedarpolicy.SnippetParseError
		if errors.As(err, &pe) {
			return &CedarApplyError{RuleID: strings.TrimPrefix(pe.Name, fleetTeamSnippetPrefix), Category: CedarErrInvalidCedar}
		}
		return &CedarApplyError{Category: CedarErrInstall}
	}
	_ = ctx
	return nil
}

// ApplyCedarDelta decodes the raw JSON delta from a Bundle and installs it as
// the fleet rule snapshot. An empty or JSON-null rawDelta clears every
// fleet-installed rule. A delta that does not decode is an error and leaves
// the previous rules in force.
func ApplyCedarDelta(ctx context.Context, engine *cedarpolicy.Engine, rawDelta json.RawMessage) error {
	var delta CedarDelta
	if len(rawDelta) > 0 {
		if err := json.Unmarshal(rawDelta, &delta); err != nil {
			return &CedarApplyError{Category: CedarErrMalformedDelta}
		}
	}
	return NewCedarBundleApplier(engine).SetTeamBundle(ctx, &delta)
}

// ActiveFleetCedarRules returns the team_rule_ids currently installed from
// fleet bundles, sorted (nil engine → nil).
func ActiveFleetCedarRules(engine *cedarpolicy.Engine) []string {
	if engine == nil {
		return nil
	}
	names := engine.HarnessSnippetNames(fleetTeamSnippetPrefix)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.TrimPrefix(n, fleetTeamSnippetPrefix))
	}
	return out
}
