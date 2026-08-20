package agentgraph

import "github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"

// init registers every ModelAttrs field with core/wiring/knobcoverage
// (structured-output-is-reachable-01PMZE14 WP03, spec §7 G-1). This is
// the general mechanism scripts/ci/check-knob-coverage.sh already
// enforces for autonomy.ResolvedKnobs; ModelAttrs never had a
// registration, which is exactly how JsonSchema (spec §1.2) and
// StreamToChat (docs/unwired-ledger.md:1674) both went unread for a
// release cycle before being found by hand. All seventeen fields have a
// real consumer in modelExecutor.Execute (exec_compute.go) — and, for
// FallbackChainId, also in the escalate executor — as of this WP; none
// is deferred.
func init() {
	knobcoverage.Register[ModelAttrs]("FallbackChainId", "agentgraph.modelExecutor.Execute (LLMRequest.FallbackChainId) + escalateExecutor.Execute")
	knobcoverage.Register[ModelAttrs]("FrequencyPenalty", "agentgraph.modelExecutor.Execute (LLMRequest.FrequencyPenalty)")
	knobcoverage.Register[ModelAttrs]("JsonSchema", "agentgraph.modelExecutor.Execute (LLMRequest.ResponseSchema, structured-output-is-reachable-01PMZE14 WP02)")
	knobcoverage.Register[ModelAttrs]("MaxTokens", "agentgraph.modelExecutor.Execute (LLMRequest.MaxTokens)")
	knobcoverage.Register[ModelAttrs]("Model", "agentgraph.modelExecutor.Execute (LLMRequest.Model, prompt/model resolution)")
	knobcoverage.Register[ModelAttrs]("ParallelToolCalls", "agentgraph.modelExecutor.Execute (LLMRequest.ParallelToolCalls)")
	knobcoverage.Register[ModelAttrs]("PresencePenalty", "agentgraph.modelExecutor.Execute (LLMRequest.PresencePenalty)")
	knobcoverage.Register[ModelAttrs]("Provider", "agentgraph.modelExecutor.Execute (LLMRequest.Provider, prompt/model resolution)")
	knobcoverage.Register[ModelAttrs]("ReasoningBudgetTokens", "agentgraph.modelExecutor.Execute (LLMRequest.ReasoningBudgetTokens)")
	knobcoverage.Register[ModelAttrs]("Seed", "agentgraph.modelExecutor.Execute (LLMRequest.Seed)")
	knobcoverage.Register[ModelAttrs]("StopSequences", "agentgraph.modelExecutor.Execute (LLMRequest.StopSequences)")
	knobcoverage.Register[ModelAttrs]("StreamToChat", "agentgraph.modelExecutor.Execute (LLMRequest.StreamToChat, model-moves-transcript-01PMCH01 WP02)")
	knobcoverage.Register[ModelAttrs]("SystemPrompt", "agentgraph.modelExecutor.Execute (composePrompt)")
	knobcoverage.Register[ModelAttrs]("Temperature", "agentgraph.modelExecutor.Execute (LLMRequest.Temperature)")
	knobcoverage.Register[ModelAttrs]("ToolAllowlist", "agentgraph.modelExecutor.Execute (tools slice)")
	knobcoverage.Register[ModelAttrs]("TopK", "agentgraph.modelExecutor.Execute (LLMRequest.TopK)")
	knobcoverage.Register[ModelAttrs]("TopP", "agentgraph.modelExecutor.Execute (LLMRequest.TopP)")
}
