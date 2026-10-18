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
// Four fields are RegisterDeferred rather than Register.
//
// UPDATE (model-settings-reach-the-model-01PMZ101 UNIT-6 / WP10-WP11,
// landed): RequestKnobs' "zero production writers" half of every
// blocker below is now FALSE — Sessions_SetKnobsDefault persists a
// session-level override to sessions.knobs_default, and
// chat.LLMProviderAdapter.Generate merges it onto
// GenerationRequest.Knobs on the send path (core/rpc/views/agentgraph/
// chat/llm_provider_adapter.go's WithKnobsDefault option). Reasoning,
// Seed, Temperature, TopP, MaxTokens, FrequencyPenalty,
// PresencePenalty, ParallelToolCalls and StopSequences were already
// Register-ed (their KnobsToParams/body.go consumer existed all along;
// only the writer was missing) and are now genuinely reachable
// end-to-end. The three still-deferred fields below stay deferred: each
// has a SEPARATE, still-open consumer-side gap that a writer alone does
// not close — flipping them to Register would be the exact "wiring an
// override layer nothing can reach and calling it done" spec §5.5
// rejects, just from the other end (a writer with no reader instead of
// a reader with no writer). Per CLAUDE.md: "a justification names the
// blocker and the owner — the change that will delete the line."
func init() {
	knobcoverage.Register[llm.RequestKnobs]("Reasoning", "core/llm/openaiwire/body.go (reasoning_effort, OpenAI-wire adapters)")
	knobcoverage.Register[llm.RequestKnobs]("Seed", "core/llm/openaiwire/base.go KnobsToParams (seed)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("ResponseFormatMode", "blocker: the production writer landed (model-settings-reach-the-model-01PMZ101 UNIT-6, Sessions_SetKnobsDefault) but there is still no consumer — body.go's response-format block reads req.ResponseFormat/req.JSONMode directly (see the WP07 docstring correction, commit 8a603ce8, \"stop claiming body.go applies the response_format knob\"), never Knobs.ResponseFormatMode. Owner: whichever mission wires RequestKnobs.ResponseFormatMode as a real override layer over req.ResponseFormat (structured-output-is-reachable-01PMZE14's family is the natural owner; not scoped to 01PMZ101 UNIT-6, which only had to give RequestKnobs a writer, not audit every field's reader).")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("JSONMode", "blocker: same as ResponseFormatMode — writer landed (01PMZ101 UNIT-6), but body.go reads req.JSONMode directly, never Knobs.JSONMode (commit 8a603ce8). Owner: whichever mission wires RequestKnobs.JSONMode as a real override layer over req.JSONMode.")
	knobcoverage.Register[llm.RequestKnobs]("Temperature", "core/llm/openaiwire/base.go KnobsToParams (temperature)")
	knobcoverage.Register[llm.RequestKnobs]("TopP", "core/llm/openaiwire/base.go KnobsToParams (top_p)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("TopK", "blocker: writer landed (01PMZ101 UNIT-6), but KnobsToParams still does not map top_k, and body.go:52-55 now carries an EXPLICIT, documented reason why: \"OpenAI Chat Completions has no top_k parameter.\" That is true for pure OpenAI but not for every openaiwire-routed kind — OpenRouter and Ollama's OpenAI-compatible endpoints both accept top_k as a passthrough field, so the exclusion is a real gap for those two kinds and a correct one for OpenAI/Azure/custom-openai. This is a per-adapter capability question BuildRequestBody's current shared, kind-agnostic signature cannot express, not a one-line fix — flagging for a product/architecture call rather than picking a side unilaterally. Owner: unassigned; escalate before resolving.")
	knobcoverage.Register[llm.RequestKnobs]("MaxTokens", "core/llm/openaiwire/base.go KnobsToParams (max_tokens)")
	knobcoverage.Register[llm.RequestKnobs]("FrequencyPenalty", "core/llm/openaiwire/base.go KnobsToParams (frequency_penalty)")
	knobcoverage.Register[llm.RequestKnobs]("PresencePenalty", "core/llm/openaiwire/base.go KnobsToParams (presence_penalty)")
	knobcoverage.Register[llm.RequestKnobs]("ParallelToolCalls", "core/llm/openaiwire/body.go (parallel_tool_calls)")
	knobcoverage.Register[llm.RequestKnobs]("StopSequences", "core/llm/openaiwire/base.go KnobsToParams (stop)")
	knobcoverage.RegisterDeferred[llm.RequestKnobs]("VendorExtensions", "blocker: writer landed (01PMZ101 UNIT-6), but no adapter merges VendorExtensions into its wire body today — found by hand while registering this struct for structured-output-is-reachable-01PMZE14 WP07, confirmed still true as of 01PMZ101 UNIT-6. Owner: whichever mission gives at least one openaiwire-routed adapter a VendorExtensions merge step.")
}
