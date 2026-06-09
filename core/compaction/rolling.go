package compaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// rolling.go houses the maximal-mode rolling-summary path (plan §2.5).
// Where Compact (engine.go) is a percent-of-tokens threshold flow that
// fires occasionally, RollingSummarize is invoked every turn by the
// chat runner when the user picks the AggressivenessMaximal tier. It
// collapses everything past the recent-window tail into a single
// rolling-summary system row that itself REPLACES any previous rolling
// summary on the next tick.
//
// The path reuses the WP03 collaborators verbatim — same MessageStore
// (ApplyCompaction handles the transactional archive+insert), same
// LLMCaller, same CapabilityLookup, same audit emitter. The differences
// from Compact are:
//
//   - Span selection is "everything older than the last N user-
//     assistant pairs" rather than a percent of total tokens.
//   - The pile prepends the previous rolling summary (if any) before
//     re-summarizing — so the compaction model sees both the prior
//     condensed history and the new turns in one prompt and produces a
//     fresh single-block summary that subsumes both.
//   - The previous rolling summary row is itself archived in the same
//     transaction (it gets folded into the new one), so a session
//     never carries more than one active rolling-summary row.
//   - The summary content is wrapped in a DIFFERENT canonical
//     prefix/suffix ("[Rolling summary: ...]") so future code can
//     disambiguate rolling vs. threshold-mode summaries by content
//     prefix without an extra column.
//
// All other invariants (tool-pair preservation, cap pre-flight, audit
// payload shape, atomic persistence) match Compact.

// rollingSummaryContentPrefix wraps the maximal-mode rolling summary
// output. The prefix is intentionally distinct from
// summaryContentPrefix ("[Earlier conversation summary: ") so the chat
// runner / frontend / sweep can tell the two summary kinds apart by
// content sniff alone.
const rollingSummaryContentPrefix = "[Rolling summary: "
const rollingSummaryContentSuffix = "]"

// rollingMaximalTier is the value the success/failure audit payloads
// carry in their AggressivenessTier field for rolling runs. Centralized
// so the constant doesn't drift across emit sites.
const rollingMaximalTier = "maximal"

// rollingSummaryUserPromptTemplate is the locked prompt body adapted
// from the threshold-mode prompt (engine.go) for the rolling-summary
// flow. The key behavioral difference is the "you are maintaining a
// running summary" framing: the model is told the conversation
// continues and that this same prompt will run again on every turn,
// each time given the prior summary plus a few new turns. Output is
// still a single block of plain text.
const rollingSummaryUserPromptTemplate = `You are maintaining a running summary of an ongoing conversation.
The conversation continues; new turns will be added on every interaction.
Update this summary to incorporate the latest turns. Preserve facts,
decisions, tool inputs and outputs, file paths, and any identifiers
verbatim. Compress aggressively; remove pleasantries and filler. Do not
invent. Do not editorialize. Output a single block of plain text.

<rolling_pile>
%s
</rolling_pile>`

