package openaiwire

import (
	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// init registers every llm.RequestKnobs field with
// core/wiring/knobcoverage (structured-output-is-reachable-01PMZE14
// WP07, spec §7 G-2). RequestKnobs never had a registration; base.go's
// docstring claimed response_format was "applied explicitly" by
// body.go when in fact neither ResponseFormatMode nor JSONMode is read
// anywhere — a docstring asserting a wire that does not exist,
// CLAUDE.md's unwired-sweep failure mode by name (spec §1.5).
//
// This package (core/llm/openaiwire) is the natural home: it is the
// one production consumer of *llm.RequestKnobs (KnobsToParams, plus
// the reasoning_effort/parallel_tool_calls block in body.go), so the
// registrations travel with the code that either does or does not read
// each field.
//
// Four fields are RegisterDeferred rather than Register. Each reason
// names the blocker (RequestKnobs has ZERO production writers at all —
// nothing ever sets req.Knobs to a non-nil value; the chat seam's
// llm_provider_adapter.go and every adapter that reads sampling knobs
// wire per-node values onto the untyped Params map instead, per
// registry.go's KnobPolicy comment) and the owner (model-settings-
// reach-the-model-01PMZ101 UNIT-6 / WP10-WP11, which lands
// Sessions_SetKnobsDefault — the first and only planned production
// writer). Per CLAUDE.md: "a justification names the blocker and the
// owner — the change that will delete the line." Wiring an override
// layer above req.ResponseFormat/req.JSONMode/etc. that nothing can
// reach until Z101 UNIT-6 lands would be "wiring an unreachable
// override and calling it done," which spec §5.5 explicitly rejects as
// not a fix.
func init() {
	knobcoverage.Register[llm.RequestKnobs]("Reasoning", "core/llm/openaiwire/body.go (reasoning_effort, OpenAI-wire adapters)")
	knobcoverage.Register[llm.RequestKnobs]("Seed", "core/llm/openaiwire/base.go KnobsToParams (seed)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("ResponseFormatMode", "blocker: RequestKnobs has no production writer until model-settings-reach-the-model-01PMZ101 UNIT-6 (WP10/WP11, Sessions_SetKnobsDefault) lands; owner: 01PMZ101; wiring an override layer nothing can reach is not a fix (spec §5.5). Re-check at the release after 01PMZ101 UNIT-6 merges.")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("JSONMode", "blocker: RequestKnobs has no production writer until model-settings-reach-the-model-01PMZ101 UNIT-6 (WP10/WP11, Sessions_SetKnobsDefault) lands; owner: 01PMZ101; wiring an override layer nothing can reach is not a fix (spec §5.5). Re-check at the release after 01PMZ101 UNIT-6 merges.")
	knobcoverage.Register[llm.RequestKnobs]("Temperature", "core/llm/openaiwire/base.go KnobsToParams (temperature)")
	knobcoverage.Register[llm.RequestKnobs]("TopP", "core/llm/openaiwire/base.go KnobsToParams (top_p)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("TopK", "blocker: same as ResponseFormatMode (no production writer for RequestKnobs at all) — additionally, KnobsToParams has never mapped top_k even though the sibling fields (top_p, temperature) are wired; found by hand while registering this struct for WP07, not itself a structured-output finding. Owner: model-settings-reach-the-model-01PMZ101 (whichever WP gives Knobs a writer should also close this). Re-check at the release after 01PMZ101 UNIT-6 merges.")
	knobcoverage.Register[llm.RequestKnobs]("MaxTokens", "core/llm/openaiwire/base.go KnobsToParams (max_tokens)")
	knobcoverage.Register[llm.RequestKnobs]("FrequencyPenalty", "core/llm/openaiwire/base.go KnobsToParams (frequency_penalty)")
	knobcoverage.Register[llm.RequestKnobs]("PresencePenalty", "core/llm/openaiwire/base.go KnobsToParams (presence_penalty)")
	knobcoverage.Register[llm.RequestKnobs]("ParallelToolCalls", "core/llm/openaiwire/body.go (parallel_tool_calls)")
	knobcoverage.Register[llm.RequestKnobs]("StopSequences", "core/llm/openaiwire/base.go KnobsToParams (stop)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("VendorExtensions", "blocker: same as ResponseFormatMode (no production writer for RequestKnobs at all) — additionally, no adapter merges VendorExtensions into its wire body today, found by hand while registering this struct for WP07. Owner: model-settings-reach-the-model-01PMZ101 (whichever WP gives Knobs a writer should also close this). Re-check at the release after 01PMZ101 UNIT-6 merges.")
}
