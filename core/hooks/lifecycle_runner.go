// lifecycle_runner.go — LifecycleRunnerAdapter wires core/hooks.Runner
// onto the agentgraph.LifecycleHookRunner seam.
//
// The adapter is the production binding injected by the wiring layer
// (core/rpc). Tests can substitute a fake implementing the interface.
package hooks

import (
	"context"
	"encoding/json"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// Compile-time assertion: LifecycleRunnerAdapter implements
// agentgraph.LifecycleHookRunner.
var _ agentgraph.LifecycleHookRunner = (*LifecycleRunnerAdapter)(nil)

// LifecycleRunnerAdapter wraps a *Runner and implements the
// agentgraph.LifecycleHookRunner interface.
type LifecycleRunnerAdapter struct {
	Runner *Runner
}

// FirePreToolUse fires pre_tool_use hooks and returns a merged result.
func (a *LifecycleRunnerAdapter) FirePreToolUse(
	ctx context.Context,
	sessionID, toolName string,
	inputJSON json.RawMessage,
	model, kind string,
) (agentgraph.LifecycleMergedOutput, error) {
	if a == nil || a.Runner == nil {
		return agentgraph.LifecycleMergedOutput{}, nil
	}
	outputs, err := a.Runner.Fire(ctx, EventPreToolUse, PreToolUseEvent{
		SessionID: sessionID,
		ToolName:  toolName,
		Input:     inputJSON,
		Model:     model,
		Kind:      kind,
	})
	if err != nil {
		return agentgraph.LifecycleMergedOutput{}, err
	}
	merged := MergeOutputs(outputs)
	return agentgraph.LifecycleMergedOutput{
		Blocked:           merged.Blocked,
		BlockReason:       merged.BlockReason,
		AdditionalContext: merged.AdditionalContext,
		UpdatedInput:      merged.UpdatedInput,
		UpdatedMCPOutput:  merged.UpdatedMCPOutput,
	}, nil
}

// FirePostToolUse fires post_tool_use or post_tool_use_failure hooks.
func (a *LifecycleRunnerAdapter) FirePostToolUse(
	ctx context.Context,
	sessionID, toolName string,
	inputJSON, outputJSON json.RawMessage,
	isFailure bool,
	errMsg, model, kind string,
) (agentgraph.LifecycleMergedOutput, error) {
	if a == nil || a.Runner == nil {
		return agentgraph.LifecycleMergedOutput{}, nil
	}
	var outputs []HookOutput
	var err error
	if isFailure {
		outputs, err = a.Runner.Fire(ctx, EventPostToolUseFailure, PostToolUseFailureEvent{
			SessionID: sessionID,
			ToolName:  toolName,
			Input:     inputJSON,
			Error:     errMsg,
			Model:     model,
			Kind:      kind,
		})
	} else {
		outputs, err = a.Runner.Fire(ctx, EventPostToolUse, PostToolUseEvent{
			SessionID: sessionID,
			ToolName:  toolName,
			Input:     inputJSON,
			Output:    outputJSON,
			Model:     model,
			Kind:      kind,
		})
	}
	if err != nil {
		return agentgraph.LifecycleMergedOutput{}, err
	}
	merged := MergeOutputs(outputs)
	return agentgraph.LifecycleMergedOutput{
		Blocked:           merged.Blocked,
		BlockReason:       merged.BlockReason,
		AdditionalContext: merged.AdditionalContext,
		UpdatedInput:      merged.UpdatedInput,
		UpdatedMCPOutput:  merged.UpdatedMCPOutput,
	}, nil
}
