package agentgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// tool_invocation.go — the single tool-invocation path
// (agentgraph-total-convergence-01PMGX01 WP05, spec §3 goal 2, §1.4).
//
// The harness used to invoke tools two ways. The static `tool` kind
// (exec_compute.go) fired the greedy-memory hooks, the v2 pre_tool_use
// and post_tool_use lifecycle hooks, the FR-010 iteration gate and the
// FR-041 post-tool compaction site. `tool_dispatch` (exec_dispatch.go)
// — the ONLY path production has ever run — fired the greedy-memory
// hooks and the iteration gate, and had its own arg-schema validation,
// doom-loop guard, per-call timeout and mutation-safety lock.
//
// Two consequences the split hid, both fixed here:
//
//   - the v2 `pre_tool_use` / `post_tool_use` lifecycle hooks — the
//     surface that lets a user BLOCK a tool call, rewrite its args, or
//     inject context — had ZERO reachable call sites in production.
//     They only ever ran on the `tool` kind, which spec §1.4 documents
//     as dead in production. A user configuring a pre_tool_use hook got
//     silence.
//   - "which policy applies to a tool call" depended on which node kind
//     initiated it, which is exactly the parallel-implementations
//     failure this mission exists to remove.
//
// So the policy that must be uniform for every tool call — hook
// surfaces, the canonical EventToolCall/EventToolResult records, and
// the iteration gate — lives here and is called by both executors. The
// pieces that are genuinely per-shape stay with their executor:
// tool_dispatch owns fan-out concerns (schema validation, doom-loop,
// timeout, mutation lock, panic isolation) because they only mean
// something for a model-emitted batch.
//
// STILL SPLIT, DELIBERATELY, AND OWNED ELSEWHERE: the FR-041 post-tool
// compaction site remains on the node path only. Running a compactor
// once per call inside tool_dispatch's parallel fan-out would impose a
// concurrency contract on every Compactor implementation. Phase 4
// (WP08, spec §4.4) removes the question by making compaction a NODE
// placed in the graph instead of a hidden per-call site, which is the
// convergence that actually resolves it. This is not a "keep the old
// path" carve-out — the old path is gone; what remains is one call site
// that a later WP deletes outright.

// toolCallContext identifies the node and (for model-emitted batches)
// the individual call a tool invocation belongs to.
type toolCallContext struct {
	NodeID   string
	NodeKind NodeKind
	// CallID is the model's tool_use id. Empty for a tool node, whose
	// call is the node itself.
	CallID   string
	ToolName string
}

// eventPayload builds the common {tool, call_id, node_kind} shape the
// EventLog projection reads, regardless of which executor dispatched.
func (tc toolCallContext) eventPayload(extra map[string]any) map[string]any {
	out := map[string]any{"tool": tc.ToolName}
	for k, v := range extra {
		out[k] = v
	}
	if tc.CallID != "" {
		out["call_id"] = tc.CallID
	}
	if tc.NodeKind != "" {
		out["node_kind"] = string(tc.NodeKind)
	}
	return out
}

// toolPreDispatch runs every pre-call step that must be identical for
// all tool invocations:
//
//  1. the greedy-memory HookPreTool listener,
//  2. the canonical EventToolCall record,
//  3. the v2 pre_tool_use lifecycle hooks — which may BLOCK the call,
//     rewrite its args, or append context for the next LLM turn.
//
// It returns the (possibly rewritten) args, their JSON encoding for the
// post-hooks, whether a hook rewrote them, a non-nil ToolResult when a
// hook blocked the call, and the events the caller must fold into its
// own Result.
//
// A hook-runner error is logged and treated as "no opinion" — a broken
// hook must not take the tool call down with it.
func toolPreDispatch(
	ctx context.Context,
	env *Env,
	tc toolCallContext,
	args map[string]any,
) (outArgs map[string]any, argsJSON json.RawMessage, rewritten bool, blocked *ToolResult, events EventBatch) {
	outArgs = args

	if env.Hooks != nil {
		hookBatch := env.Hooks.Fire(ctx, HookPreTool, "session",
			"pre-tool — "+tc.ToolName, summarizeArgs(outArgs), tc.NodeID)
		appendRunScoped(&events, env.RunID, tc.NodeID, hookBatch.Events)
	}

	_ = events.AppendKind(env.RunID, tc.NodeID, EventToolCall,
		tc.eventPayload(map[string]any{"args": outArgs}))

	argsJSON, _ = json.Marshal(outArgs)
	if env.LifecycleHooks == nil {
		return outArgs, argsJSON, false, nil, events
	}

	merged, err := env.LifecycleHooks.FirePreToolUse(ctx, env.SessionID, tc.ToolName, argsJSON, "", "")
	if err != nil {
		logging.L().Warn("agentgraph.tool.pre_tool_use_hook.failed",
			"run_id", env.RunID, "session_id", env.SessionID,
			"node_id", tc.NodeID, "tool", tc.ToolName, "err", err.Error())
		return outArgs, argsJSON, false, nil, events
	}

	if merged.Blocked {
		logging.L().Warn("agentgraph.tool.hook_denied",
			"run_id", env.RunID, "session_id", env.SessionID, "node_id", tc.NodeID,
			"tool", tc.ToolName, "reason", merged.BlockReason)
		_ = events.AppendKind(env.RunID, tc.NodeID, EventHookDenied,
			tc.eventPayload(map[string]any{"reason": merged.BlockReason}))
		return outArgs, argsJSON, false, &ToolResult{
			Content: "hook_denied: " + merged.BlockReason,
			IsError: true,
		}, events
	}

	if len(merged.UpdatedInput) > 0 {
		var rewrittenArgs map[string]any
		if jerr := json.Unmarshal(merged.UpdatedInput, &rewrittenArgs); jerr == nil {
			_ = events.AppendKind(env.RunID, tc.NodeID, EventHookInputRewrite,
				tc.eventPayload(map[string]any{
					"original":  string(argsJSON),
					"rewritten": string(merged.UpdatedInput),
				}))
			outArgs = rewrittenArgs
			argsJSON = merged.UpdatedInput
			rewritten = true
		}
	}

	if merged.AdditionalContext != "" && env.PendingContext != nil {
		_ = env.PendingContext.AppendSystemContext(ctx, env.SessionID, merged.AdditionalContext)
	}
	return outArgs, argsJSON, rewritten, nil, events
}

