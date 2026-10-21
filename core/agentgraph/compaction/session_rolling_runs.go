package compaction

import (
	"fmt"
	"strings"
)

// This file is part of the PERSISTED-HISTORY layer (see session_rolling.go).
//
// session_rolling_runs.go holds the options RollingSummarizeWith takes on
// top of the maximal tier's user-pair window. They exist for ONE caller
// today, the per-schedule context budget of a scheduled chat's reused
// conversation (scheduled-chat-context-budget, core/rpc/views/agentgraph/
// chat/scheduled_budget.go), and they extend the rolling flow rather than
// fork it: the same span selection, tool-pair clamp, previous-summary
// fold, pre-flight cap check, single transactional ApplyCompaction (so the
// rolled-in originals are soft-archived and later purged by the existing
// CompactionArchiveDays sweep) and the same "[Rolling summary: ...]" row.
//
// What the options change:
//
//   - RunWindow measures the live tail in RUNS instead of user-assistant
//     pairs. A run opens at a user row the caller names (a scheduled
//     fire's prompt); every row after it up to the next run start belongs
//     to that run — including turns the user typed into the conversation
//     between fires. Those interactive turns are therefore kept when they
//     fall inside the kept runs and folded into the summary when they are
//     older. See findRunWindowStart for the legacy-row rule.
//   - OnlyIfOverTokens makes the pass conditional: under the caller's
//     budget, nothing is summarised (and no call is made).
//   - TailBudgetTokens shrinks the kept runs when the newest ones alone
//     would not fit the budget — the budget is the hard target, the run
//     count is the preference.
//   - ExtraInstructions and MaxSummaryTokens extend the prompt: what the
//     user said to remember, and a length target so the rolling summary
//     cannot itself grow past the budget.

// RollingOptions configures RollingSummarizeWith. The zero value plus a
// RecentWindow is exactly RollingSummarize.
type RollingOptions struct {
	// RecentWindow is the user-assistant pair count the maximal tier
	// keeps verbatim. Ignored when RunWindow is set.
	RecentWindow int

	// RunWindow, when non-nil, keeps the last RunWindow.Keep runs
	// verbatim instead of the last RecentWindow pairs.
	RunWindow *RunWindow

	// OnlyIfOverTokens > 0 makes the pass a no-op (RollingResult.
	// UnderBudget) unless the active history plus PendingTokens exceeds
	// it.
	OnlyIfOverTokens int

	// PendingTokens is the size of a turn about to be appended (a
	// scheduled fire's rendered prompt): counted toward OnlyIfOverTokens
	// and TailBudgetTokens, never summarised.
	PendingTokens int

	// TailBudgetTokens > 0 caps the verbatim tail: when the last
	// RunWindow.Keep runs plus PendingTokens exceed it, fewer runs are
	// kept (down to RunWindow.MinKeep). Run-window only.
	TailBudgetTokens int

	// MaxSummaryTokens > 0 asks the summariser to keep the new summary
	// under roughly that many tokens (stated to it in words).
	MaxSummaryTokens int

	// ExtraInstructions is appended to the summariser prompt verbatim,
	// before the pile. Caller-composed; the engine never logs it.
	ExtraInstructions string

	// Tier is the AggressivenessTier the audit payloads carry. Empty is
	// "maximal", the tier RollingSummarize serves.
	Tier string
}

// RunWindow is the run-boundary option of RollingOptions.
type RunWindow struct {
	// StartIDs are the ids of the user rows that open a run.
	StartIDs []string
	// Keep is how many of the newest runs stay verbatim. 0 folds every
	// run into the summary.
	Keep int
	// MinKeep is the floor TailBudgetTokens may shrink Keep to.
	MinKeep int
}

// RollingResult reports what RollingSummarizeWith did. SummaryID is ""
// on every no-op path.
type RollingResult struct {
	SummaryID string
	// UnderBudget: OnlyIfOverTokens was not exceeded; nothing ran.
	UnderBudget bool
	// TokensBefore / TokensAfter are the active history's size before
	// the pass and after it (the new summary plus the kept tail). Equal
	// on a no-op. Neither includes PendingTokens.
	TokensBefore int
	TokensAfter  int
	// RolledMessages is how many rows (other than a previous summary)
	// were folded into the new summary.
	RolledMessages int
	// KeptRuns is how many runs the tail holds (run window only).
	KeptRuns int
	// PileTokens is the size of what the summariser was asked to
	// compress: previous summary plus newly aged rows.
	PileTokens int
	// HadPreviousSummary reports that the pile began with the previous
	// rolling summary (an incremental update, not a first roll).
	HadPreviousSummary bool
}

