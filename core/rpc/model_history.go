package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/llm"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// ---------------------------------------------------------------------------
// THE model-visible history composition — model-moves-transcript-01PMCH01
// WP03 (spec FR-002, §4 "the history change is the risky half").
//
// This file owns the answer to one question: given the rows a session
// has persisted, what message array does the next request carry?
//
// ONE COMPOSITION, NOT TWO
// ------------------------
// The gate does not select between an old function and a new one. There
// is a single walk over the persisted rows, and the gate is a PARAMETER
// of that walk — moveFidelity — which decides, per row, how much of a
// move survives into the request:
//
//	moveFidelityClassic  a move-kind row projects to the same thing the
//	                     pre-mission transcript held: nothing, except the
//	                     turn's `final`, which projects exactly as the
//	                     pre-mission flattened assistant row did.
//	moveFidelityMoves    a move-kind row projects to its provider-native
//	                     counterpart: assistant text, assistant tool_use,
//	                     paired tool_result.
//
// Everything else — classic rows, ordering, the content-block path, the
// truncation window, and the pair-integrity sweep — is shared code that
// runs identically at both settings. That is deliberate and it is the
// doctrine: `if gate { newCompose() } else { oldCompose() }` grows two
// definitions of what a transcript IS, and the one that rots is the one
// nobody runs. Here, a bug in ordering or truncation is a bug at both
// settings, so both goldens catch it.
//
// WHERE THE PROVIDER SHAPES COME FROM
// -----------------------------------
// Nowhere in this file. The per-family renderers already exist and are
// already the only thing that knows a wire format:
//
//	coreag.Message  →  LLMProviderAdapter.Generate  →  corellm.Message
//	                   (text / tool_use / tool_result content blocks)
//	                →  anthropic.convertContent   → tool_use + tool_result blocks
//	                →  openaiwire.BuildRequestBody → tool_calls[] + role:"tool"
//
// So this composition's whole job on the model-fidelity side is to
// populate the fields of coreag.Message that carry a tool turn, which
// the pre-WP03 projection left zero. Adding a second, parallel path
// that emitted provider JSON here would be the convergence violation;
// filling in the seam the renderers already read is the convergence
// fix.
//
// TWO OF THE THREE ARE FED, AND THE THIRD IS NOT. ToolCalls and
// ToolCallID are populated below. coreag.Message.IsError is NOT, and
// cannot be from here: it would have to arrive from the persisted row,
// and neither coreag.HistoryEntry nor session.TranscriptEntry carries
// an error flag for a tool_result — WP02 records the failure only in
// the result TEXT. The consequence is bounded but real: a failed tool
// reaches the model without Anthropic's `tool_result.is_error`, so the
// model infers failure from the text the way it does today rather than
// from the protocol field. Closing it is a seam change (a flag on
// HistoryEntry + the column behind it), which is why it is not done
// here — WP04 is concurrently editing exactly those files for the
// DISPLAY layer's session.ToolCall.IsError, and the model layer's
// half belongs on top of that merge, not underneath it.
//
// WHAT THIS REPAIRS
// -----------------
// WP02 began persisting move rows without teaching this projection
// about them, so every move row already reached the model as a flat
// message — and a tool_result row became a corellm tool_result block
// with an EMPTY tool_use_id and no matching tool_use anywhere in the
// request. That is the classic provider 400. Both fidelity settings fix
// it: classic drops the move rows back out of the request, moves pairs
// them properly. Neither can emit a half-pair; see pairIntegritySweep.
// ---------------------------------------------------------------------------

// moveFidelity is the gate, expressed as the parameter it actually is.
type moveFidelity int

const (
	// moveFidelityClassic composes one flattened assistant message per
	// human turn — byte-identical to what shipped before this mission.
	// It is the fail-closed value: every ambiguity, every read error and
	// every session that predates the mission resolves here.
	moveFidelityClassic moveFidelity = iota
	// moveFidelityMoves composes the full move chain in provider-native
	// shape.
	moveFidelityMoves
)

// resolveMoveFidelity ANDs the gate's two inputs, fail-closed.
//
//	dialEnabled   the LIVE half — Settings.MoveFidelityHistoryEnabled(),
//	              read at the point of consumption by the caller so that
//	              turning it off reverts every session on the next
//	              request rather than the next launch.
//	sessionMode   the DURABLE half — session.Record.MoveHistoryMode, the
//	              mode this conversation was opened under. Empty (every
//	              session older than migration 0334) resolves classic, so
//	              turning the dial ON never reshapes a thread already in
//	              flight.
//
// Both must say yes. Either saying no — or not being resolvable at all
// — yields classic.
func resolveMoveFidelity(dialEnabled bool, sessionMode string) moveFidelity {
	if dialEnabled && session.MoveHistoryModeFromRecord(sessionMode) {
		return moveFidelityMoves
	}
	return moveFidelityClassic
}

