package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// scheduled_budget.go is the per-schedule context budget of a scheduled
// chat's reused conversation (scheduled-chat-context-budget; the
// conversation itself is sessions/0348's).
//
// WHY A SECOND ENTRY POINT NEXT TO THE DIAL. The five-tier dial
// (session_compaction.go) is a property of the USER's chat and is
// measured against the MODEL's window: balanced compacts at 80% of it.
// A schedule that fires every 30 minutes into one conversation pays for
// that history on every fire, so its threshold is a cost decision the
// schedule owns, measured in runs, not in user-assistant pairs. This
// file is that decision; the machinery is the dial's own. It reaches the
// same SessionEngine through CompactionDeps (same engine, same
// CompactionModel resolution, same capability lookup), runs the rolling
// summary through RollingSummarizeWith (the maximal tier's flow with a
// run-boundary tail), and so writes the same "[Rolling summary: ...]"
// row and soft-archives the originals for the same CompactionArchiveDays
// sweep. Nothing here runs a tool: the summariser call is a bare
// completion with no tool catalog (compaction/wiring.LLMCaller).
//
// Only the dispatcher calls it, before a scheduled fire appends its
// prompt. Interactive turns — including ones the user types into the
// schedule's conversation — never reach it and follow the dial as
// before.

// ScheduledBudget is one fire's context policy plus what the dispatcher
// knows about the conversation's runs.
type ScheduledBudget struct {
	// BudgetTokens is the schedule's context_budget_tokens. It is capped
	// at the chat model's context window when that is known.
	BudgetTokens int
	// KeepRecentRuns is how many of the newest runs stay verbatim.
	KeepRecentRuns int
	// MemoryFocus is the user's "what to remember", given to the
	// summariser as extra instructions. Never logged.
	MemoryFocus string
	// RunStartIDs are the session rows earlier fires appended their
	// prompts as (scheduled_chat_run_history.prompt_message_id).
	RunStartIDs []string
	// PendingPrompt is the prompt this fire is about to append: counted
	// toward the budget, never summarised. "" on the retry path, where
	// the prompt is already in the conversation.
	PendingPrompt string
	// Force compacts even under the budget, keeping at least MinKeepRuns
	// runs — the session-full retry.
	Force       bool
	MinKeepRuns int
}

// ScheduledBudgetOutcome reports one budget pass.
type ScheduledBudgetOutcome struct {
	// Compacted is true when a rolling summary was written.
	Compacted bool
	// Attempted is true when the summariser was (or would have been)
	// asked — over budget or forced — whatever the result.
	Attempted bool
	// Result is the engine's report (tokens before/after, kept runs,
	// the pile it summarised).
	Result compaction.RollingResult
	// Cost is the summarisation calls this pass made.
	Cost compaction.CallCostSnapshot
}

// ErrScheduledBudgetUnavailable reports that compaction is not wired on
// this chassis (HARNESS_COMPACTION=off, or no engine).
var ErrScheduledBudgetUnavailable = errors.New("context compaction is not available on this install")

// scheduledBudgetTier is the AggressivenessTier the budget's audit
// events carry, so they are not mistaken for the maximal dial.
const scheduledBudgetTier = "scheduled_budget"

// ScheduledBudgetCompactor runs the per-schedule budget over the chat
// stack's compaction deps. The dispatcher holds it behind a one-method
// interface (core/rpc.ScheduledConversationCompactor).
type ScheduledBudgetCompactor struct {
	deps *CompactionDeps
}

// NewScheduledBudgetCompactor returns nil when deps carry no engine, so
// a chassis without compaction leaves the dispatcher's seam nil rather
// than holding a compactor that always fails.
func NewScheduledBudgetCompactor(deps *CompactionDeps) *ScheduledBudgetCompactor {
	if deps == nil || deps.Engine == nil {
		return nil
	}
	return &ScheduledBudgetCompactor{deps: deps}
}

