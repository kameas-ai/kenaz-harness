package rpc

// model_history_test.go — the safety net for the risky half of
// model-moves-transcript-01PMCH01 (WP03, spec FR-002 + §4).
//
// The load-bearing claims, and the test that pins each:
//
//	gate OFF is byte-identical to the pre-mission composition
//	    → TestModelHistory_GateOff_MatchesPreMissionOracle
//	gate ON produces provider-native shape, per family
//	    → TestModelHistory_GateOn_AnthropicWireShape
//	      TestModelHistory_GateOn_OpenAIWireShape
//	the gate is actually consulted (both goldens cannot both pass
//	if it is ignored)
//	    → TestModelHistory_GateIsLoadBearing_BothPositionsDiffer
//	orphan tool pairs are impossible, including under truncation
//	    → TestModelHistory_TruncationNeverOrphansAPair
//	      TestModelHistory_ToolCallWithoutModelArgsDropsBothHalves
//	the defaulting rules
//	    → TestResolveMoveFidelity_DefaultingRules

import (
	"encoding/json"
	"fmt"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// ---------------------------------------------------------------------------
// The oracle.
// ---------------------------------------------------------------------------

// preMissionHistoryProjection is a VERBATIM copy of
// chatSessionMessageReader.History's projection body as it stood at
// commit 2066c928 — `main` before this mission's first commit, i.e. the
// last tree in which the model-visible composition was the one that had
// always shipped.
//
// It exists so the gate-OFF golden is an ORACLE rather than a
// transcription. A golden hand-written from what the author believes the
// old bytes were proves only that the author is self-consistent; a
// golden produced by running the old code proves the claim. (A prior
// reviewer on this mission caught exactly that mistake, which is why
// this is here rather than a []coreag.Message literal.)
//
// Recover it with:
//
//	git show 2066c928:core/rpc/api.go
//
// and read the body of func (r chatSessionMessageReader) History.
func preMissionHistoryProjection(stored []session.Message, n int) []coreag.Message {
	if n > 0 && len(stored) > n {
		stored = stored[len(stored)-n:]
	}
	out := make([]coreag.Message, 0, len(stored))
	for _, m := range stored {
		out = append(out, coreag.Message{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Fixtures.
// ---------------------------------------------------------------------------

// preMissionRows is the conversation as a PRE-MISSION runner persisted
// it: one user row, one flattened assistant row per human turn. This is
// the input the oracle is fed.
//
// It is the same conversation as moveBearingRows below — same user text,
// same final answer — which is what makes comparing the two meaningful.
func preMissionRows() []session.Message {
	return []session.Message{
		{Role: session.RoleUser, Content: "read foo.txt and tell me what it says"},
		{Role: session.RoleAssistant, Content: "The file says hello world."},
	}
}

// moveBearingRows is the SAME conversation as WP02's runner persists it:
// the user turn, then a three-move trajectory (assistant text → tool_call
// → tool_result) and the turn's `final`.
//
// Built through session.Manager.AppendTranscriptEntry — the real seam,
// the only thing that can stamp move metadata — so the rows carry
// genuinely-persisted move columns rather than hand-set struct fields
// (which the compiler forbids from this package anyway).
func moveBearingRows(t *testing.T) []session.Message {
	t.Helper()
	ctx := t.Context()
	mgr := session.NewManager(session.NewMemoryStore())
	rec, err := mgr.Create(ctx, "moves")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	user, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role: session.RoleUser, Content: "read foo.txt and tell me what it says",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	span := user.ID

	entries := []session.TranscriptEntry{
		{
			Role: session.RoleAssistant, Content: "Let me read the file.",
			Kind: session.MoveKindAssistantMove, MoveIndex: 0, TurnSpanID: span,
		},
		{
			Role: session.RoleTool, Content: "kenaz__read_file(path=<string>)",
			Kind: session.MoveKindToolCall, MoveIndex: 1, TurnSpanID: span,
			ToolCalls:     []session.ToolCall{{ID: "toolu_01", Name: "kenaz__read_file"}},
			ModelToolArgs: map[string]string{"toolu_01": `{"path":"foo.txt"}`},
		},
		{
			Role: session.RoleTool, Content: "hello world",
			Kind: session.MoveKindToolResult, MoveIndex: 2, TurnSpanID: span,
			ToolCalls: []session.ToolCall{{ID: "toolu_01", Name: "kenaz__read_file"}},
		},
		{
			Role: session.RoleAssistant, Content: "The file says hello world.",
			Kind: session.MoveKindFinal, MoveIndex: 3, TurnSpanID: span,
		},
	}
	for _, e := range entries {
		if _, err := mgr.AppendTranscriptEntry(ctx, rec.ID, e); err != nil {
			t.Fatalf("AppendTranscriptEntry(%s): %v", e.Kind, err)
		}
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	return msgs
}

// ---------------------------------------------------------------------------
// Both-positions goldens.
// ---------------------------------------------------------------------------

// TestModelHistory_GateOff_MatchesPreMissionOracle is the byte-identity
// claim: with the gate off, a move-bearing session composes to EXACTLY
// what the pre-mission code produced for the same conversation.
//
// Mutation: in projectRow, make MoveKindAssistantMove / MoveKindToolCall
// / MoveKindToolResult project unconditionally (drop the
// `fidelity != moveFidelityMoves` guards) — the shape of "the gate is
// ignored" — and this fails with 5 messages against the oracle's 2.
func TestModelHistory_GateOff_MatchesPreMissionOracle(t *testing.T) {
	t.Parallel()

	wantJSON := mustJSON(t, preMissionHistoryProjection(preMissionRows(), 0))
	got := composeModelHistory(
		modelHistoryRowsFrom(moveBearingRows(t)), moveFidelityClassic, 0)
	gotJSON := mustJSON(t, got)

	if gotJSON != wantJSON {
		t.Fatalf("gate-OFF composition is not byte-identical to the pre-mission oracle.\n got: %s\nwant: %s",
			gotJSON, wantJSON)
	}
}

// TestModelHistory_GateOff_ClassicRowsAreUntouched pins that the gate
// changes nothing for a session that has no moves at all — every session
// written before this mission, and every non-chat writer (system notes,
// compaction summaries) after it.
func TestModelHistory_GateOff_ClassicRowsAreUntouched(t *testing.T) {
	t.Parallel()
	rows := preMissionRows()
	want := mustJSON(t, preMissionHistoryProjection(rows, 0))

	for _, fidelity := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
		got := mustJSON(t, composeModelHistory(modelHistoryRowsFrom(rows), fidelity, 0))
		if got != want {
			t.Fatalf("classic rows changed at fidelity %v.\n got: %s\nwant: %s", fidelity, got, want)
		}
	}
}

// TestModelHistory_GateIsLoadBearing_BothPositionsDiffer is the mutation
// guard the spec asks for by name: if the gate were ignored, one
// composition would have to satisfy both goldens, and it cannot, because
// they differ.
//
// This is the test that dies FIRST under "ignore the gate", which makes
// the failure legible rather than showing up as a confusing byte diff.
func TestModelHistory_GateIsLoadBearing_BothPositionsDiffer(t *testing.T) {
	t.Parallel()
	rows := modelHistoryRowsFrom(moveBearingRows(t))

	off := composeModelHistory(rows, moveFidelityClassic, 0)
	on := composeModelHistory(rows, moveFidelityMoves, 0)

	if mustJSON(t, off) == mustJSON(t, on) {
		t.Fatal("the two gate positions compose identically — the gate is not consulted, " +
			"so the gate-OFF golden and the gate-ON per-family goldens cannot both be meaningful")
	}
	if len(off) != 2 {
		t.Fatalf("gate OFF: got %d messages, want 2 (user + final)", len(off))
	}
	// user + assistant_move + assistant(tool_use) + tool_result + final
	if len(on) != 5 {
		t.Fatalf("gate ON: got %d messages, want 5 (the 3-move trajectory + user + final)", len(on))
	}
}

// ---------------------------------------------------------------------------
// Per-family wire shape, gate ON.
//
// Both tests run the composition's output through the REAL production
// translation (chat.KernelMessagesToWire — what LLMProviderAdapter.Generate
// calls) and then the REAL per-family renderer. Nothing about a provider's
// JSON is re-implemented here.
// ---------------------------------------------------------------------------

// gateOnWireMessages composes the 3-move turn at full fidelity and
// renders it to the family-agnostic content-block shape.
func gateOnWireMessages(t *testing.T) []corellm.Message {
	t.Helper()
	return chat.KernelMessagesToWire(
		composeModelHistory(modelHistoryRowsFrom(moveBearingRows(t)), moveFidelityMoves, 0))
}

// TestModelHistory_GateOn_OpenAIWireShape asserts the OpenAI family
// shape: the assistant turn carries a `tool_calls` ARRAY, and the result
// is its own `role:"tool"` message carrying the matching
// `tool_call_id`.
//
// Mutation: drop the ToolCalls literal from projectRow's
// MoveKindToolCall branch and this fails — no tool_calls array is
// emitted, and the pair sweep then removes the tool message too.
func TestModelHistory_GateOn_OpenAIWireShape(t *testing.T) {
	t.Parallel()
	body, err := openaiwire.BuildRequestBody(
		corellm.GenerationRequest{Messages: gateOnWireMessages(t)}, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	raw, err := json.MarshalIndent(body["messages"], "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("OpenAI messages for a 3-move turn:\n%s", raw)

	var msgs []map[string]any
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var assistantWithCalls, toolMsg map[string]any
	for _, m := range msgs {
		if m["role"] == "assistant" && m["tool_calls"] != nil {
			assistantWithCalls = m
		}
		if m["role"] == "tool" {
			toolMsg = m
		}
	}
	if assistantWithCalls == nil {
		t.Fatal("no assistant message carries a tool_calls array")
	}
	calls, ok := assistantWithCalls["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %#v, want a 1-element array", assistantWithCalls["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != "toolu_01" {
		t.Errorf("tool_calls[0].id = %v, want toolu_01", call["id"])
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "kenaz__read_file" {
		t.Errorf("tool_calls[0].function.name = %v, want kenaz__read_file", fn["name"])
	}
	// The RAW arguments — the model layer's whole reason to exist.
	if fn["arguments"] != `{"path":"foo.txt"}` {
		t.Errorf("tool_calls[0].function.arguments = %v, want the raw model-emitted JSON", fn["arguments"])
	}
	if toolMsg == nil {
		t.Fatal(`no role:"tool" message — the tool result never reached the model`)
	}
	if toolMsg["tool_call_id"] != "toolu_01" {
		t.Errorf("tool message tool_call_id = %v, want toolu_01 (an unmatched id is a provider 400)",
			toolMsg["tool_call_id"])
	}
}

// TestModelHistory_GateOn_AnthropicWireShape asserts the Anthropic
// family shape: the assistant turn carries a `tool_use` BLOCK with the
// decoded `input` object, and the result is a `tool_result` block naming
// the same `tool_use_id`.
//
// It drives the real anthropic adapter over an httptest server and reads
// the request body the adapter actually wrote, because the adapter's
// body builder is unexported — and going through Stream() is the more
// honest assertion anyway: these are the bytes the API would receive.
func TestModelHistory_GateOn_AnthropicWireShape(t *testing.T) {
	t.Parallel()
	body := captureAnthropicRequestBody(t, gateOnWireMessages(t))

	raw, err := json.MarshalIndent(body["messages"], "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("Anthropic messages for a 3-move turn:\n%s", raw)

	var msgs []map[string]any
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var toolUse, toolResult map[string]any
	for _, m := range msgs {
		blocks, ok := m["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			blk, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch blk["type"] {
			case "tool_use":
				toolUse = blk
			case "tool_result":
				toolResult = blk
			}
		}
	}
	if toolUse == nil {
		t.Fatal("no tool_use block — the model never sees the call it made")
	}
	if toolUse["id"] != "toolu_01" {
		t.Errorf("tool_use.id = %v, want toolu_01", toolUse["id"])
	}
	if toolUse["name"] != "kenaz__read_file" {
		t.Errorf("tool_use.name = %v, want kenaz__read_file", toolUse["name"])
	}
	// Anthropic wants `input` as a decoded OBJECT, not a JSON string.
	input, ok := toolUse["input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_use.input = %#v, want a decoded object", toolUse["input"])
	}
	if input["path"] != "foo.txt" {
		t.Errorf("tool_use.input.path = %v, want foo.txt", input["path"])
	}
	if toolResult == nil {
		t.Fatal("no tool_result block — the model never sees what the tool returned")
	}
	if toolResult["tool_use_id"] != "toolu_01" {
		t.Errorf("tool_result.tool_use_id = %v, want toolu_01 (an unmatched id is a 400)",
			toolResult["tool_use_id"])
	}
}

// ---------------------------------------------------------------------------
// Orphan impossibility.
// ---------------------------------------------------------------------------

// TestModelHistory_TruncationNeverOrphansAPair walks EVERY tail window
// over the move-bearing conversation and asserts that no window ever
// emits half a pair.
//
// A window is exactly the cut WP06's compaction will make and the cut
// agentgraph's exec_control.go already makes today (it calls History
// with a hardcoded n=10), so this is not a hypothetical: window 3 slices
// the trajectory between the tool_call and its tool_result, and window 2
// slices between the tool_result and the final.
//
// Mutation: reorder composeModelHistory to sweep BEFORE truncating —
// `pairIntegritySweep` then verifies pairs that the very next line
// splits — and windows 2 and 3 fail with a dangling tool_result.
func TestModelHistory_TruncationNeverOrphansAPair(t *testing.T) {
	t.Parallel()
	rows := modelHistoryRowsFrom(moveBearingRows(t))

	for n := 1; n <= 8; n++ {
		for _, fidelity := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
			got := composeModelHistory(rows, fidelity, n)
			assertNoOrphans(t, got, "n=%d fidelity=%v", n, fidelity)
		}
	}
}

// TestModelHistory_ToolCallWithoutModelArgsDropsBothHalves pins the
// other orphan source: a tool_call row whose model-layer arguments are
// missing (a pre-0334 move row, or a corrupt payload) cannot become a
// faithful tool_use — so its tool_result must go too, rather than
// shipping alone.
//
// Mutation: in projectRow, emit the tool_use anyway with `Arguments: ""`
// instead of returning false — the composition then carries a tool_use
// with empty input, which is a lie to the model; remove the
// pairIntegritySweep call instead and this fails with a dangling
// tool_result.
func TestModelHistory_ToolCallWithoutModelArgsDropsBothHalves(t *testing.T) {
	t.Parallel()
	rows := []modelHistoryRow{
		{Role: "user", Content: "go"},
		{Role: "tool", Content: "kenaz__read_file(path=<string>)",
			MoveKind: string(session.MoveKindToolCall), ToolID: "toolu_01", ToolName: "kenaz__read_file"},
		{Role: "tool", Content: "hello world",
			MoveKind: string(session.MoveKindToolResult), ToolID: "toolu_01", ToolName: "kenaz__read_file"},
		{Role: "assistant", Content: "done", MoveKind: string(session.MoveKindFinal)},
	}
	got := composeModelHistory(rows, moveFidelityMoves, 0)
	assertNoOrphans(t, got, "missing model args")

	for _, m := range got {
		if m.Role == "tool" {
			t.Fatalf("a tool_result survived with no tool_use to answer: %#v", m)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (user + final) once the unbuildable pair is dropped", len(got))
	}
}

// assertNoOrphans is the provider-400 predicate stated once: every
// tool_call_id referenced by a tool message must appear in some
// assistant tool_calls array, and vice versa.
func assertNoOrphans(t *testing.T, msgs []coreag.Message, format string, args ...any) {
	t.Helper()
	ctx := ""
	if format != "" {
		ctx = " [" + fmt.Sprintf(format, args...) + "]"
	}
	calls := map[string]bool{}
	results := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			results[m.ToolCallID] = true
		}
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("orphaned tool_use %q — no tool_result answers it%s", id, ctx)
		}
	}
	for id := range results {
		if !calls[id] {
			t.Errorf("orphaned tool_result %q — no tool_use precedes it%s", id, ctx)
		}
	}
}

// ---------------------------------------------------------------------------
// Defaulting.
// ---------------------------------------------------------------------------

// TestResolveMoveFidelity_DefaultingRules pins spec §4's defaulting
// contract, including the case that only exists in the field: a session
// created before migration 0334 added the column at all.
//
// Mutation: change resolveMoveFidelity's `&&` to `||` — the
// pre-existing-session rows flip to moves and this fails, which is the
// bug where turning the dial on reshapes a conversation mid-thread.
func TestResolveMoveFidelity_DefaultingRules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		dialEnabled bool
		sessionMode string
		want        moveFidelity
	}{
		{
			name:        "new session created with the dial on gets moves",
			dialEnabled: true,
			sessionMode: string(session.MoveHistoryModeMoves),
			want:        moveFidelityMoves,
		},
		{
			name:        "session created with the dial off stays classic even after the dial is turned on",
			dialEnabled: true,
			sessionMode: string(session.MoveHistoryModeClassic),
			want:        moveFidelityClassic,
		},
		{
			name:        "session that predates the column (NULL -> empty) stays classic",
			dialEnabled: true,
			sessionMode: "",
			want:        moveFidelityClassic,
		},
		{
			name:        "dial off reverts a moves session immediately",
			dialEnabled: false,
			sessionMode: string(session.MoveHistoryModeMoves),
			want:        moveFidelityClassic,
		},
		{
			name:        "unrecognised mode fails closed",
			dialEnabled: true,
			sessionMode: "some-future-mode",
			want:        moveFidelityClassic,
		},
		{
			name:        "everything off",
			dialEnabled: false,
			sessionMode: "",
			want:        moveFidelityClassic,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMoveFidelity(tc.dialEnabled, tc.sessionMode); got != tc.want {
				t.Fatalf("resolveMoveFidelity(%v, %q) = %v, want %v",
					tc.dialEnabled, tc.sessionMode, got, tc.want)
			}
		})
	}
}

// TestNewSessionStampsMoveHistoryMode pins the durable half's write
// side: a session created while the dial is on records "moves", one
// created while it is off records "classic", and a manager with no dial
// wired records "classic" (fail-closed).
//
// Mutation: drop the MoveHistoryMode line from Manager.createInternal —
// every session then reads back "" and TestResolveMoveFidelity's
// pre-existing rule silently disables the feature for everyone.
func TestNewSessionStampsMoveHistoryMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dial func() bool
		want string
	}{
		{"dial on", func() bool { return true }, string(session.MoveHistoryModeMoves)},
		{"dial off", func() bool { return false }, string(session.MoveHistoryModeClassic)},
		{"no dial wired (fail-closed)", nil, string(session.MoveHistoryModeClassic)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := session.NewManager(session.NewMemoryStore())
			if tc.dial != nil {
				mgr.SetMoveFidelityDial(tc.dial)
			}
			rec, err := mgr.Create(t.Context(), "s")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if rec.MoveHistoryMode != tc.want {
				t.Fatalf("MoveHistoryMode = %q, want %q", rec.MoveHistoryMode, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Two-layer redaction.
// ---------------------------------------------------------------------------

// TestModelHistory_DisplayArgsSummaryNeverReachesTheModel pins the half
// of spec §4's redaction contract that lives on THIS side of the seam:
// the tool_call row's Content is the display layer's args summary, and
// it must never be handed to the model as if it were assistant prose.
//
// Mutation: set `Content: r.Content` on the tool_use message projectRow
// builds and this fails — the model would then read
// "kenaz__read_file(path=<string>)" as something the assistant said.
func TestModelHistory_DisplayArgsSummaryNeverReachesTheModel(t *testing.T) {
	t.Parallel()
	got := composeModelHistory(
		modelHistoryRowsFrom(moveBearingRows(t)), moveFidelityMoves, 0)

	for _, m := range got {
		if m.Content == "kenaz__read_file(path=<string>)" {
			t.Fatalf("the DISPLAY layer's args summary reached the model layer as message content: %#v", m)
		}
	}
}

// TestModelHistory_ModelArgsNeverLandInTheDisplayField pins the other
// half, from the store side: WP02's invariant that
// session.ToolCall.Arguments stays nil must survive WP03 adding a raw-args
// carrier right next to it.
//
// Mutation: in llmHistoryWriter.AppendEntry, populate moveToolCalls'
// output with the model args instead of routing them to ModelToolArgs —
// this fails, and so does WP02's TestMoveToolCalls_NeverCarriesRawArguments.
func TestModelHistory_ModelArgsNeverLandInTheDisplayField(t *testing.T) {
	t.Parallel()
	for _, m := range moveBearingRows(t) {
		for _, tc := range m.ToolCalls {
			if len(tc.Arguments) > 0 {
				t.Fatalf("session.ToolCall.Arguments is populated on a %s row (%#v) — "+
					"raw arguments leaked into the DISPLAY layer's field",
					m.MoveKind(), tc)
			}
		}
	}
	// ...and the model layer really does hold them.
	var found bool
	for _, m := range moveBearingRows(t) {
		if args := m.ModelLayerToolArgs(); len(args) > 0 {
			found = true
			if args["toolu_01"] != `{"path":"foo.txt"}` {
				t.Fatalf("ModelLayerToolArgs = %v, want the raw model-emitted JSON", args)
			}
		}
	}
	if !found {
		t.Fatal("no row carries model-layer tool args — the model layer has nothing to compose from")
	}
}

// ---------------------------------------------------------------------------
// Multimodal (drains the WP02 ledger entry naming WP03 as a consumer).
// ---------------------------------------------------------------------------

// TestModelHistory_ContentBlocksAreNotReflattened pins the ledger's
// claim on WP03: "per-family rendering must not re-flatten a multimodal
// move on its way to the provider". Before WP03 the projection carried
// Role+Content only, so a row whose body lives in ContentBlocks reached
// the model empty.
//
// Mutation: delete the ContentBlocks branch in textMessage and this
// fails with an empty Content.
func TestModelHistory_ContentBlocksAreNotReflattened(t *testing.T) {
	t.Parallel()
	rows := []modelHistoryRow{{
		Role: "assistant",
		ContentBlocks: []corellm.ContentBlock{
			{Type: "text", Text: "described the image"},
		},
		MoveKind: string(session.MoveKindAssistantMove),
	}}
	got := composeModelHistory(rows, moveFidelityMoves, 0)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != "described the image" {
		t.Fatalf("Content = %q — the content-block body was dropped on the way to the provider",
			got[0].Content)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