// modelHistoryRow is the projection-input shape: one persisted
// transcript row, reduced to exactly what the composition reads.
//
// Defined here rather than reusing session.Message so the composition is
// unit-testable without a store, and so the fields it must NOT read
// (ToolCalls[].Arguments — the display layer's, permanently nil) are
// simply absent rather than present-and-resisted.
type modelHistoryRow struct {
	Role          string
	Content       string
	ContentBlocks []llm.ContentBlock

	// MoveKind is "" for a classic entry, else one of
	// session.MoveKinds().
	MoveKind string
	// ToolID / ToolName identify the tool call a tool_call or
	// tool_result row belongs to. The pair is (tool_call, tool_result)
	// sharing one ToolID.
	ToolID   string
	ToolName string
	// ModelToolArgs is the MODEL LAYER's raw argument JSON for a
	// tool_call row, keyed by tool-call id — session.Message's
	// ModelLayerToolArgs(), never ToolCalls[].Arguments. Empty means the
	// row cannot be expanded into a tool_use block, and the whole pair is
	// then dropped rather than a malformed half shipped.
	ModelToolArgs map[string]string
}

// composeModelHistory is the single composition. It walks rows once,
// projecting each according to fidelity, applies the caller's tail
// window, and then guarantees pair integrity.
//
// ORDER MATTERS AND IS THE INVARIANT. Truncation happens BEFORE the
// pair-integrity sweep, never after, so a window that cuts through the
// middle of a tool turn has its dangling half removed rather than
// shipped. Doing it the other way round — sweep, then truncate — would
// verify pairs that the very next line then splits, which is exactly the
// provider-400 this ordering exists to make unreachable. WP06 owns the
// compaction STRATEGIES; the invariant that no composition can emit a
// half-pair is owned here, because this is the only function that emits.
//
// n <= 0 means "no window" (the shipped chat graph's history_read n=0).
func composeModelHistory(rows []modelHistoryRow, fidelity moveFidelity, n int) []coreag.Message {
	projected := make([]coreag.Message, 0, len(rows))
	for _, r := range rows {
		msg, ok := projectRow(r, fidelity)
		if !ok {
			continue
		}
		projected = append(projected, msg)
	}
	if n > 0 && len(projected) > n {
		projected = projected[len(projected)-n:]
	}
	return pairIntegritySweep(projected)
}

// projectRow renders one persisted row as at most one model-visible
// message. The bool reports whether the row contributes at all.
//
// This is the ONLY place fidelity is consulted, and it consults it as a
// property of the ROW's kind, not as a fork in control flow: a classic
// row and a `final` row project identically at both settings, which is
// what makes the gate-off composition byte-identical to the pre-mission
// one rather than merely similar to it.
func projectRow(r modelHistoryRow, fidelity moveFidelity) (coreag.Message, bool) {
	switch r.MoveKind {
	case "":
		// A classic entry — every row written before this mission, and
		// every row still written outside the chat runner (the user's own
		// turn, system notes, compaction summaries). Untouched by the
		// gate in either position.
		return textMessage(r), true

	case string(session.MoveKindFinal):
		// The turn's answer. This is the row that OCCUPIES the position
		// the pre-mission flattened assistant message held — same role,
		// same text — which is why classic fidelity can drop every other
		// move and still reproduce the old request exactly.
		return textMessage(r), true

	case string(session.MoveKindAssistantMove):
		if fidelity != moveFidelityMoves {
			return coreag.Message{}, false
		}
		return textMessage(r), true

	case string(session.MoveKindToolCall):
		if fidelity != moveFidelityMoves {
			return coreag.Message{}, false
		}
		args, ok := r.ModelToolArgs[r.ToolID]
		if r.ToolID == "" || !ok || args == "" {
			// No raw arguments means no faithful tool_use block. Emitting
			// one with empty input would be a lie the model then reasons
			// from; emitting none leaves the answering tool_result an
			// orphan, which the sweep below removes. Dropping here is what
			// makes that sweep's job well-defined.
			return coreag.Message{}, false
		}
		// The assistant turn that REQUESTED the tool. Content is
		// deliberately empty: the row's stored Content is the DISPLAY
		// layer's args summary (chat.displayArgsSummary) and must never
		// reach the model as if it were the assistant's prose.
		return coreag.Message{
			Role: "assistant",
			ToolCalls: []coreag.ToolCallRequest{{
				ID:        r.ToolID,
				Name:      r.ToolName,
				Arguments: args,
			}},
		}, true

	case string(session.MoveKindToolResult):
		if fidelity != moveFidelityMoves {
			return coreag.Message{}, false
		}
		if r.ToolID == "" {
			return coreag.Message{}, false
		}
		msg := textMessage(r)
		msg.Role = "tool"
		msg.Name = r.ToolName
		msg.ToolCallID = r.ToolID
		return msg, true

	default:
		// A kind this build does not understand. Fail-closed: a row we
		// cannot classify is not a row we should reshape a request
		// around.
		return coreag.Message{}, false
	}
}