// CompactForSchedule enforces b on sessionID before a scheduled fire.
// profileID / modelOverride are the run's chat model: the summariser
// fallback when no CompactionModel is configured, and the model whose
// window caps the budget.
//
// Under the budget (and not forced) it returns Attempted=false with no
// call made. A failure is returned with the outcome so far; the caller
// proceeds uncompacted and the session-full path is the safety net.
func (c *ScheduledBudgetCompactor) CompactForSchedule(ctx context.Context, sessionID, profileID, modelOverride string, b ScheduledBudget) (ScheduledBudgetOutcome, error) {
	var out ScheduledBudgetOutcome
	if c == nil || c.deps == nil || c.deps.Engine == nil || compactionDisabledByEnv() {
		return out, ErrScheduledBudgetUnavailable
	}
	active := compaction.ProviderProfileRef{ProviderID: profileID, ModelID: modelOverride}
	budget := b.BudgetTokens
	if c.deps.MaxContextTokens != nil {
		if window, ok := c.deps.MaxContextTokens(active); ok && window > 0 && budget > window {
			budget = window
		}
	}
	if budget <= 0 {
		return out, fmt.Errorf("scheduled budget: no budget")
	}
	model := active
	if c.deps.CompactionModel != nil {
		if ref, ok := c.deps.CompactionModel(); ok && (ref.ProviderID != "" || ref.ModelID != "") {
			model = ref
		}
	}

	// A quarter of the budget is the summary's; the rest holds the kept
	// runs and the new prompt. The summariser is told the summary's
	// share, so the summary cannot itself outgrow the budget.
	summaryShare := budget / 4
	opts := compaction.RollingOptions{
		RunWindow: &compaction.RunWindow{
			StartIDs: b.RunStartIDs,
			Keep:     b.KeepRecentRuns,
			MinKeep:  b.MinKeepRuns,
		},
		PendingTokens:     countPromptTokens(b.PendingPrompt),
		TailBudgetTokens:  budget - summaryShare,
		MaxSummaryTokens:  summaryShare,
		ExtraInstructions: scheduledSummaryInstructions(b.MemoryFocus),
		Tier:              scheduledBudgetTier,
	}
	if !b.Force {
		opts.OnlyIfOverTokens = budget
	}

	tctx, tally := compaction.WithCallCostTally(ctx)
	res, err := c.deps.Engine.RollingSummarizeWith(tctx, sessionID, model, opts)
	out.Result = res
	out.Cost = tally.Snapshot()
	out.Attempted = !res.UnderBudget
	out.Compacted = err == nil && res.SummaryID != ""
	if err != nil {
		logging.L().Warn("chat.scheduled_budget.compaction_failed",
			"session_id", sessionID, "budget", budget, "err", err.Error())
		return out, err
	}
	if out.Compacted {
		logging.L().Info("chat.scheduled_budget.compacted",
			"session_id", sessionID, "budget", budget,
			"tokens_before", res.TokensBefore, "tokens_after", res.TokensAfter,
			"kept_runs", res.KeptRuns, "rolled_messages", res.RolledMessages,
			"pile_tokens", res.PileTokens, "incremental", res.HadPreviousSummary,
			"summary_calls", out.Cost.Calls, "summary_input_tokens", out.Cost.InputTokens,
			"summary_output_tokens", out.Cost.OutputTokens, "summary_cost_usd", out.Cost.TotalUSD,
			"summary_cost_unpriced_calls", out.Cost.Indeterminate)
	}
	return out, nil
}

// scheduledSummaryInstructions frames the summary for a recurring task
// and carries the user's memory focus. The focus is the user's own text;
// it is delimited so the summariser reads it as an instruction about
// what to keep, not as part of the transcript.
func scheduledSummaryInstructions(memoryFocus string) string {
	var b strings.Builder
	b.WriteString(`This conversation is a recurring scheduled task: each run starts with the
same scheduled prompt and the assistant's answer follows. Keep what later
runs will need — results, decisions, open items, values that change from
run to run and how they changed. Drop answers that a later run superseded.`)
	if focus := strings.TrimSpace(memoryFocus); focus != "" {
		b.WriteString("\n\nThe user asked this summary to remember the following. Give it priority:\n<memory_focus>\n")
		b.WriteString(focus)
		b.WriteString("\n</memory_focus>")
	}
	return b.String()
}

// countPromptTokens sizes a pending user turn with the shared tokenizer.
func countPromptTokens(prompt string) int {
	if prompt == "" {
		return 0
	}
	return tokenizer.CountRequestTokens("", []tokenizer.Message{{Role: "user", Content: prompt}})
}

// ContextWindow reports the chat model's context window through the same
// capability lookup the dial and CompactForSchedule use, so the budget a
// schedule editor validates against is the ceiling CompactForSchedule
// applies. ok=false when unknown or when c is nil.
func (c *ScheduledBudgetCompactor) ContextWindow(profileID, model string) (int, bool) {
	if c == nil || c.deps == nil || c.deps.MaxContextTokens == nil {
		return 0, false
	}
	return c.deps.MaxContextTokens(compaction.ProviderProfileRef{ProviderID: profileID, ModelID: model})
}
