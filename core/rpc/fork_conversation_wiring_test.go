package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	branchesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/branches"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coreforkconv "github.com/kameas-ai/kenaz-harness/core/tools/forkconversation"
)

// newForkChassis boots a REAL chassis the way main.go does (rpc.New(c)
// over core.New with a DataDir), so sessions and branches persist through
// real sqlite — not session.NewMemoryStore (CLAUDE.md blind spot #2).
func newForkChassis(t *testing.T) (*API, *session.Manager) {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)
	sm := c.SessionManager()
	if sm == nil {
		t.Fatal("real chassis has no session manager")
	}
	return api, sm
}

func forkTool(t *testing.T, api *API) toolloop.BuiltinTool {
	t.Helper()
	tool, ok := api.Builtins().Lookup(coreforkconv.ToolName)
	if !ok {
		t.Fatalf("%s is not registered on a real chassis; registered: %v", coreforkconv.ToolName, api.Builtins().Names())
	}
	return tool
}

func callFork(t *testing.T, tool toolloop.BuiltinTool, sessionID, args string) map[string]any {
	t.Helper()
	raw, err := tool.Call(toolloop.WithSessionID(context.Background(), sessionID), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, raw)
	}
	return out
}

// TestForkConversationTool_RegisteredAndPredicateEnabled pins both sides
// of the registration-vs-consumption pair on the production wiring: the
// tool is in the registry AND builtinEnabledPredicate has an explicit,
// enabling case for it (the all-tools tripwire in builtins_wiring_test.go
// also sees it, because it walks the same registry).
func TestForkConversationTool_RegisteredAndPredicateEnabled(t *testing.T) {
	api, _ := newForkChassis(t)
	forkTool(t, api)
	pred := builtinEnabledPredicate(api.settingsImpl)
	logs := captureLog(t, func() {
		if !pred(coreforkconv.ToolName) {
			t.Errorf("builtinEnabledPredicate(%s) = false, want true", coreforkconv.ToolName)
		}
	})
	if strings.Contains(logs, "rpc.builtins.predicate.unknown_tool") {
		t.Errorf("%s fell through to the fail-closed default — no explicit predicate case", coreforkconv.ToolName)
	}
}

// TestForkConversationTool_GatingParityWithHumanFork: human branch
// creation (Bindings.Branches_Create) has no Settings dial, so the model
// tool must not be hidden behind one either — and must not be enabled by
// one. Flip every dial a neighbouring tool rides; the predicate answer
// does not move.
func TestForkConversationTool_GatingParityWithHumanFork(t *testing.T) {
	t.Parallel()
	api := settings.NewAPI(nil)
	store := api.Store()
	pred := builtinEnabledPredicate(api)
	for _, on := range []bool{false, true} {
		_ = store.SaveFSWriteEnabled(on)
		_ = store.SaveSaveArtifactEnabled(on)
		_ = store.SaveBash(on)
		_ = store.SaveTodoEnabled(on)
		if !pred(coreforkconv.ToolName) {
			t.Errorf("fork tool disabled with dials=%v; human Branches_Create has no dial, parity requires always-on", on)
		}
	}
}

// TestForkConversationTool_NotRegisteredWithoutManagers: a chassis with
// no conversation manager (rpc.New(nil)) must not advertise a tool that
// can only return ErrManagerUnavailable (FR-007).
func TestForkConversationTool_NotRegisteredWithoutManagers(t *testing.T) {
	t.Parallel()
	registry := toolloop.NewBuiltinRegistry()
	registerForkConversationTool(registry, branchesview.New(branchesview.Config{}), nil, false)
	if _, ok := registry.Lookup(coreforkconv.ToolName); ok {
		t.Fatal("fork tool registered without a conversation/session manager")
	}
}

