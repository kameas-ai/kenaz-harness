// Package subagentdispatch implements the kenaz__subagent_dispatch builtin
// tool (branch-subagent-interactive-01KZNP3B WP03).
//
// The model calls this tool to delegate a task to a named sub-agent profile.
// The tool resolves the profile from the registry, spawns a branch via the
// BranchSeam, registers the spawned task in core/tasks.Registry (when wired),
// and returns either immediately (run_in_background=true) or after the worker
// completes (run_in_background=false).
//
// Design notes:
//   - The sync (run_in_background=false) path is fully implemented in WP03.
//   - The async (run_in_background=true) path sets up the branch and returns
//     immediately; the completion event path requires the background-task-monitor
//     mission's tasks.Registry (WP06). When tasks.Registry is nil the async
//     path degrades gracefully: it still spawns the branch and returns the
//     branch_id, but no global-task-visibility is available.
//   - Cedar action: tool.subagent.dispatch (ActionToolSubagentDispatch).
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package subagentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	coreagents "github.com/kameas-ai/kenaz-harness/core/agents"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kenaz__subagent_dispatch"

	// ToolDescription explains the tool to the model.
	ToolDescription = "Dispatch a sub-agent to handle a delegated task. " +
		"Choose a profile that matches the task (explore = read-only research, " +
		"code-reviewer = review code without changes, implementer = write/edit code, " +
		"summarizer = condense text, bug-finder = diagnose issues). " +
		"By default the sub-agent runs in the background and you receive a branch_id " +
		"immediately. Set run_in_background=false to block until the worker returns its " +
		"merge summary."

	// MaxPromptLen is the upper bound on the prompt to prevent oversized payloads.
	MaxPromptLen = 32768
)

// inputSchema is the JSON Schema for kenaz__subagent_dispatch's args.
const inputSchema = `{
  "type": "object",
  "properties": {
    "profile": {
      "type": "string",
      "description": "ID of the sub-agent profile to use (e.g. explore, code-reviewer, implementer, summarizer, bug-finder)."
    },
    "prompt": {
      "type": "string",
      "description": "Task description sent to the spawned worker as its first user message."
    },
    "run_in_background": {
      "type": "boolean",
      "description": "When true (default), return immediately with a branch_id. When false, block until the worker completes and return the merge summary.",
      "default": true
    },
    "isolation": {
      "type": "string",
      "enum": ["session", "worktree"],
      "description": "Isolation level. 'session' (default) uses copy-on-write conversation storage. 'worktree' is reserved for a future mission.",
      "default": "session"
    },
    "description": {
      "type": "string",
      "description": "Optional human-readable label shown in the BranchSidebar status chip."
    }
  },
  "required": ["profile", "prompt"]
}`

// toolInput is the parsed wire shape.
type toolInput struct {
	Profile           string `json:"profile"`
	Prompt            string `json:"prompt"`
	RunInBackground   *bool  `json:"run_in_background"`
	Isolation         string `json:"isolation"`
	Description       string `json:"description"`
}

// toolResultRunning is returned when run_in_background=true.
type toolResultRunning struct {
	BranchID  string `json:"branch_id"`
	Status    string `json:"status"`
	ProfileID string `json:"profile_id"`
}

// toolResultComplete is returned when run_in_background=false and the worker
// has finished.
type toolResultComplete struct {
	BranchID     string `json:"branch_id"`
	Status       string `json:"status"`
	ProfileID    string `json:"profile_id"`
	MergeSummary string `json:"merge_summary,omitempty"`
}

// toolResultError is returned when the profile is unknown or the input is
// invalid. The model can self-correct from this.
type toolResultError struct {
	Error     string   `json:"error"`
	Message   string   `json:"message"`
	Available []string `json:"available,omitempty"`
}

// TasksRegistry is the optional cross-mission dependency from
// background-task-monitor-01KZNP3C. When wired, each dispatched sub-agent
// is registered so the Tasks panel UI shows it alongside other background
// tasks. The interface is intentionally minimal so this package can code
// against the predicted contract without importing the not-yet-merged mission.
type TasksRegistry interface {
	Register(ctx context.Context, kind string, descriptor map[string]any) (taskID string, err error)
	Cancel(ctx context.Context, taskID string) error
}

