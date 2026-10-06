package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
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

func TestLatestConversationalMessageID_SkipsToolAndSystemRows(t *testing.T) {
	t.Parallel()
	msgs := []session.Message{
		{ID: "1", Role: session.RoleUser},
		{ID: "2", Role: session.RoleAssistant},
		{ID: "3", Role: session.RoleTool},
		{ID: "4", Role: session.RoleSystem},
	}
	if got := latestConversationalMessageID(msgs); got != "2" {
		t.Errorf("got %q, want 2", got)
	}
	if got := latestConversationalMessageID(nil); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
