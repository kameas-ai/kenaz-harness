package eval

import (
	"fmt"
	"strings"

	corecompaction "github.com/sigil-tech/kaneaz-harness/core/compaction"
)

// StrategyOverride is a parsed key=value strategy dial override.
//
// Keys follow a dotted hierarchy: <domain>.<parameter>, e.g.:
//   - compaction.tier   — one of off|conservative|balanced|aggressive|maximal
//   - memory.enabled    — "true" | "false"
//   - branching.auto    — "true" | "false"
type StrategyOverride struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ParseStrategyOverride parses a single "key=value" override string into a
// StrategyOverride. Returns an error if the string is not in key=value form.
// Does not validate the key or value — validation happens in ApplyStrategyOverrides.
func ParseStrategyOverride(s string) (StrategyOverride, error) {
	idx := strings.IndexByte(s, '=')
	if idx < 1 || idx == len(s)-1 {
		return StrategyOverride{}, fmt.Errorf("eval/strategy: override %q: expected key=value", s)
	}
	return StrategyOverride{
		Key:   strings.TrimSpace(s[:idx]),
		Value: strings.TrimSpace(s[idx+1:]),
	}, nil
}

// ParseStrategyOverrides parses multiple "key=value" strings. The flag can be
// specified multiple times on the CLI, e.g.:
//
//	--strategy-override compaction.tier=aggressive
//	--strategy-override memory.enabled=false
func ParseStrategyOverrides(entries []string) ([]StrategyOverride, error) {
	out := make([]StrategyOverride, 0, len(entries))
	for _, e := range entries {
		so, err := ParseStrategyOverride(e)
		if err != nil {
			return nil, err
		}
		out = append(out, so)
	}
	return out, nil
}

// AppliedStrategy carries the resolved values after applying all overrides.
// Downstream code reads these fields to configure its subsystems.
type AppliedStrategy struct {
	// Compaction is the effective compaction aggressiveness tier.
	Compaction corecompaction.CompactionAggressiveness
	// MemoryEnabled controls whether the memory subsystem hooks are active.
	MemoryEnabled bool
	// BranchingAuto controls whether the branch-advisor auto-suggestion is active.
	BranchingAuto bool
}

// DefaultAppliedStrategy returns the production defaults for an AppliedStrategy.
func DefaultAppliedStrategy() AppliedStrategy {
	return AppliedStrategy{
		Compaction:    corecompaction.AggressivenessBalanced,
		MemoryEnabled: true,
		BranchingAuto: true,
	}
}

// ApplyStrategyOverrides materializes an AppliedStrategy by starting with defaults
// and applying each override in order. Unknown keys are silently ignored; the
// caller should validate keys before invoking if strict mode is desired.
func ApplyStrategyOverrides(defaults AppliedStrategy, overrides []StrategyOverride) (AppliedStrategy, error) {
	result := defaults
	for _, so := range overrides {
		if err := applyOne(&result, so); err != nil {
			return result, err
		}
	}
	return result, nil
}

func applyOne(s *AppliedStrategy, so StrategyOverride) error {
	switch so.Key {
	case "compaction.tier":
		tier := corecompaction.CompactionAggressiveness(so.Value)
		// Validate against known tiers.
		switch tier {
		case corecompaction.AggressivenessOff,
			corecompaction.AggressivenessConservative,
			corecompaction.AggressivenessBalanced,
			corecompaction.AggressivenessAggressive,
			corecompaction.AggressivenessMaximal:
			s.Compaction = tier
		default:
			return fmt.Errorf(
				"eval/strategy: compaction.tier=%q: unknown tier (valid: off|conservative|balanced|aggressive|maximal)",
				so.Value,
			)
		}

	case "memory.enabled":
		switch so.Value {
		case "true", "1":
			s.MemoryEnabled = true
		case "false", "0":
			s.MemoryEnabled = false
		default:
			return fmt.Errorf("eval/strategy: memory.enabled=%q: expected true|false", so.Value)
		}

	case "branching.auto":
		switch so.Value {
		case "true", "1":
			s.BranchingAuto = true
		case "false", "0":
			s.BranchingAuto = false
		default:
			return fmt.Errorf("eval/strategy: branching.auto=%q: expected true|false", so.Value)
		}

	default:
		// Unknown keys are silently skipped; future keys can be added without
		// breaking older binaries.
	}
	return nil
}