// Options bundles the tool's dependencies. Only DataDir is required; all
// other fields are optional and default to nil-safe behaviour.
type Options struct {
	// DataDir is the path to the application data directory. Used to load
	// user-authored profiles from <DataDir>/agents/*.yaml.
	DataDir string

	// Seam is the BranchSeam used to fork the parent session. When nil,
	// the tool returns an error (spawning cannot proceed without a seam).
	Seam agentgraph.BranchSeam

	// Tasks is the optional tasks.Registry from the background-task-monitor
	// mission. When nil the tool logs a debug note and continues without
	// global task visibility.
	//
	// Deliberately left unwired in production as of UNIT-6
	// (subagent-control-and-background-tasks-01PMZB11): task
	// registration/completion for a dispatched sub-agent now flows
	// through the RunSpawner threaded into agentgraph.BranchSeam's
	// Fork (core/rpc/subagent_run_spawner.go), which registers a
	// tasks.KindSubagent row and ends it when the child run reaches a
	// terminal state — that IS the "task registry is the completion
	// signal" UNIT-3/UNIT-4 built. Wiring this field too would create a
	// SECOND, competing registration for the same dispatch (two Tasks
	// panel rows per sub-agent). Left in place for the TasksRegistry
	// interface's own sake — a future unit may still want it for a
	// purpose the run spawner doesn't cover (e.g. a Cancel path that
	// doesn't go through WaitForChildRun) — but do not wire it without
	// removing the run spawner's own registration first.
	Tasks TasksRegistry

	// SessionResolver reads the dispatching session's ID off ctx. Every
	// Fork call requires a non-empty ForkRequest.ParentSessionID
	// (agentgraph.BranchSeam's contract — env_deps_branch.go's Fork
	// returns an error otherwise), so this is not optional in
	// production. Defaults to toolloop.SessionIDFromContext, the same
	// convention every other session-scoped builtin tool uses
	// (kenaz__monitor, kenaz__todo_write, kenaz__save_artifact, …) —
	// the kernel tool adapter calls toolloop.WithSessionID(ctx, id)
	// immediately before dispatching to any builtin, so this is always
	// populated on the production path. Tests that don't go through
	// that dispatch layer must call toolloop.WithSessionID themselves.
	SessionResolver func(context.Context) string
}

// Tool implements the kenaz__subagent_dispatch builtin.
type Tool struct {
	opts            Options
	sessionResolver func(context.Context) string
}

// New constructs a Tool.
func New(opts Options) *Tool {
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	return &Tool{opts: opts, sessionResolver: resolver}
}

