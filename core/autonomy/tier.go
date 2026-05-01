// Package autonomy implements the agentic-behavior dial: a 5-tier user-facing
// preset (Strict→Autonomous) that maps onto seven independently-tunable knobs,
// with a three-level override hierarchy (global → project → session).
//
// The package is a leaf — it has no dependencies on any other core/* package.
// Resolution is per-knob and follows the rule: "Override beats Level within a
// layer; session beats project beats global beats tier-default across layers."
package autonomy

import (
	"fmt"
	"strings"
)

// Tier is the user-facing autonomy preset. Higher tier == more aggressive
// auto-proceeding; lower tier == more confirmation prompts and tighter caps.
type Tier int

const (
	// TierStrict (0): minimum iterations, asks on every ambiguity, no
	// auto-approval of any tool family.
	TierStrict Tier = iota
	// TierCautious (1): small iteration budget, asks on hard calls, only
	// read ops auto-approve.
	TierCautious
	// TierDefault (2): default tier; balanced defaults — read+write
	// auto-approve, retry once on tool error.
	TierDefault
	// TierBold (3): generous iteration budget, proceed on minor ambiguity,
	// shell-safe ops auto-approve.
	TierBold
	// TierAutonomous (4): unbounded iterations, never asks, all four canonical
	// tool families auto-approve. Cedar deny remains the floor.
	TierAutonomous
)

// String returns the canonical lowercase tier name.
func (t Tier) String() string {
	switch t {
	case TierStrict:
		return "strict"
	case TierCautious:
		return "cautious"
	case TierDefault:
		return "default"
	case TierBold:
		return "bold"
	case TierAutonomous:
		return "autonomous"
	default:
		return fmt.Sprintf("tier(%d)", int(t))
	}
}

// ParseTier parses a tier name (case-insensitive) back into a Tier.
// Returns an error on unknown names.
func ParseTier(s string) (Tier, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return TierStrict, nil
	case "cautious":
		return TierCautious, nil
	case "default":
		return TierDefault, nil
	case "bold":
		return TierBold, nil
	case "autonomous":
		return TierAutonomous, nil
	default:
		return 0, fmt.Errorf("autonomy: unknown tier %q", s)
	}
}
