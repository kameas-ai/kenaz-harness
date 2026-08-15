package export

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

// ---------------------------------------------------------------------------
// Export is a DISPLAY consumer (model-moves-transcript-01PMCH01 WP05, FR-006).
//
// THE CONTRACT, and why it is the one it is.
//
// A session export is a document a human reads and forwards. Before WP05
// it walked the message list row by row, which was correct while a human
// turn was two rows and became wrong the moment WP02 started persisting
// the whole trajectory:
//
//  1. A move-bearing turn exported as ~13 sections headed "Turn 1" …
//     "Turn 13" — thirteen turns for one question.
//  2. Every `tool_result` row's Content is the RAW tool output, and the
//     row loop wrote it into the document verbatim and uncapped. A single
//     `grep -r` exported four thousand lines into the middle of a
//     conversation.
//  3. Every `tool_call`'s arguments were rendered as raw JSON.
//
// So the rules this file implements:
//
//   - THE DOCUMENT IS TURNS. Move rows never take a turn heading and never
//     advance the turn counter. A turn's answer is rendered exactly as a
//     classic assistant turn always was, and the trajectory that produced
//     it hangs beneath it in a disclosure — the same shape the chat
//     surface collapses to (WP05 FR-004). A classic session has no move
//     rows, so its export is byte-identical to the pre-WP05 output.
//
//   - ARGUMENT VALUES NEVER APPEAR. A tool_call row's Content is already
//     the display layer's args summary (argument names and value types —
//     `chat.displayArgsSummary`), and that is what the export prints. For
//     a pre-mission row that carries a structured `ToolCall.Arguments`
//     map, `argsSummaryFromValues` renders the same names-and-types shape
//     rather than the values. Nothing here reads
//     `session.Message.ModelLayerToolArgs`, which is the MODEL layer's
//     raw arguments and is not exportable — it is not even reachable from
//     this package's imports.
//
//   - TOOL OUTPUT IS CAPPED. `capToolOutput` at ToolOutputCap runes, with
//     an explicit marker. The chat surface caps at the same size
//     (ToolChip.vue's OUTPUT_CAP); an export that a human reads has no
//     more business carrying a 4 MB log than the transcript does.
//
// The redaction pass (redactMessages → RedactValue) still runs over
// everything on top of these rules. It is a credential scanner, not a
// structural guarantee: it would not catch a secret sitting in a nested
// argument object, which is precisely why the structural rule above is
// "no argument values", not "redact the argument values".
// ---------------------------------------------------------------------------

// ToolOutputCap is the maximum number of runes of a single tool result
// the export carries. Matches the chat surface's OUTPUT_CAP so what you
// export is what you were reading.
const ToolOutputCap = 4000

// capToolOutput truncates s to ToolOutputCap runes, appending a marker
// that names how much was dropped. Cutting on a rune boundary is not
// decoration: slicing a UTF-8 string at a byte offset mid-rune produces
// U+FFFD in the exported document.
func capToolOutput(s string) string {
	total := utf8.RuneCountInString(s)
	if total <= ToolOutputCap {
		return s
	}
	// `range` over a string yields the byte offset of each rune start,
	// so the offset seen at the (cap+1)-th rune is exactly where to cut.
	cut := len(s)
	n := 0
	for i := range s {
		if n == ToolOutputCap {
			cut = i
			break
		}
		n++
	}
	return fmt.Sprintf("%s\n…output truncated (%d more characters)",
		s[:cut], total-ToolOutputCap)
}

// argsSummaryFromValues renders a structured argument map the way the
// DISPLAY layer does: argument names and value TYPES, sorted for a
// stable document, never values.
//
// It exists for rows written before the mission, whose `ToolCall`
// carries an `Arguments` map rather than a `tool_call` move row with a
// summary already in its Content. Deliberately named nothing like the
// model layer's helpers — see the contract on
// `session.Message.ModelLayerToolArgs` for why the two must never be
// confused.
func argsSummaryFromValues(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+valueTypeName(args[k]))
	}
	return strings.Join(parts, " ")
}

// valueTypeName names a JSON value's type without revealing it.
func valueTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64, float32, int, int64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}

// exportItem is one "## Turn N" section of the document: the row that
// gets the heading, plus the move rows folded beneath it.
type exportItem struct {
	// Msg is the row rendered as the turn's body. For a move-bearing
	// turn this is the answer; for a classic session it is the row
	// itself, exactly as before the mission.
	Msg session.Message
	// Trail is the turn's trajectory — the intermediate moves and the
	// tool_call / tool_result rows — in persisted order. Empty for every
	// classic row, which is what makes a classic export unchanged.
	Trail []session.Message
	// TrailOnly marks a section that holds moves with no answer row of
	// its own: an interrupted turn that never reached `final`, or the
	// tool traffic a turn emitted after its last piece of text. Its Msg
	// is synthetic (role assistant, no content) so the trajectory still
	// has a heading to hang from. The chat surface opens a fresh trail
	// for exactly the same rows.
	TrailOnly bool
}

