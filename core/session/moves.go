package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// ---------------------------------------------------------------------------
// The transcript-entry seam (model-moves-transcript-01PMCH01 WP01).
//
// THIS FILE IS THE ONLY PLACE IN THE REPOSITORY THAT MAY WRITE MOVE
// METADATA ONTO A session.Message.
//
// Message.moveKind / moveIndex / moveTurnSpanID are unexported, so no
// package outside core/session can set them at all — that half of the
// single-writer rule is enforced by the compiler. Inside core/session,
// scripts/ci/check-single-move-writer.sh enforces the other half: those
// three identifiers may not be assigned anywhere but here.
//
// Spec §4: "One writer: session_write/the chat runner persists moves
// through the same seam — no second history-writing path (the
// convergence doctrine binds)."
// ---------------------------------------------------------------------------

// MoveKind classifies a transcript entry within one human turn.
//
// A single human turn can drive up to the agent_loop's iteration ceiling
// of model fires. Each fire's text is an assistant_move; each tool it
// invokes is a tool_call paired with a tool_result; the answer the user
// reads is the final. The empty MoveKind is the classic pre-moves entry:
// one flattened assistant message per human turn.
type MoveKind string

const (
	// MoveKindAssistantMove is one model iteration's text output.
	MoveKindAssistantMove MoveKind = "assistant_move"
	// MoveKindToolCall is one tool invocation the model requested. The
	// DISPLAY layer carries an args summary, never raw argument values
	// (spec §4, redaction).
	MoveKindToolCall MoveKind = "tool_call"
	// MoveKindToolResult is the result returned for a MoveKindToolCall.
	// Pairs with it by ToolCalls[].ID.
	MoveKindToolResult MoveKind = "tool_result"
	// MoveKindFinal is the answer the collapsed view renders — the last
	// assistant text of the turn.
	MoveKindFinal MoveKind = "final"
)

// MoveKinds is the canonical ordered set of move kinds. The wire
// contract, the frontend union type and the SQL CHECK-free `kind`
// column all mirror this list; extend all three together.
func MoveKinds() []MoveKind {
	return []MoveKind{
		MoveKindAssistantMove,
		MoveKindToolCall,
		MoveKindToolResult,
		MoveKindFinal,
	}
}

// known reports whether k is one of the four move kinds. The empty kind
// is deliberately NOT known: it means "classic entry", which callers
// express by leaving TranscriptEntry.Kind zero, not by naming it.
func (k MoveKind) known() bool {
	for _, want := range MoveKinds() {
		if k == want {
			return true
		}
	}
	return false
}

// Errors returned by AppendTranscriptEntry when move metadata is
// incoherent. They are sentinel values so callers (and WP02's runner)
// can distinguish a contract violation from a storage failure.
var (
	// ErrUnknownMoveKind means Kind was non-empty but not one of
	// MoveKinds(). Persisting it would put a value on the wire that no
	// reader has a case for.
	ErrUnknownMoveKind = errors.New("session: unknown move kind")
	// ErrMoveTurnSpanRequired means a move-kind entry arrived without
	// the id of the human turn it belongs to. A move with no turn is
	// unorderable and unrenderable.
	ErrMoveTurnSpanRequired = errors.New("session: move entry requires a turn span id")
	// ErrNegativeMoveIndex means MoveIndex was below zero. Position
	// within a turn is 0-based and dense.
	ErrNegativeMoveIndex = errors.New("session: move index must be >= 0")
	// ErrMoveMetadataWithoutKind means MoveIndex/TurnSpanID were set on
	// an entry with no Kind. Silently dropping them would make the seam
	// lossy; refusing keeps "no kind == classic entry" true.
	ErrMoveMetadataWithoutKind = errors.New("session: move metadata requires a move kind")
	// ErrModelToolArgsWithoutToolCall means ModelToolArgs was set on an
	// entry that is not a tool_call. The model layer reconstructs an
	// assistant tool_use block from exactly one kind of row; arguments
	// hanging off any other kind have no reader and would be a silent
	// leak of raw values into a row the display layer renders.
	ErrModelToolArgsWithoutToolCall = errors.New("session: model tool args require a tool_call move")
)

