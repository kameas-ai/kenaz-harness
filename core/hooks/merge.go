// merge.go — MergeOutputs folds N hook outputs into a single decision.
//
// Merge semantics (hooks-event-surface-expansion-01KZNP3A plan §merge):
//
//   - Decision: "block" dominates "approve"; first block reason wins.
//   - PermissionDecision: "deny" dominates "allow"; first deny reason wins.
//   - AdditionalContext: all non-empty values joined with "\n\n".
//   - UpdatedInput: transforms compose left-to-right; last non-nil wins
//     (each hook operates on the output of the preceding one).
//   - UpdatedMCPOutput: same left-to-right composition as UpdatedInput.
//   - WatchPaths: union of all paths (deduped, order preserved).
//   - Async outputs are excluded from the sync merge entirely.
package hooks

import (
	"encoding/json"
	"strings"
)

// MergedOutput is the collapsed result of running MergeOutputs over the
// slice returned by a Registry.Fire call. Pipeline integrators consume
// MergedOutput rather than iterating the raw slice themselves.
type MergedOutput struct {
	// Blocked is true when at least one hook returned decision="block".
	Blocked bool
	// BlockReason is the first block reason encountered.
	BlockReason string

	// AdditionalContext is the concatenation of all non-empty
	// additional_context fields, joined with "\n\n".
	AdditionalContext string

	// UpdatedInput is the final rewritten tool-call input after all
	// transforms are composed left-to-right. Nil when no hook provided
	// an updated_input.
	UpdatedInput json.RawMessage

	// UpdatedMCPOutput is the final rewritten MCP tool output after all
	// transforms. Nil when no hook provided an updated_mcp_tool_output.
	UpdatedMCPOutput json.RawMessage

	// PermissionDenied is true when at least one hook returned
	// permission_decision="deny".
	PermissionDenied bool
	// PermissionAllowed is true when at least one hook returned
	// permission_decision="allow" and no hook returned "deny".
	PermissionAllowed bool
	// PermissionReason is the first non-empty permission_decision_reason.
	PermissionReason string

	// WatchPaths is the deduped union of watch_paths from all hook outputs.
	WatchPaths []string
}

// MergeOutputs folds a slice of HookOutputs into a single MergedOutput.
// It is a pure function (no side effects) and is total (never panics on
// empty input).
//
// Async outputs (where Async==true) are excluded: they were fire-and-
// forget and their stdout is logged separately — merging them would let
// slow async hooks accidentally mutate the sync pipeline.
func MergeOutputs(outputs []HookOutput) MergedOutput {
	var merged MergedOutput
	var ctxParts []string
	seen := map[string]struct{}{}

	for _, o := range outputs {
		// Async outputs are excluded from the sync merge.
		if o.Async {
			continue
		}

		// Decision: block dominates.
		if o.Decision == "block" && !merged.Blocked {
			merged.Blocked = true
			merged.BlockReason = o.Reason
		}

		// AdditionalContext: accumulate.
		if o.AdditionalContext != "" {
			ctxParts = append(ctxParts, o.AdditionalContext)
		}

		// UpdatedInput: left-to-right composition (last non-nil wins when
		// treating each hook output as a transform of the previous state).
		if len(o.UpdatedInput) > 0 {
			merged.UpdatedInput = o.UpdatedInput
		}

		// UpdatedMCPOutput: same composition.
		if len(o.UpdatedMCPOutput) > 0 {
			merged.UpdatedMCPOutput = o.UpdatedMCPOutput
		}

		// Collect permission decisions for post-loop resolution (deny dominates).
		switch o.PermissionDecision {
		case "deny":
			if !merged.PermissionDenied {
				merged.PermissionDenied = true
				if o.PermissionDecisionReason != "" {
					merged.PermissionReason = o.PermissionDecisionReason
				}
			}
		case "allow":
			// Record allow; will be cleared if any deny is found.
			if o.PermissionDecisionReason != "" && merged.PermissionReason == "" {
				merged.PermissionReason = o.PermissionDecisionReason
			}
			merged.PermissionAllowed = true
		}

		// WatchPaths: deduped union.
		for _, p := range o.WatchPaths {
			if p == "" {
				continue
			}
			if _, dup := seen[p]; !dup {
				seen[p] = struct{}{}
				merged.WatchPaths = append(merged.WatchPaths, p)
			}
		}
	}

	merged.AdditionalContext = strings.Join(ctxParts, "\n\n")

	// deny dominates allow: if any hook denied, clear the allow flag and
	// ensure the reason reflects the deny, not an earlier allow reason.
	if merged.PermissionDenied {
		merged.PermissionAllowed = false
		// PermissionReason was already set to the deny reason when we saw the
		// first deny; no action needed here.
	}

	return merged
}