// TestForkConversationTool_ForksCurrentSessionWithSeed_RealSqlite drives
// the registered tool end to end against real sqlite: the default branch
// point is the latest user/assistant message, the child carries the
// replayed history then the handoff, and the branch row is listed under
// the dispatch session with creation path model_tool.
func TestForkConversationTool_ForksCurrentSessionWithSeed_RealSqlite(t *testing.T) {
	api, sm := newForkChassis(t)
	ctx := context.Background()
	parent, err := sm.Create(ctx, "trunk")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []session.Message{
		{Role: session.RoleUser, Content: "q1"},
		{Role: session.RoleAssistant, Content: "a1"},
		{Role: session.RoleUser, Content: "please fork this"},
	} {
		if _, err := sm.AppendMessage(ctx, parent.ID, m); err != nil {
			t.Fatal(err)
		}
	}

	out := callFork(t, forkTool(t, api), parent.ID, `{"title":"Tangent","handoff":"Investigate option B."}`)
	if out["error"] != nil {
		t.Fatalf("fork failed: %v", out)
	}
	childID, _ := out["branch_session_id"].(string)
	branchID, _ := out["branch_id"].(string)
	if childID == "" || branchID == "" || out["title"] != "Tangent" || out["handoff_seeded"] != true {
		t.Fatalf("unexpected result: %v", out)
	}

	msgs, err := sm.ListMessages(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, string(m.Role)+":"+m.Content)
	}
	want := []string{"user:q1", "assistant:a1", "user:please fork this", "user:Investigate option B."}
	if len(got) != len(want) {
		t.Fatalf("child transcript = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("child transcript = %v, want %v", got, want)
		}
	}

	brs, err := api.Branches().ListBranches(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(brs) != 1 || brs[0].ID != branchID || brs[0].ChildSessionID != childID || brs[0].Title != "Tangent" {
		t.Fatalf("branches under parent = %+v", brs)
	}
	row, err := api.convMgr.Get(ctx, branchID)
	if err != nil {
		t.Fatal(err)
	}
	if row.CreationPath != branchesview.CreationPathModelTool {
		t.Errorf("persisted creation_path = %q, want %q", row.CreationPath, branchesview.CreationPathModelTool)
	}
}