// TranscriptEntry is the input shape of the single transcript-writing
// seam. It is what the chat runner and the session_write node hand to
// the store; Manager.AppendTranscriptEntry turns it into the persisted
// Message.
//
// Leaving Kind empty writes a classic entry — byte-identical on the
// wire to everything written before this mission. Setting Kind writes a
// move, and then TurnSpanID is mandatory.
type TranscriptEntry struct {
	// Role is the speaker. Moves use RoleAssistant for assistant_move /
	// final and RoleTool for tool_call / tool_result.
	Role Role
	// Content is the entry's text.
	Content string
	// ToolCalls carries the structured tool payload for tool_call /
	// tool_result entries.
	ToolCalls []ToolCall
	// ContentBlocks is the polymorphic multimodal body (the
	// content_json column added by multimodal-io WP02). Empty means
	// "text only" and the store synthesizes the canonical single text
	// block from Content, exactly as it does for every other writer.
	//
	// WHY IT IS HERE (model-moves-transcript-01PMCH01 WP02, review of
	// WP01): without it this seam could express strictly LESS than
	// Manager.AppendMessage, and the only way to persist a multimodal
	// entry was the AppendMessage path — which cannot stamp move
	// metadata, so a move routed through it degraded to a classic entry
	// with neither the compiler nor check-single-move-writer.sh
	// objecting. The degradation was silent because the author had no
	// expressive alternative. Now there is one, and the compiler still
	// forbids minting move metadata anywhere else, so the only remaining
	// use of AppendMessage is what it has always been: classic entries.
	ContentBlocks []llm.ContentBlock

	// Kind is the move classification. Empty means "classic entry".
	Kind MoveKind
	// MoveIndex is the 0-based position of this entry inside its human
	// turn. Carries no meaning when Kind is empty.
	MoveIndex int
	// TurnSpanID is the id of the user message that opened the turn this
	// entry belongs to. Every move in one turn shares it — that shared
	// id IS the turn's span. Required when Kind is set.
	TurnSpanID string

	// ModelToolArgs carries the MODEL LAYER's raw tool arguments for a
	// tool_call entry: tool-call id → the JSON argument object the model
	// emitted (model-moves-transcript-01PMCH01 WP03).
	//
	// Only meaningful on MoveKindToolCall. Setting it on any other kind
	// is rejected (ErrModelToolArgsWithoutToolCall) rather than silently
	// dropped — a silent drop is how the model layer would quietly stop
	// being able to reconstruct a tool_use block, and the symptom would
	// be a provider 400 three turns later, not a test failure here.
	//
	// See modelLayerToolArgs* below for why this is emphatically NOT the
	// same thing as ToolCalls[].Arguments.
	ModelToolArgs map[string]string
}

// isMove reports whether e carries move metadata.
func (e TranscriptEntry) isMove() bool { return e.Kind != "" }

// validate enforces the move-metadata contract before anything reaches
// the store.
func (e TranscriptEntry) validate() error {
	if len(e.ModelToolArgs) > 0 && e.Kind != MoveKindToolCall {
		return fmt.Errorf("%w (kind=%q)", ErrModelToolArgsWithoutToolCall, e.Kind)
	}
	if !e.isMove() {
		if e.MoveIndex != 0 || e.TurnSpanID != "" {
			return fmt.Errorf("%w (index=%d span=%q)",
				ErrMoveMetadataWithoutKind, e.MoveIndex, e.TurnSpanID)
		}
		return nil
	}
	if !e.Kind.known() {
		return fmt.Errorf("%w: %q", ErrUnknownMoveKind, e.Kind)
	}
	if e.TurnSpanID == "" {
		return fmt.Errorf("%w (kind=%q)", ErrMoveTurnSpanRequired, e.Kind)
	}
	if e.MoveIndex < 0 {
		return fmt.Errorf("%w (got %d)", ErrNegativeMoveIndex, e.MoveIndex)
	}
	return nil
}

// AppendTranscriptEntry is THE seam through which every chat transcript
// entry — classic or move — reaches the session store.
//
// It validates the move contract, stamps the move columns onto the
// durable Message, and delegates to AppendMessage for id/sequence
// assignment, last-active bookkeeping and the audit emission. Callers
// outside core/session cannot reach the move fields any other way: they
// are unexported.
//
// model-moves-transcript-01PMCH01 WP01.
func (m *Manager) AppendTranscriptEntry(ctx context.Context, sessionID string,
	e TranscriptEntry) (Message, error) {
	if err := e.validate(); err != nil {
		return Message{}, err
	}
	msg := Message{
		Role:          e.Role,
		Content:       e.Content,
		ToolCalls:     e.ToolCalls,
		ContentBlocks: e.ContentBlocks,
	}
	if e.isMove() {
		idx := e.MoveIndex
		msg.moveKind = e.Kind
		msg.moveIndex = &idx
		msg.moveTurnSpanID = e.TurnSpanID
		msg.modelToolArgs = cloneModelLayerToolArgs(e.ModelToolArgs)
	}
	return m.AppendMessage(ctx, sessionID, msg)
}

// ---- read accessors ----------------------------------------------------

