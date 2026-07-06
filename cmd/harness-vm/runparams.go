// runparams.go — the additive, backward-compatible `run_params` half of the
// kenaz.harness.run-control wire contract (ADR-agent-run-model §5, Spec 056).
//
// task.start widens from {task_id, prompt} to {task_id, prompt, run_params?}
// where run_params carries archetype-derived run shaping — ALL optional. An
// ABSENT run_params object reproduces today's exact behaviour (regression-safe):
// parseRunParams returns hasParams=false and the task dispatch path is unchanged.
//
// Phase-1 mapping onto existing harness machinery:
//
//   - workflow_preset — the one sub-key with a live consumer in the in-VM graph
//     runner. When set, it names a preset from core/workflows/builtin/*.yaml; the
//     resolved step sequence drives the graph's node sequence (graphrun.go), so
//     the selection is observable on the ledger trail (Spec 056 AC-5).
//   - autonomy_tier — validated against core/autonomy.ParseTier and threaded
//     through, but the in-VM graph path has no live session to bind it to (no
//     session.Spec / autonomy resolver in this headless runner), so it is
//     plumbed-not-enforced in Phase 1. See CONCERNS.
//   - system_prompt_ref / review_rigor / policy_strictness — threaded through as
//     opaque strings; no consumer exists in the in-VM graph runner yet (their
//     Cedar/workflow knobs live in the full session engine, not this path), so
//     they are plumbed-not-enforced in Phase 1. See CONCERNS.
//
// PRIVACY: run_params carries only structural selectors (preset names, tier
// labels, refs) — never prompt text, message bodies, or diffs. Nothing here
// reaches the ledger or audit sink beyond the existing metadata-only surface.
package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/workflows"
)

// RunParams is the typed form of the optional task.start `run_params` object.
// All fields are optional; the zero value means "no run shaping" (today's
// behaviour). JSON sub-keys are snake_case per the wire contract.
type RunParams struct {
	WorkflowPreset   string // → core/workflows/builtin preset selection
	SystemPromptRef  string // → session.Spec system-prompt (no in-VM consumer yet)
	AutonomyTier     string // → core/autonomy.Tier (validated; no in-VM consumer yet)
	ReviewRigor      string // → Cedar/workflow review gate (no in-VM consumer yet)
	PolicyStrictness string // → Cedar posture (no in-VM consumer yet)
}

// isZero reports whether no run-shaping field is set.
func (p RunParams) isZero() bool { return p == RunParams{} }

// parseRunParams reads an optional `run_params` object off a task.start frame.
// The second return is false when the frame carries no run_params (or it is not
// an object) — the caller then reproduces today's exact dispatch behaviour.
func parseRunParams(m msg) (RunParams, bool) {
	raw, ok := m["run_params"].(map[string]any)
	if !ok {
		return RunParams{}, false
	}
	return RunParams{
		WorkflowPreset:   strFromMap(raw, "workflow_preset"),
		SystemPromptRef:  strFromMap(raw, "system_prompt_ref"),
		AutonomyTier:     strFromMap(raw, "autonomy_tier"),
		ReviewRigor:      strFromMap(raw, "review_rigor"),
		PolicyStrictness: strFromMap(raw, "policy_strictness"),
	}, true
}

// strFromMap returns the string at key, or "" when absent / not a string.
func strFromMap(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// validateRunParams checks the sub-keys that have a validatable domain. It
// returns a non-empty, human-readable reason when a value is malformed (the
// caller maps it onto a task.error{code:"bad_request"}). An empty return means
// the params are acceptable. Absent/empty sub-keys are always acceptable
// (optional). Only autonomy_tier has a closed enum today; the remaining
// selectors are opaque strings resolved (or ignored) downstream.
func validateRunParams(p RunParams) string {
	if p.AutonomyTier != "" {
		if _, err := autonomy.ParseTier(p.AutonomyTier); err != nil {
			return fmt.Sprintf("invalid autonomy_tier: %q", p.AutonomyTier)
		}
	}
	return ""
}

// ---- workflow preset resolution --------------------------------------------

var (
	presetOnce  sync.Once
	presetIndex map[string][]string // key: lowercased preset id OR name → step names
)

// loadPresetIndex builds the preset lookup once from the embedded builtin
// workflow catalog. Both the workflow id and its display name are indexed
// (lowercased) so callers can select by either. Load errors on individual
// files are ignored here — a preset that fails to parse simply isn't selectable
// (resolveWorkflowPreset returns ok=false → the caller emits unknown_preset).
func loadPresetIndex() {
	presetIndex = map[string][]string{}
	wfs, _ := workflows.LoadBuiltins()
	for _, w := range wfs {
		steps := make([]string, 0, len(w.Steps))
		for _, s := range w.Steps {
			steps = append(steps, s.Name)
		}
		if w.ID != "" {
			presetIndex[strings.ToLower(w.ID)] = steps
		}
		if w.Name != "" {
			presetIndex[strings.ToLower(strings.TrimSpace(w.Name))] = steps
		}
	}
}

// resolveWorkflowPreset maps a preset selector (id or display name,
// case-insensitive) onto its ordered step-name sequence. ok=false when no
// builtin preset matches.
func resolveWorkflowPreset(name string) ([]string, bool) {
	presetOnce.Do(loadPresetIndex)
	steps, ok := presetIndex[strings.ToLower(strings.TrimSpace(name))]
	return steps, ok
}