// TestForkConversationTool_SessionScopeEnforced_RealSqlite: (a) a forged
// session argument is refused and creates nothing anywhere; (b) a
// from_message_id belonging to ANOTHER session cannot be used to reach
// across — it fails and creates nothing.
func TestForkConversationTool_SessionScopeEnforced_RealSqlite(t *testing.T) {
	api, sm := newForkChassis(t)
	ctx := context.Background()
	mine, _ := sm.Create(ctx, "mine")
	other, _ := sm.Create(ctx, "other")
	if _, err := sm.AppendMessage(ctx, mine.ID, session.Message{Role: session.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	otherMsg, err := sm.AppendMessage(ctx, other.ID, session.Message{Role: session.RoleUser, Content: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	tool := forkTool(t, api)

	out := callFork(t, tool, mine.ID, `{"title":"x","session_id":"`+other.ID+`"}`)
	if out["error"] != "invalid_args" {
		t.Errorf("forged session_id: error = %v, want invalid_args", out["error"])
	}

	out = callFork(t, tool, mine.ID, `{"title":"x","from_message_id":"`+otherMsg.ID+`"}`)
	if out["error"] != "fork_failed" {
		t.Errorf("cross-session from_message_id: error = %v, want fork_failed", out["error"])
	}

	for _, sid := range []string{mine.ID, other.ID} {
		brs, err := api.Branches().ListBranches(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		if len(brs) != 0 {
			t.Errorf("session %s has %d branches after refused forks, want 0", sid, len(brs))
		}
	}
}

// TestForkConversationTool_EmptySessionIsNothingToFork: no message, no
// fork — the tool says so rather than inventing an empty branch.
func TestForkConversationTool_EmptySessionIsNothingToFork(t *testing.T) {
	api, sm := newForkChassis(t)
	s, _ := sm.Create(context.Background(), "empty")
	out := callFork(t, forkTool(t, api), s.ID, `{"title":"x"}`)
	if out["error"] != "nothing_to_fork" {
		t.Fatalf("error = %v, want nothing_to_fork", out["error"])
	}
}

func TestDefaultForkAnchor(t *testing.T) {
	t.Parallel()
	msgs := []session.Message{
		{ID: "1", Role: session.RoleUser},
		{ID: "2", Role: session.RoleAssistant},
		{ID: "3", Role: session.RoleTool},
		{ID: "4", Role: session.RoleSystem},
		{ID: "5", Role: session.RoleUser}, // opens the live turn
		{ID: "6", Role: session.RoleTool}, // the live turn's tool_call
	}
	if got := defaultForkAnchor(msgs[:4], ""); got != "2" {
		t.Errorf("no span: got %q, want 2 (tool/system rows skipped)", got)
	}
	if got := defaultForkAnchor(msgs, "5"); got != "2" {
		t.Errorf("mid-turn: got %q, want 2 (last conversational row BEFORE the span)", got)
	}
	if got := defaultForkAnchor(msgs, "missing"); got != "5" {
		t.Errorf("unknown span: got %q, want 5 (falls back to latest)", got)
	}
	if got := defaultForkAnchor(msgs, "1"); got != "" {
		t.Errorf("first-turn span: got %q, want empty (nothing before the turn)", got)
	}
	if got := defaultForkAnchor(nil, ""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}

// callForkInTurn dispatches the tool the way the chat path does: session
// AND the live turn's span on the context (kernel_tool_adapter.go).
func callForkInTurn(t *testing.T, tool toolloop.BuiltinTool, sessionID, spanID, args string) map[string]any {
	t.Helper()
	ctx := toolloop.WithTurnSpanID(toolloop.WithSessionID(context.Background(), sessionID), spanID)
	raw, err := tool.Call(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("result not JSON: %v (%s)", err, raw)
	}
	return out
}

func transcriptOf(t *testing.T, sm *session.Manager, sessionID string) []string {
	t.Helper()
	msgs, err := sm.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, string(m.Role)+":"+m.Content)
	}
	return out
}

// TestForkConversationTool_MidTurnDefaultAnchorPrecedesLiveTurn (review
// M1): called inside a live turn, the default branch point is the last
// row BEFORE the turn's opening user message, so the branch does not end
// on the open "please fork this" request; the handoff then reads as the
// branch's opening instruction.
func TestForkConversationTool_MidTurnDefaultAnchorPrecedesLiveTurn(t *testing.T) {
	api, sm := newForkChassis(t)
	ctx := context.Background()
	parent, _ := sm.Create(ctx, "trunk")
	for _, m := range []session.Message{
		{Role: session.RoleUser, Content: "q1"},
		{Role: session.RoleAssistant, Content: "a1"},
	} {
		if _, err := sm.AppendMessage(ctx, parent.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	req, err := sm.AppendMessage(ctx, parent.ID, session.Message{Role: session.RoleUser, Content: "please fork this"})
	if err != nil {
		t.Fatal(err)
	}

	out := callForkInTurn(t, forkTool(t, api), parent.ID, req.ID, `{"title":"Tangent","handoff":"Investigate option B."}`)
	if out["error"] != nil {
		t.Fatalf("fork failed: %v", out)
	}
	got := transcriptOf(t, sm, out["branch_session_id"].(string))
	want := []string{"user:q1", "assistant:a1", "user:Investigate option B."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("child transcript = %v, want %v (must not end on the open fork request)", got, want)
	}

	// An explicit from_message_id still overrides the default.
	out = callForkInTurn(t, forkTool(t, api), parent.ID, req.ID, `{"title":"At request","from_message_id":"`+req.ID+`"}`)
	if out["error"] != nil {
		t.Fatalf("explicit fork failed: %v", out)
	}
	if got := transcriptOf(t, sm, out["branch_session_id"].(string)); len(got) != 3 || got[2] != "user:please fork this" {
		t.Fatalf("explicit-anchor child = %v, want it to end on the named message", got)
	}

	// First turn: nothing precedes the live turn — say so, no empty branch.
	fresh, _ := sm.Create(ctx, "fresh")
	first, _ := sm.AppendMessage(ctx, fresh.ID, session.Message{Role: session.RoleUser, Content: "fork me"})
	if out := callForkInTurn(t, forkTool(t, api), fresh.ID, first.ID, `{"title":"x"}`); out["error"] != "nothing_to_fork" {
		t.Fatalf("first-turn fork: error = %v, want nothing_to_fork", out["error"])
	}
}

// buildToolUsingParent writes, through the production transcript seam, a
// session whose single turn is user -> assistant_move -> tool_call ->
// tool_result -> final — the shape the chat runner persists for any
// tool-using turn. The tool_call row's Content is the DISPLAY layer's
// args summary; the raw args live only in the model layer.
func buildToolUsingParent(t *testing.T, sm *session.Manager, name string) (session.Record, session.Message, session.Message) {
	t.Helper()
	ctx := context.Background()
	parent, err := sm.Create(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	user, err := sm.AppendTranscriptEntry(ctx, parent.ID, session.TranscriptEntry{Role: session.RoleUser, Content: "Where is the config read?"})
	if err != nil {
		t.Fatal(err)
	}
	span := user.ID
	for i, e := range []session.TranscriptEntry{
		{Role: session.RoleAssistant, Content: "Let me look.", Kind: session.MoveKindAssistantMove},
		{Role: session.RoleTool, Content: argsSummary, Kind: session.MoveKindToolCall,
			ToolCalls:     []session.ToolCall{{ID: "toolu_01", Name: "read_file"}},
			ModelToolArgs: map[string]string{"toolu_01": `{"path":"core/config/load.go"}`}},
		{Role: session.RoleTool, Content: "func Load() {...}", Kind: session.MoveKindToolResult,
			ToolCalls: []session.ToolCall{{ID: "toolu_01", Name: "read_file"}}},
	} {
		e.MoveIndex, e.TurnSpanID = i, span
		if _, err := sm.AppendTranscriptEntry(ctx, parent.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	final, err := sm.AppendTranscriptEntry(ctx, parent.ID, session.TranscriptEntry{
		Role: session.RoleAssistant, Content: "It is read in core/config/load.go.",
		Kind: session.MoveKindFinal, MoveIndex: 3, TurnSpanID: span,
	})
	if err != nil {
		t.Fatal(err)
	}
	return parent, user, final
}

// argsSummary is a display-layer summary string distinctive enough that
// finding it in model content is unambiguous.
const argsSummary = "path:string<<display-summary>>"

func composeFor(t *testing.T, sm *session.Manager, sessionID string, f moveFidelity) []coreag.Message {
	t.Helper()
	msgs, err := sm.ListMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return composeModelHistory(modelHistoryRowsFrom(msgs), f, 0)
}

// assertForkHistoryFaithful is the reviewer's H1 probe: compose the
// CHILD's model history at both fidelities and require (1) zero orphaned
// / id-less tool rows, (2) no display args summary in model content,
// (3) the wire conversion emits no tool_result without a tool_use_id,
// and (4) byte-for-byte the same composition as the parent's up to the
// anchor — the replay is faithful, not merely non-crashing.
func assertForkHistoryFaithful(t *testing.T, sm *session.Manager, parentID, childID string) {
	t.Helper()
	for _, f := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
		child := composeFor(t, sm, childID, f)
		parent := composeFor(t, sm, parentID, f)
		if !reflect.DeepEqual(child, parent) {
			t.Errorf("fidelity %d: child history differs from parent's\nchild:  %+v\nparent: %+v", f, child, parent)
		}
		calls, results := 0, 0
		for _, m := range child {
			if m.Role == "tool" {
				results++
				if m.ToolCallID == "" {
					t.Errorf("fidelity %d: tool message with empty ToolCallID: %+v", f, m)
				}
			}
			calls += len(m.ToolCalls)
			if strings.Contains(m.Content, "<<display-summary>>") {
				t.Errorf("fidelity %d: display args summary leaked into model content: %q", f, m.Content)
			}
		}
		if f == moveFidelityMoves && (calls != 1 || results != 1) {
			t.Errorf("moves fidelity: tool_use=%d tool_result=%d, want the pair (1/1) preserved", calls, results)
		}
		if f == moveFidelityClassic && (calls != 0 || results != 0) {
			t.Errorf("classic fidelity: tool_use=%d tool_result=%d, want none", calls, results)
		}
		for _, wm := range chat.KernelMessagesToWire(child) {
			for _, b := range wm.Content {
				if b.Type == "tool_result" && (b.ToolResult == nil || b.ToolResult.ToolUseID == "") {
					t.Errorf("fidelity %d: wire tool_result with no tool_use_id", f)
				}
			}
		}
	}

	// Move metadata survived and the span was remapped onto the CHILD's
	// own user row, so the transcript UI groups the turn correctly.
	msgs, _ := sm.ListMessages(context.Background(), childID)
	if len(msgs) != 5 {
		t.Fatalf("child rows = %d, want 5", len(msgs))
	}
	for _, m := range msgs[1:] {
		if m.MoveKind() == "" {
			t.Errorf("row %q lost its move kind", m.Content)
		}
		if m.TurnSpanID() != msgs[0].ID {
			t.Errorf("row %q span = %q, want child user row %q", m.Content, m.TurnSpanID(), msgs[0].ID)
		}
	}
	if args := msgs[2].ModelLayerToolArgs(); args["toolu_01"] == "" {
		t.Errorf("tool_call lost its model-layer args: %v", args)
	}
}

// TestForkReplay_ToolUsingSession_ModelTool_RealSqlite: the H1 probe on
// the model's fork path.
func TestForkReplay_ToolUsingSession_ModelTool_RealSqlite(t *testing.T) {
	api, sm := newForkChassis(t)
	parent, _, _ := buildToolUsingParent(t, sm, "tools")
	out := callFork(t, forkTool(t, api), parent.ID, `{"title":"branch"}`)
	if out["error"] != nil {
		t.Fatalf("fork failed: %v", out)
	}
	assertForkHistoryFaithful(t, sm, parent.ID, out["branch_session_id"].(string))
}

// TestForkReplay_ToolUsingSession_BranchFromThisTurn_RealSqlite: the
// SAME probe on the human "Branch from this turn" path (createExplicit ->
// Branches_Create with a ParentMessageID), which shared the bug.
func TestForkReplay_ToolUsingSession_BranchFromThisTurn_RealSqlite(t *testing.T) {
	api, sm := newForkChassis(t)
	parent, _, final := buildToolUsingParent(t, sm, "tools")
	br, err := NewBindings(api).Branches_Create(branchesview.CreateBranchOptions{
		ParentSessionID: parent.ID,
		ParentMessageID: final.ID,
		Title:           "human fork",
		CreationPath:    "explicit",
	})
	if err != nil {
		t.Fatalf("Branches_Create: %v", err)
	}
	assertForkHistoryFaithful(t, sm, parent.ID, br.ChildSessionID)
}

// TestPairIntegritySweep_DropsIdlessToolRows: branches forked before the
// faithful replay hold classic role:"tool" rows with no tool-call id. The
// composition must never ship them (a tool_result with no tool_use_id).
func TestPairIntegritySweep_DropsIdlessToolRows(t *testing.T) {
	t.Parallel()
	rows := []modelHistoryRow{
		{Role: "user", Content: "q"},
		{Role: "tool", Content: "path:string"},
		{Role: "tool", Content: "file body"},
		{Role: "assistant", Content: "a"},
	}
	for _, f := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
		for _, m := range composeModelHistory(rows, f, 0) {
			if m.Role == "tool" {
				t.Fatalf("fidelity %d: id-less tool row reached the model: %+v", f, m)
			}
		}
	}
}

// TestBranchesCreate_ClientCannotForgeInternalCreationPath (review L1):
// "model_tool" and "auto_act" are internal provenance; the binding arm
// coerces a client-supplied one rather than recording the forgery.
func TestBranchesCreate_ClientCannotForgeInternalCreationPath(t *testing.T) {
	api, sm := newForkChassis(t)
	ctx := context.Background()
	parent, _ := sm.Create(ctx, "p")
	m, _ := sm.AppendMessage(ctx, parent.ID, session.Message{Role: session.RoleUser, Content: "hi"})
	b := NewBindings(api)

	for _, forged := range []string{branchesview.CreationPathModelTool, "auto_act", "anything"} {
		anchored, err := b.Branches_Create(branchesview.CreateBranchOptions{
			ParentSessionID: parent.ID, ParentMessageID: m.ID, Title: "x", CreationPath: forged,
		})
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := b.Branches_Create(branchesview.CreateBranchOptions{
			ParentSessionID: parent.ID, Title: "y", CreationPath: forged,
		})
		if err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]string{anchored.ID: "explicit", legacy.ID: "unknown"} {
			row, err := api.convMgr.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if row.CreationPath != want {
				t.Errorf("forged %q: stored creation_path = %q, want %q", forged, row.CreationPath, want)
			}
		}
	}
	if got := branchesview.ClientCreationPath("edit_resend"); got != "edit_resend" {
		t.Errorf("edit_resend must pass through, got %q", got)
	}
}

// cycleBranches is a BranchesAPI whose CreateBranch fails the way
// CreateBranchAtMessage's depth-32 ancestor walk does.
type cycleBranches struct {
	branchesview.BranchesAPI
}

func (cycleBranches) CreateBranch(context.Context, branchesview.CreateBranchOptions) (branchesview.Branch, error) {
	return branchesview.Branch{}, conversation.ErrCycle
}

type staticLister []session.Message

func (s staticLister) ListMessages(context.Context, string) ([]session.Message, error) { return s, nil }

// TestBranchForker_DepthCapIsReportedAsDepthLimit (review L4).
func TestBranchForker_DepthCapIsReportedAsDepthLimit(t *testing.T) {
	t.Parallel()
	f := &branchForker{branches: cycleBranches{}, sessions: staticLister{{ID: "m1", Role: session.RoleUser}}}
	_, err := f.Fork(context.Background(), coreforkconv.ForkRequest{ParentSessionID: "p", Title: "t"})
	if !errors.Is(err, coreforkconv.ErrDepthLimit) {
		t.Fatalf("err = %v, want ErrDepthLimit", err)
	}
}