// projectExportItems folds a flat row list into turn sections.
//
// It mirrors `frontend/src/lib/transcript.ts` deliberately, including
// the positional answer rule: within a turn span the highest MoveIndex
// among the non-empty assistant_move / final rows is the answer, and
// everything else is trajectory. Keying off the KIND instead would put
// the wrong row in the document for an interrupted turn, where nothing
// was ever stamped `final`. Export and the chat surface agreeing on
// which row is the answer is the property that makes "the export is
// what you were reading" true rather than aspirational.
func projectExportItems(msgs []session.Message) []exportItem {
	// Pass 1 — the answer position per span.
	answerIdx := make(map[string]int)
	for _, m := range msgs {
		k := m.MoveKind()
		if k != session.MoveKindAssistantMove && k != session.MoveKindFinal {
			continue
		}
		if m.Content == "" {
			continue
		}
		span := m.TurnSpanID()
		idx := 0
		if p := m.MoveIndex(); p != nil {
			idx = *p
		}
		if cur, ok := answerIdx[span]; !ok || idx >= cur {
			answerIdx[span] = idx
		}
	}

	items := make([]exportItem, 0, len(msgs))
	pending := make(map[string][]session.Message)
	var pendingOrder []string

	for _, m := range msgs {
		kind := m.MoveKind()
		if kind == "" {
			items = append(items, exportItem{Msg: m})
			continue
		}
		span := m.TurnSpanID()
		idx := 0
		if p := m.MoveIndex(); p != nil {
			idx = *p
		}
		isAnswer := (kind == session.MoveKindAssistantMove || kind == session.MoveKindFinal) &&
			m.Content != "" && idx == answerIdx[span]
		if isAnswer {
			items = append(items, exportItem{Msg: m, Trail: pending[span]})
			delete(pending, span)
			continue
		}
		if _, seen := pending[span]; !seen {
			pendingOrder = append(pendingOrder, span)
		}
		pending[span] = append(pending[span], m)
	}

	// Moves left over once every span's answer has been placed: an
	// interrupted turn that never produced one, and the tool traffic a
	// turn emitted after its last piece of text. Their trajectory still
	// belongs in the document; dropping it would make the export quietly
	// lossier than the store. Flushed in first-seen order after the
	// answered turns, which in practice is the tail of the session.
	for _, span := range pendingOrder {
		trail, ok := pending[span]
		if !ok || len(trail) == 0 {
			continue
		}
		synth := session.Message{
			SessionID: trail[0].SessionID,
			Role:      session.RoleAssistant,
			CreatedAt: trail[0].CreatedAt,
		}
		items = append(items, exportItem{Msg: synth, Trail: trail, TrailOnly: true})
	}

	return items
}

// moveDisplay is one trajectory entry reduced to what the export prints.
type moveDisplay struct {
	Kind        string
	MoveIndex   int
	TurnSpanID  string
	Text        string // assistant_move content
	ToolID      string
	ToolName    string
	ArgsSummary string // tool_call — names and types, never values
	Output      string // tool_result — capped
	IsError     bool
}

// describeMove reduces a persisted move row to its display projection.
func describeMove(m session.Message) moveDisplay {
	d := moveDisplay{
		Kind:       string(m.MoveKind()),
		TurnSpanID: m.TurnSpanID(),
	}
	if p := m.MoveIndex(); p != nil {
		d.MoveIndex = *p
	}
	if len(m.ToolCalls) > 0 {
		tc := m.ToolCalls[0]
		d.ToolID = tc.ID
		d.ToolName = tc.Name
		d.IsError = tc.IsError
	}
	switch m.MoveKind() {
	case session.MoveKindToolCall:
		// The row's Content IS the display summary WP02 persisted. A
		// pre-mission row without one falls back to names-and-types over
		// the structured map — still never a value.
		d.ArgsSummary = m.Content
		if d.ArgsSummary == "" && len(m.ToolCalls) > 0 {
			d.ArgsSummary = argsSummaryFromValues(m.ToolCalls[0].Arguments)
		}
	case session.MoveKindToolResult:
		d.Output = capToolOutput(m.Content)
	default:
		d.Text = m.Content
	}
	return d
}