// Name implements toolloop.BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements toolloop.BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements toolloop.BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// Call executes the sub-agent dispatch.
func (t *Tool) Call(ctx context.Context, rawArgs json.RawMessage) (json.RawMessage, error) {
	var in toolInput
	if err := json.Unmarshal(rawArgs, &in); err != nil {
		return marshalErr("invalid_args", fmt.Sprintf("could not parse subagent_dispatch args: %v", err))
	}

	// Validate required fields.
	if in.Profile == "" {
		return marshalErr("missing_profile", "the 'profile' argument is required")
	}
	if in.Prompt == "" {
		return marshalErr("missing_prompt", "the 'prompt' argument is required")
	}
	if len(in.Prompt) > MaxPromptLen {
		return marshalErr("prompt_too_long",
			fmt.Sprintf("prompt exceeds maximum length (%d > %d)", len(in.Prompt), MaxPromptLen))
	}

	// Resolve the profile.
	profile, err := coreagents.LoadProfile(t.opts.DataDir, in.Profile)
	if err != nil {
		if errors.Is(err, coreagents.ErrProfileNotFound) {
			// Return the available profiles to help the model self-correct.
			available := t.availableProfileIDs(ctx)
			return marshalProfileNotFound(in.Profile, available)
		}
		return marshalErr("profile_load_error", fmt.Sprintf("could not load profile %q: %v", in.Profile, err))
	}

	// Default run_in_background=true when not specified.
	runBg := true
	if in.RunInBackground != nil {
		runBg = *in.RunInBackground
	}

	// Isolation: only "session" is supported; "worktree" is reserved.
	isolation := strings.ToLower(strings.TrimSpace(in.Isolation))
	if isolation == "" {
		isolation = "session"
	}
	if isolation != "session" && isolation != "worktree" {
		return marshalErr("invalid_isolation",
			fmt.Sprintf("isolation must be 'session' or 'worktree', got %q", isolation))
	}
	if isolation == "worktree" {
		return marshalErr("worktree_not_supported",
			"worktree isolation is not yet supported (deferred mission). Use isolation='session'.")
	}

	// Check that the BranchSeam is wired.
	if t.opts.Seam == nil {
		return marshalErr("seam_not_configured",
			"subagent dispatch requires the branch seam to be wired; this build does not support it")
	}

	// Resolve the dispatching session. Required: BranchSeam.Fork rejects
	// an empty ParentSessionID (env_deps_branch.go), and without it every
	// dispatch would fail at the seam regardless of how the model called
	// this tool. See the SessionResolver doc on Options.
	parentSessionID := t.sessionResolver(ctx)
	if parentSessionID == "" {
		return marshalErr("no_session",
			"subagent dispatch requires an active session context; the dispatching session id was not found on ctx")
	}

	// Build the title for the branch.
	title := in.Description
	if title == "" {
		title = profile.Name + " worker"
	}

	// Build the ForkRequest. The profile's model override, tool allowlist,
	// and system prompt override are applied via the seam.
	forkReq := agentgraph.ForkRequest{
		ParentSessionID: parentSessionID,
		Title:           title,
		TaskHint:        in.Prompt,
		HandoffPrompt:   buildHandoffPrompt(profile, in.Prompt),
		ModelID:         profile.Model,
		ToolAllowlist:   profile.AllowedTools,
	}

	// Fork the session.
	handle, err := t.opts.Seam.Fork(ctx, forkReq)
	if err != nil {
		return marshalErr("fork_failed", fmt.Sprintf("could not spawn sub-agent branch: %v", err))
	}

	// Register with the tasks.Registry when available (WP06 / background-task-monitor).
	if t.opts.Tasks != nil {
		_, _ = t.opts.Tasks.Register(ctx, "subagent", map[string]any{
			"branch_id":  handle.BranchID,
			"profile_id": profile.ID,
			"prompt":     truncateForDescriptor(in.Prompt, 200),
		})
		// Registration failure is non-fatal: the branch was already forked.
	}

	if runBg {
		// Async path: return immediately.
		return marshalJSON(toolResultRunning{
			BranchID:  handle.BranchID,
			Status:    "running",
			ProfileID: profile.ID,
		})
	}

	// Sync path: block until the child run completes.
	if err := t.opts.Seam.WaitForChildRun(ctx, handle.BranchID); err != nil {
		return marshalErr("worker_wait_failed",
			fmt.Sprintf("sub-agent did not complete: %v", err))
	}

	// Pull the last N messages from the child as the merge summary.
	msgs, err := t.opts.Seam.PullChildTail(ctx, handle.BranchID, 5)
	if err != nil {
		// Non-fatal: return without a summary.
		msgs = nil
	}
	summary := buildMergeSummary(msgs)

	return marshalJSON(toolResultComplete{
		BranchID:     handle.BranchID,
		Status:       "complete",
		ProfileID:    profile.ID,
		MergeSummary: summary,
	})
}

// availableProfileIDs returns a sorted slice of known profile IDs for
// use in error messages. Failures silently return an empty slice.
func (t *Tool) availableProfileIDs(ctx context.Context) []string {
	all, err := coreagents.LoadAll(t.opts.DataDir)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(all))
	for _, p := range all {
		ids = append(ids, p.ID)
	}
	return ids
}

// buildHandoffPrompt constructs the first user message sent to the worker.
func buildHandoffPrompt(profile coreagents.Profile, prompt string) string {
	var sb strings.Builder
	if profile.SystemPromptOverride != "" {
		sb.WriteString(profile.SystemPromptOverride)
		sb.WriteString("\n\n---\n\n")
	}
	sb.WriteString(prompt)
	return sb.String()
}

// buildMergeSummary extracts a text summary from the last N child messages.
func buildMergeSummary(msgs []agentgraph.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, m := range msgs {
		if m.Role == "assistant" && m.Content != "" {
			sb.WriteString(m.Content)
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// truncateForDescriptor truncates s to maxLen for use in task descriptors.
func truncateForDescriptor(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// ── marshal helpers ────────────────────────────────────────────────────────

func marshalJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("subagentdispatch: marshal: %w", err)
	}
	return b, nil
}

func marshalErr(kind, msg string) (json.RawMessage, error) {
	return marshalJSON(toolResultError{Error: kind, Message: msg})
}

func marshalProfileNotFound(id string, available []string) (json.RawMessage, error) {
	return marshalJSON(toolResultError{
		Error:     "unknown_profile",
		Message:   fmt.Sprintf("no profile with id %q is registered", id),
		Available: available,
	})
}