// MoveKind returns the entry's move classification, or the empty
// MoveKind for a classic pre-moves entry.
func (m Message) MoveKind() MoveKind { return m.moveKind }

// MoveIndex returns the entry's 0-based position within its human turn,
// or nil for a classic entry. The pointer is a copy — mutating it does
// not touch the Message.
func (m Message) MoveIndex() *int {
	if m.moveIndex == nil {
		return nil
	}
	v := *m.moveIndex
	return &v
}

// TurnSpanID returns the id of the user message that opened the turn
// this entry belongs to, or "" for a classic entry.
func (m Message) TurnSpanID() string { return m.moveTurnSpanID }

// ModelLayerToolArgs returns the MODEL LAYER's raw tool arguments for a
// tool_call move — tool-call id → the JSON argument object the model
// emitted — or nil for every other row.
//
// ────────────────────────────────────────────────────────────────────
// READ THIS BEFORE USING IT (spec §4, two-layer redaction).
//
// There are two layers with two opposite rules about the same bytes,
// and this is the one that MAY see values:
//
//	DISPLAY layer  → chat.displayArgsSummary. Argument NAMES and value
//	                 TYPES only, never a value. It is what a tool_call
//	                 row's Content holds, what the user reads, copies,
//	                 exports and shares. session.ToolCall.Arguments
//	                 belongs to this layer and is nil forever.
//
//	MODEL layer    → this function, consumed by the model-visible
//	                 history composition. RAW arguments, because the
//	                 provider protocol cannot express an assistant
//	                 tool_use block without them and because the model
//	                 authored them in the first place. This is history,
//	                 not logs.
//
// Neither is a correct implementation of the other. If you are here
// because a display surface is missing argument values, you want
// displayArgsSummary and the answer is "by design". If you are here
// because a provider rejected a request for a malformed tool_use, you
// are in the right place.
//
// The returned map is a copy; mutating it does not touch the Message.
func (m Message) ModelLayerToolArgs() map[string]string {
	return cloneModelLayerToolArgs(m.modelToolArgs)
}

// cloneModelLayerToolArgs copies a model-layer args map, returning nil
// for an empty input so a classic entry's persisted bytes stay NULL.
func cloneModelLayerToolArgs(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ---- SQL column plumbing -----------------------------------------------
//
// The store never touches the move fields directly; it round-trips them
// through these two helpers so the single-writer grep gate has exactly
// one file to guard.

// moveColumnValues renders the move fields as the three bind values for
// the session_messages INSERT: (kind, move_index, turn_span_id). A
// classic entry binds NULL/NULL/NULL, which is what every pre-existing
// row already holds after migration 0333.
func moveColumnValues(m Message) (kindCol, indexCol, spanCol, modelArgsCol any) {
	if m.moveKind == "" {
		return nil, nil, nil, nil
	}
	kindCol = string(m.moveKind)
	if m.moveIndex != nil {
		indexCol = int64(*m.moveIndex)
	}
	if m.moveTurnSpanID != "" {
		spanCol = m.moveTurnSpanID
	}
	if len(m.modelToolArgs) > 0 {
		// Marshal failure binds NULL rather than aborting the write: a
		// transcript row that loses its model-layer args degrades to a
		// move the history composition will not expand (the pair-integrity
		// sweep drops the half it cannot build), which is strictly better
		// than failing the user's turn over a serialisation edge case.
		if b, err := json.Marshal(m.modelToolArgs); err == nil {
			modelArgsCol = string(b)
		}
	}
	return kindCol, indexCol, spanCol, modelArgsCol
}

// applyMoveColumns hydrates the move fields from a scanned row. NULL
// kind leaves the entry classic regardless of what the other two
// columns hold — the kind is the discriminator.
func applyMoveColumns(m *Message, kind sql.NullString, moveIndex sql.NullInt64,
	turnSpanID sql.NullString, modelArgs sql.NullString) {
	if !kind.Valid || kind.String == "" {
		return
	}
	m.moveKind = MoveKind(kind.String)
	if moveIndex.Valid {
		v := int(moveIndex.Int64)
		m.moveIndex = &v
	}
	if turnSpanID.Valid {
		m.moveTurnSpanID = turnSpanID.String
	}
	if modelArgs.Valid && modelArgs.String != "" {
		var decoded map[string]string
		// A corrupt payload leaves the field nil, which the composition
		// reads as "this tool_call cannot be expanded" and its pair is
		// dropped whole. Never a partial tool_use.
		if err := json.Unmarshal([]byte(modelArgs.String), &decoded); err == nil {
			m.modelToolArgs = cloneModelLayerToolArgs(decoded)
		}
	}
}