// chargeToolIteration applies the FR-010 iteration gate: a passive tool
// (kenaz__sleep, and anything else registered through
// toolloop.RegisterPassiveTool) must not consume an iteration slot. The
// `sleep` node kind declares the same contract as `budget: none` in its
// manifest; both halves must agree, so both go through this one call.
func chargeToolIteration(env *Env, toolName string) {
	if env.Counters != nil && toolloop.ShouldCountIteration(toolName) {
		env.Counters.AddTool()
	}
}

// toolPostDispatch runs every post-call step that must be identical for
// all tool invocations: the canonical EventToolResult record and the v2
// post_tool_use (or post_tool_use_failure) lifecycle hooks, including
// the updated_mcp_tool_output rewrite and additional_context injection.
//
// It returns the (possibly hook-rewritten) result plus the events to
// fold into the caller's Result. On the failure path only the
// post_tool_use_failure hook fires — the caller owns how it surfaces
// the failure, because a tool node returns an error while a dispatch
// fan-out converts it into a model-readable is_error result.
// extra carries caller-supplied fields merged into the EventToolResult
// payload (integration 2026-08-12: the runway tool-output cap threads its
// truncation telemetry — truncated / bytes_original / bytes_elided /
// output_handle / archive_err — through here so exactly one
// EventToolResult is emitted per call). Nil means no extra fields.
func toolPostDispatch(
	ctx context.Context,
	env *Env,
	tc toolCallContext,
	argsJSON json.RawMessage,
	tr ToolResult,
	callErr error,
	extra map[string]any,
) (ToolResult, EventBatch) {
	var events EventBatch

	if callErr != nil {
		if env.LifecycleHooks != nil {
			_, _ = env.LifecycleHooks.FirePostToolUse(ctx, env.SessionID, tc.ToolName,
				argsJSON, nil, true, callErr.Error(), "", "")
		}
		return tr, events
	}

	resultPayload := map[string]any{
		"is_error": tr.IsError,
		"bytes":    len(tr.Content),
	}
	for k, v := range extra {
		resultPayload[k] = v
	}
	_ = events.AppendKind(env.RunID, tc.NodeID, EventToolResult,
		tc.eventPayload(resultPayload))
	appendFSToolProvenance(&events, env.RunID, tc, argsJSON, tr)

	if env.LifecycleHooks == nil {
		return tr, events
	}
	outJSON, _ := json.Marshal(map[string]any{
		"content":  tr.Content,
		"is_error": tr.IsError,
	})
	merged, herr := env.LifecycleHooks.FirePostToolUse(ctx, env.SessionID, tc.ToolName,
		argsJSON, outJSON, false, "", "", "")
	if herr != nil {
		logging.L().Warn("agentgraph.tool.post_tool_use_hook.failed",
			"run_id", env.RunID, "session_id", env.SessionID,
			"node_id", tc.NodeID, "tool", tc.ToolName, "err", herr.Error())
		return tr, events
	}
	// updated_mcp_tool_output is MCP-only in practice; applying it
	// unconditionally is harmless because non-MCP hooks never set it.
	if len(merged.UpdatedMCPOutput) > 0 {
		var updatedContent string
		if jerr := json.Unmarshal(merged.UpdatedMCPOutput, &updatedContent); jerr == nil {
			tr.Content = updatedContent
		}
	}
	if merged.AdditionalContext != "" && env.PendingContext != nil {
		_ = env.PendingContext.AppendSystemContext(ctx, env.SessionID, merged.AdditionalContext)
	}
	return tr, events
}