// textMessage projects a row's body, preferring the canonical
// polymorphic blocks over the flattened text column.
//
// The ContentBlocks branch drains the WP02 ledger entry naming WP03 as a
// consumer: "per-family rendering must not re-flatten a multimodal move
// on its way to the provider". Before this, chatSessionMessageReader
// carried Role+Content only, so an image-bearing row reached the model
// as whatever text had been flattened alongside it — or as nothing.
func textMessage(r modelHistoryRow) coreag.Message {
	msg := coreag.Message{Role: r.Role, Content: r.Content}
	if r.Content == "" && len(r.ContentBlocks) > 0 {
		msg.Content = llm.Message{Content: r.ContentBlocks}.Text()
	}
	return msg
}

// pairIntegritySweep makes an orphaned tool_use/tool_result pair
// IMPOSSIBLE to emit, rather than merely unlikely.
//
// Both providers reject a half-pair, and they reject it as a 400 on the
// whole request:
//
//	Anthropic  a tool_use block with no tool_result in the following
//	           user turn, or a tool_result whose tool_use_id names no
//	           preceding tool_use.
//	OpenAI     a role:"tool" message whose tool_call_id appears in no
//	           preceding assistant tool_calls array.
//
// The sweep is deliberately symmetric and deliberately last: it runs
// after truncation, so it sees the request as the provider will. Any
// tool id that does not appear on BOTH sides loses both sides — the
// half that exists is dropped too, because a lone tool_result is as
// fatal as a lone tool_use.
//
// A message emptied by the sweep (an assistant turn whose only content
// was the dropped tool_use) is removed rather than sent blank.
func pairIntegritySweep(msgs []coreag.Message) []coreag.Message {
	calls := map[string]bool{}
	results := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				calls[tc.ID] = true
			}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			results[m.ToolCallID] = true
		}
	}
	// complete[id] is true only when the id was seen on both sides.
	complete := map[string]bool{}
	for id := range calls {
		if results[id] {
			complete[id] = true
		}
	}

	out := make([]coreag.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" && !complete[m.ToolCallID] {
			continue
		}
		if len(m.ToolCalls) > 0 {
			kept := make([]coreag.ToolCallRequest, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if complete[tc.ID] {
					kept = append(kept, tc)
				}
			}
			if len(kept) == 0 && m.Content == "" {
				// The message was nothing but the dropped call.
				continue
			}
			m.ToolCalls = kept
			if len(kept) == 0 {
				m.ToolCalls = nil
			}
		}
		out = append(out, m)
	}
	return out
}

// modelHistoryRowsFrom projects persisted session messages onto the
// composition's input shape.
//
// The model-layer read is ModelLayerToolArgs() — NOT ToolCalls[].Arguments,
// which is the display layer's field and is nil by construction. See
// core/session/moves.go for the two layers' contract; the helpers are
// named apart precisely so this call site cannot be "simplified" into
// the wrong one.
func modelHistoryRowsFrom(stored []session.Message) []modelHistoryRow {
	out := make([]modelHistoryRow, 0, len(stored))
	for _, m := range stored {
		row := modelHistoryRow{
			Role:          string(m.Role),
			Content:       m.Content,
			ContentBlocks: m.ContentBlocks,
			MoveKind:      string(m.MoveKind()),
			ModelToolArgs: m.ModelLayerToolArgs(),
		}
		if len(m.ToolCalls) > 0 {
			row.ToolID = m.ToolCalls[0].ID
			row.ToolName = m.ToolCalls[0].Name
		}
		out = append(out, row)
	}
	return out
}
