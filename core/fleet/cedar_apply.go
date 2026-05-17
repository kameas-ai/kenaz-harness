// Package fleet — cedar_apply.go
//
// CedarBundleApplier merges fleet-distributed Cedar policy deltas into the
// local Cedar engine. The delta carries Cedar policy source bytes (raw .cedar
// text) keyed by a team_rule_id. Each team rule is installed under a
// "fleet-team/<team_rule_id>" PolicyID namespace so local forbid overrides can
// cancel individual team rules.
//
// Override semantics (spec WP03):
//
//	Local forbid rule `when { context.team_rule_id == "X" }` cancels the
//	team-distributed permit rule for rule-id "X". The frontend Override button
//	writes exactly this forbid statement into the user's local policy dir.
//
// The apply is atomic from Cedar's perspective: a fresh PolicySet is built
// by merging the existing set with the incoming delta; it is installed via
// atomic.Pointer.Store so in-flight Evaluate calls see either the old or
// the new bundle, never a torn state.
//
// (fleet-config-pull-01NDFSEX10 WP03)
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	cedarpolicy "github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// CedarDelta is the decoded form of Bundle.CedarDelta.
//
// Wire shape (subset the fleet server may populate):
//
//	{
//	  "rules": {
//	    "<team_rule_id>": "<cedar source bytes as string>"
//	  }
//	}
//
// Each rule entry is a complete Cedar permit/forbid statement. The team_rule_id
// is an opaque server-assigned identifier used as the PolicyID prefix so the
// frontend override mechanism can address individual rules.
type CedarDelta struct {
	// Rules maps team_rule_id → Cedar policy source (one or more statements).
	Rules map[string]string `json:"rules"`
}

// CedarBundleApplier applies fleet-distributed Cedar policy deltas to a
// cedar.Engine. Satisfies the ConfigApplier interface when combined with the
// other section appliers in a composite applier.
type CedarBundleApplier struct {
	engine *cedarpolicy.Engine
}

// NewCedarBundleApplier constructs a CedarBundleApplier backed by engine.
func NewCedarBundleApplier(engine *cedarpolicy.Engine) *CedarBundleApplier {
	return &CedarBundleApplier{engine: engine}
}

// SetTeamBundle parses delta and installs each team rule into the Cedar engine
// under the "fleet-team/<team_rule_id>" PolicyID namespace.
//
// Errors from individual rule parse failures are collected and returned as a
// joined error; rules that parse successfully are still installed (partial
// apply, not all-or-nothing).
func (a *CedarBundleApplier) SetTeamBundle(ctx context.Context, delta *CedarDelta) error {
	if a.engine == nil {
		return errors.New("cedar apply: engine is nil")
	}
	if delta == nil || len(delta.Rules) == 0 {
		return nil
	}

	snippets := make(map[string][]byte, len(delta.Rules))
	for ruleID, src := range delta.Rules {
		name := fmt.Sprintf("fleet-team/%s", ruleID)
		snippets[name] = []byte(src)
	}

	// LoadHarnessSnippets is the existing mechanism that adds named policy
	// sources to the active PolicySet without a full reload. Re-using it
	// avoids duplicating the atomic-store / file-list logic.
	if err := a.engine.LoadHarnessSnippets(snippets); err != nil {
		return fmt.Errorf("cedar apply: install team bundle: %w", err)
	}
	_ = ctx
	return nil
}

// ApplyCedarDelta decodes the raw JSON delta from a Bundle and delegates to
// SetTeamBundle. Returns nil when CedarDelta is nil or empty (not all bundles
// carry a Cedar update).
func ApplyCedarDelta(ctx context.Context, engine *cedarpolicy.Engine, rawDelta json.RawMessage) error {
	if len(rawDelta) == 0 {
		return nil
	}
	var delta CedarDelta
	if err := json.Unmarshal(rawDelta, &delta); err != nil {
		return fmt.Errorf("cedar apply: decode delta: %w", err)
	}
	applier := NewCedarBundleApplier(engine)
	return applier.SetTeamBundle(ctx, &delta)
}

// TeamRuleIDFromPolicyID extracts the team_rule_id from a PolicyID with the
// "fleet-team/" prefix. Returns ("", false) when the ID does not carry the
// fleet-team prefix.
func TeamRuleIDFromPolicyID(id cedar.PolicyID) (string, bool) {
	const prefix = "fleet-team/"
	s := string(id)
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}