// fsToolProvenanceKinds names the builtin filesystem tools (core/tools/
// fsbuiltins) that mirror a State `read` / `write` node kind, and what
// each one should record when it succeeds.
//
// agentgraph-total-convergence-01PMGX01 WP13 deletes the "State-vs-Tool
// framing" doctrine (docs/agent-kernel-graph-node-catalog.md §4.9): the
// prose told graph authors to pick `read_file`/`write_file` over
// `kenaz__read_file`/`kenaz__write_file` when they wanted the result
// "remembered" — provenance (path/sha256/mtime) recorded to the
// EventLog. That was a real behavioural difference, which is exactly
// why it was doctrine instead of an accident: two mechanisms, one
// arbitrated by prose.
//
// The two candidate collapses were (a) move the three State kinds onto
// the `_archetype.tool.yaml` dispatch model added by WP04, so
// `read_file` calls `kenaz__read_file` instead of its own os.ReadFile,
// or (b) make both surfaces record the same provenance so the
// distinction the doctrine arbitrated no longer exists. (a) was
// rejected: `kenaz__read_file`/`kenaz__write_file` gate through
// core/tools/fs.Gate's interactive-prompt Cedar family
// (ActionReadFilesystem/ActionWriteFilesystem on FilesystemOp::"..."),
// while the State kinds gate through the deny-only
// ActionFileRead/ActionFileWrite + FR-058b ActionStateRead/
// ActionStateWrite pair (core/policy/cedar/hooks.go CheckFileRead/
// CheckStateRead). Retargeting the node's dispatch would silently swap
// which Cedar action family — and which permission UX — governs an
// existing graph, which spec §5 forbids without an explicit behaviour
// change. So (b): this file is the one place BOTH mechanisms already
// funnel every tool call through (toolPreDispatch/toolPostDispatch,
// shared by tool_dispatch and the `tool`-archetype node executor per
// the WP05 header comment above) — appendFSToolProvenance hooks the
// existing convergence point rather than adding a third.
var fsToolProvenanceKinds = map[string]struct {
	event  EventKind
	action string
}{
	"kenaz__read_file":  {EventFileRead, "Read::file"},
	"kenaz__write_file": {EventFileWrite, "Write::file"},
}

// appendFSToolProvenance emits a file_read/file_write EventLog record —
// the same EventKind and payload shape (path, sha256, mtime, size_bytes,
// state_action) the read_file/write_file State kinds record in
// exec_state.go — for a successful kenaz__read_file / kenaz__write_file
// tool call, regardless of whether the call arrived via a model-emitted
// tool_dispatch batch or a future `tool`-archetype node. It is
// best-effort: a stat/hash failure here must not fail an otherwise
// successful tool call, so errors are swallowed and no event is
// emitted.
func appendFSToolProvenance(events *EventBatch, runID string, tc toolCallContext, argsJSON json.RawMessage, tr ToolResult) {
	spec, ok := fsToolProvenanceKinds[tc.ToolName]
	if !ok || tr.IsError {
		return
	}
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(argsJSON, &a); err != nil || a.Path == "" {
		return
	}
	// filepath.Abs resolves a relative path against the process CWD —
	// the same base core/tools/fs.Canonicalize uses (filepath.Abs +
	// filepath.Clean) — so this lands on the same file the gate
	// evaluated, even though we don't have the gate's canonical string
	// at this layer.
	abs, err := filepath.Abs(a.Path)
	if err != nil {
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		return
	}
	raw, err := os.ReadFile(abs) //nolint:gosec // provenance hash; path is the tool's own already-gated argument
	if err != nil {
		return
	}
	hash := sha256.Sum256(raw)
	_ = events.AppendKind(runID, tc.NodeID, spec.event, map[string]any{
		"path":         abs,
		"sha256":       hex.EncodeToString(hash[:]),
		"mtime":        info.ModTime().UTC().Format(time.RFC3339Nano),
		"size_bytes":   info.Size(),
		"state_action": spec.action,
		"tool":         tc.ToolName,
	})
}

// appendRunScoped copies hook-emitted events onto a batch, stamping the
// run + node identity the hook manager does not know.
func appendRunScoped(dst *EventBatch, runID, nodeID string, src []Event) {
	for _, e := range src {
		if e.RunID == "" {
			e.RunID = runID
		}
		if e.NodeID == "" {
			e.NodeID = nodeID
		}
		dst.Append(e)
	}
}