// findRunWindowStart returns the index of the first message of the
// newest `keep` runs, and how many runs that tail actually holds.
//
// A run starts at a user row whose id is in startIDs. A user row OLDER
// than every named run start also counts as one: those are rows written
// before the caller began naming its runs (a conversation that predates
// the column the ids come from), and treating each as a run start is the
// same reading the user-pair window would give them. A user row AFTER
// the oldest named start that is not itself named is an interactive turn
// and belongs to the run it follows.
//
// keep <= 0 returns len(messages): no tail, everything is fold-eligible.
// Fewer runs than keep returns 0: everything is tail.
func findRunWindowStart(messages []SessionMessage, startIDs map[string]bool, keep int) (int, int) {
	if keep <= 0 {
		return len(messages), 0
	}
	oldestNamed := len(messages)
	for i, m := range messages {
		if startIDs[m.ID] {
			oldestNamed = i
			break
		}
	}
	runs := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != "user" {
			continue
		}
		if !startIDs[m.ID] && i >= oldestNamed {
			continue // an interactive turn inside a run
		}
		runs++
		if runs == keep {
			return i, runs
		}
	}
	return 0, runs
}

// runWindowTailStart applies RunWindow and, when set, TailBudgetTokens:
// the largest kept-run count in [MinKeep, Keep] whose tail plus the
// pending turn fits the tail budget. When none fits, MinKeep's tail is
// returned — the caller then proceeds over budget rather than dropping a
// run it was told to keep.
func (e *engine) runWindowTailStart(messages []SessionMessage, w RunWindow, tailBudget, pending int) (int, int) {
	ids := make(map[string]bool, len(w.StartIDs))
	for _, id := range w.StartIDs {
		if id != "" {
			ids[id] = true
		}
	}
	keep := w.Keep
	if keep < 0 {
		keep = 0
	}
	minKeep := w.MinKeep
	if minKeep < 0 {
		minKeep = 0
	}
	if minKeep > keep {
		keep = minKeep
	}
	start, kept := findRunWindowStart(messages, ids, keep)
	if tailBudget <= 0 {
		return start, kept
	}
	for k := keep; k >= minKeep; k-- {
		start, kept = findRunWindowStart(messages, ids, k)
		if e.countMessages(messages[start:])+pending <= tailBudget {
			return start, kept
		}
	}
	return start, kept
}

// countMessages is the engine tokenizer over a message slice (empty
// system slot), the count the budget is measured in.
func (e *engine) countMessages(msgs []SessionMessage) int {
	if len(msgs) == 0 {
		return 0
	}
	tm := make([]TokenizerMessage, 0, len(msgs))
	for _, m := range msgs {
		tm = append(tm, TokenizerMessage{Role: m.Role, Content: m.Content})
	}
	return e.tokenizer.Count("", tm)
}

// rollingSummaryLengthTemplate states MaxSummaryTokens to the summariser
// in words (roughly 0.75 words per token).
const rollingSummaryLengthTemplate = `Keep the updated summary under about %d words. When it would be longer,
drop the least useful detail, not the most recent.`

// buildRollingPrompt is the rolling summariser's user prompt. With no
// options it is byte-identical to the maximal tier's locked prompt.
func buildRollingPrompt(pileText string, opts RollingOptions) string {
	if opts.ExtraInstructions == "" && opts.MaxSummaryTokens <= 0 {
		return fmt.Sprintf(rollingSummaryUserPromptTemplate, pileText)
	}
	locked := fmt.Sprintf(rollingSummaryUserPromptTemplate, pileText)
	// Insert the additions before the pile so the transcript stays the
	// last thing the model reads.
	cut := strings.Index(locked, "<rolling_pile>")
	var b strings.Builder
	b.WriteString(strings.TrimRight(locked[:cut], "\n"))
	if opts.MaxSummaryTokens > 0 {
		words := opts.MaxSummaryTokens * 3 / 4
		if words < 1 {
			words = 1
		}
		b.WriteString("\n\n")
		fmt.Fprintf(&b, rollingSummaryLengthTemplate, words)
	}
	if opts.ExtraInstructions != "" {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(opts.ExtraInstructions))
	}
	b.WriteString("\n\n")
	b.WriteString(locked[cut:])
	return b.String()
}