// RollingSummarize implements the rolling-summary algorithm from plan
// §2.5. See the file-level comment for design rationale.
func (e *engine) RollingSummarize(ctx context.Context, sessionID string,
	model ProviderProfileRef, recentWindow int) (string, error) {
	// Guard: a non-positive window means "everything is recent" so
	// there's nothing to roll. We treat this as a clean no-op rather
	// than an error — the chat runner can pass a defaulted-to-zero
	// window during early bring-up without a special case here.
	if recentWindow <= 0 {
		e.emitRollingNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// Step 1: Load active messages.
	messages, err := e.store.ListActiveMessages(ctx, sessionID)
	if err != nil {
		e.emitRollingFailedAudit(ctx, sessionID, model, 0, "store_error")
		return "", err
	}
	if len(messages) == 0 {
		// Empty session — nothing to roll. No I/O follow-up needed.
		e.emitRollingNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// Step 2: Identify the recent-window tail. tailStart is the index
	// of the first message that belongs to the live tail; everything
	// strictly before tailStart is fold-into-summary territory.
	tailStart := findRecentWindowStart(messages, recentWindow)

	// Step 3: Tool-pair clamp. If tailStart lands inside a
	// tool_use/tool_result pair, push it forward (toward higher
	// indices) so the pair stays whole on the live-tail side. This is
	// the same direction snapBoundaryForToolPairs uses for the
	// threshold flow — the helper is symmetrical to the boundary
	// semantic ("first index NOT in the older-side span").
	tailStart = snapBoundaryForToolPairs(messages, tailStart)

	// Step 4: Identify the previous rolling summary (if any). By
	// construction it sits at the head of active-messages (lowest
	// sequence, distinguished content prefix). We accept the row at
	// any index < tailStart since a future implementation might shift
	// it; in practice index 0 covers all cases today.
	prevSummaryID, prevSummaryIdx, prevSummaryContent := findPreviousRollingSummary(messages, tailStart)

	// Step 5: Compute the rolling-pile span. Everything in
	// messages[0:tailStart] gets archived; the prompt-input pile is
	// (previous rolling summary content if any) + (other messages in
	// that range). If there's no previous summary AND no messages in
	// the older-side span, this is a no-op tick.
	if tailStart <= 0 && prevSummaryID == "" {
		// Tail covers everything — nothing to roll.
		e.emitRollingNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// "rolledTurns" = messages slated for archival that are NOT the
	// previous rolling summary itself. These contribute their
	// rendered text to the prompt body alongside the previous
	// summary.
	rolledTurns := make([]Message, 0, tailStart)
	for i := 0; i < tailStart; i++ {
		if prevSummaryID != "" && i == prevSummaryIdx {
			continue
		}
		rolledTurns = append(rolledTurns, messages[i])
	}

	if prevSummaryID == "" && len(rolledTurns) == 0 {
		// Defensive: tailStart > 0 but the only message in the
		// older-side span is somehow the prev summary index without
		// us actually having a prev summary id. Treat as no-op.
		e.emitRollingNoOpAudit(ctx, sessionID, model, 0)
		return "", nil
	}

	// Step 6: Build the rolling-pile transcript and the prompt. The
	// pile starts with the previous rolling summary content (verbatim,
	// including its own prefix wrapper — the model sees the canonical
	// form so it knows what to extend) followed by the freshly-rolled
	// turns rendered the same way Compact renders its transcript.
	pileText := renderRollingPile(prevSummaryContent, rolledTurns)
	userPrompt := fmt.Sprintf(rollingSummaryUserPromptTemplate, pileText)
	systemPrompt := ""

	// Tokens-in-pile is what the audit's TokensInSpan field will
	// carry. We count the pile content (prev summary + rolled turns)
	// rather than the framed prompt because the audit payload's
	// "what did we ask the model to compress" semantics matches the
	// raw pile, not the wire-prompt envelope.
	tokensInPile := e.tokenizePile(prevSummaryContent, rolledTurns)

	// Step 7: Pre-flight cap check. The whole prompt (system + user)
	// must fit under the compaction model's MaxContextTokens budget.
	estimatedPromptTokens := e.tokenizer.Count(systemPrompt, []TokenizerMessage{
		{Role: "user", Content: userPrompt},
	})
	if cap, ok := e.capabilities.MaxContextTokens(model); ok && estimatedPromptTokens > cap {
		err := &ErrCompactionModelTooSmall{
			NeedsTokens:    estimatedPromptTokens,
			ModelMaxTokens: cap,
		}
		e.emitRollingFailedAudit(ctx, sessionID, model, tokensInPile, "model_too_small")
		return "", err
	}

	// Step 8: Run the LLM call.
	text, _, outputTokens, err := e.llm.CallForSummary(ctx, model, systemPrompt, userPrompt)
	if err != nil {
		e.emitRollingFailedAudit(ctx, sessionID, model, tokensInPile, classifyLLMError(err))
		return "", err
	}

	// Step 9: Build the new summary row. Sequence = lowest sequence
	// among archived rows so the summary sits at the head of the
	// surviving history. originalIDs covers the previous rolling
	// summary (if any) + every rolled-in original.
	summaryID, err := newSummaryID()
	if err != nil {
		e.emitRollingFailedAudit(ctx, sessionID, model, tokensInPile, "id_generation_failed")
		return "", err
	}
	now := e.now()

	originalIDs := make([]string, 0, tailStart)
	lowestSeq := messages[0].Sequence
	for i := 0; i < tailStart; i++ {
		originalIDs = append(originalIDs, messages[i].ID)
		if messages[i].Sequence < lowestSeq {
			lowestSeq = messages[i].Sequence
		}
	}

	summary := Message{
		ID:        summaryID,
		Role:      "system",
		Content:   rollingSummaryContentPrefix + text + rollingSummaryContentSuffix,
		Sequence:  lowestSeq,
		CreatedAt: now,
	}

	// Step 10: Persist atomically.
	if err := e.store.ApplyCompaction(ctx, sessionID, summary, originalIDs, now); err != nil {
		e.emitRollingFailedAudit(ctx, sessionID, model, tokensInPile, "persist_error")
		return "", err
	}

	// Step 11: Success audit. compression_ratio = output / pile.
	ratio := 0.0
	if tokensInPile > 0 {
		ratio = float64(outputTokens) / float64(tokensInPile)
	}
	if e.auditEm != nil {
		e.auditEm.Emit(ctx, audit.KindSessionCompacted, audit.SessionCompactedPayload{
			SessionID:          sessionID,
			AggressivenessTier: rollingMaximalTier,
			ModelUsed:          model.String(),
			TokensInSpan:       tokensInPile,
			TokensAfterSummary: outputTokens,
			CompressionRatio:   ratio,
		})
	}

	return summary.ID, nil
}

// findRecentWindowStart returns the index of the first message that
// belongs to the recent-window tail of the last `recentWindow`
// user-assistant pairs. Everything strictly before that index is
// older-side material the rolling summary subsumes.
//
// A "user-assistant pair" begins at a user-role message; the assistant
// reply that immediately follows the user message is part of the same
// pair. We walk the slice from the END backward, decrementing the
// remaining-pairs counter every time we cross a user-role message.
// When the counter reaches zero, the index of that user message is
// the tail-start.
//
// If the slice contains FEWER than `recentWindow` user messages, the
// whole slice is the tail and the function returns 0 (i.e., the
// rolling pile is empty and the caller should no-op).
//
// If recentWindow <= 0 the function returns len(messages) — the tail
// is empty so everything is fold-eligible. The engine's RollingSummarize
// guards that case earlier; the helper still handles it cleanly so
// it's safe in isolation.
func findRecentWindowStart(messages []Message, recentWindow int) int {
	if recentWindow <= 0 {
		return len(messages)
	}
	if len(messages) == 0 {
		return 0
	}

	pairsRemaining := recentWindow
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			pairsRemaining--
			if pairsRemaining == 0 {
				return i
			}
		}
	}
	// Not enough user turns to satisfy the window — entire slice is
	// the tail.
	return 0
}

// findPreviousRollingSummary scans messages[0:tailStart] for the
// previous rolling-summary row (a system-role message whose Content
// starts with rollingSummaryContentPrefix). Returns the id, index, and
// raw content. If none exists, all three zero values are returned.
//
// Convention is "at most one active rolling summary at a time" — the
// algorithm folds the previous one into each new run — so a slice
// should never carry more than one match. If somehow it does, we take
// the first (lowest-index) one.
func findPreviousRollingSummary(messages []Message, tailStart int) (string, int, string) {
	for i := 0; i < tailStart && i < len(messages); i++ {
		m := messages[i]
		if m.Role == "system" && strings.HasPrefix(m.Content, rollingSummaryContentPrefix) {
			return m.ID, i, m.Content
		}
	}
	return "", 0, ""
}

// renderRollingPile concatenates the previous rolling summary content
// (if any) and the freshly-rolled turns into a single transcript-style
// block the rolling-summary prompt embeds between <rolling_pile> tags.
//
// The previous summary is emitted verbatim, on its own line, so the
// model sees the canonical "[Rolling summary: ...]" form and knows
// it's looking at a prior condensed pile rather than a fresh user turn.
// The rolled turns follow in role: content form, mirroring
// renderTranscript in engine.go.
func renderRollingPile(prevSummaryContent string, rolledTurns []Message) string {
	var b strings.Builder
	if prevSummaryContent != "" {
		b.WriteString(prevSummaryContent)
	}
	for i, m := range rolledTurns {
		if b.Len() > 0 || i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// tokenizePile returns the engine's deterministic token count for the
// rolling pile (previous summary content + every rolled-in turn).
// Centralized so the count semantics for audit payloads stays one
// source of truth — the prompt-overhead estimation uses a different
// shape (one big user message) that we want the cap-check to consume,
// but the audit's TokensInSpan / TokensAfterSummary ratio only makes
// sense over the raw pile.
func (e *engine) tokenizePile(prevSummaryContent string, rolledTurns []Message) int {
	msgs := make([]TokenizerMessage, 0, 1+len(rolledTurns))
	if prevSummaryContent != "" {
		msgs = append(msgs, TokenizerMessage{Role: "system", Content: prevSummaryContent})
	}
	for _, m := range rolledTurns {
		msgs = append(msgs, TokenizerMessage{Role: m.Role, Content: m.Content})
	}
	return e.tokenizer.Count("", msgs)
}

// emitRollingNoOpAudit emits a KindSessionCompacted audit with
// AggressivenessTier="maximal" and CompressionRatio=1.0 — the rolling-
// mode mirror of emitNoOpAudit (engine.go). Centralized so the no-op
// return paths above stay consistent.
func (e *engine) emitRollingNoOpAudit(ctx context.Context, sessionID string, model ProviderProfileRef, tokensInPile int) {
	if e.auditEm == nil {
		return
	}
	e.auditEm.Emit(ctx, audit.KindSessionCompacted, audit.SessionCompactedPayload{
		SessionID:          sessionID,
		AggressivenessTier: rollingMaximalTier,
		ModelUsed:          model.String(),
		TokensInSpan:       tokensInPile,
		TokensAfterSummary: tokensInPile,
		CompressionRatio:   1.0,
	})
}

// emitRollingFailedAudit emits a KindCompactionFailed audit with the
// given error classifier, tagged with AggressivenessTier="maximal" so
// dashboards can break out maximal-mode failures from threshold-mode
// failures without sniffing other fields.
func (e *engine) emitRollingFailedAudit(ctx context.Context, sessionID string, model ProviderProfileRef, tokensInPile int, errorKind string) {
	if e.auditEm == nil {
		return
	}
	e.auditEm.Emit(ctx, audit.KindCompactionFailed, audit.CompactionFailedPayload{
		SessionID:          sessionID,
		AggressivenessTier: rollingMaximalTier,
		ModelUsed:          model.String(),
		TokensInSpan:       tokensInPile,
		ErrorKind:          errorKind,
	})
}
