package branches

// Fork inherits the parent's tool-exposure override and activated tool
// set (tool-context-budget-01TCBUD01 Q-F) on every CreateBranch path — the anchored path the
// "Branch from this turn" menu and kenaz__fork_conversation take, and
// the legacy "+ Fork" path — through real sqlite, read back after a
// close/reopen.

import (
	"context"
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

func openForkStack(t *testing.T, dir string) (storage.DB, *API, *session.Manager) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	convMgr := conversation.NewManager(conversation.NewSQLStore(conversation.NewStorageDB(db)), sessMgr)
	return db, New(Config{Conversations: convMgr, Sessions: sessMgr}), sessMgr
}

// TestCreateBranch_ChildInheritsToolExposure: each fork's child holds
// the parent's session override — a tool the user turned off for the
// parent resolves off in the child — and its activations at fork time,
// sticky kept, LastUsedTurn reset to the child's own turn count (0); the
// parent is unchanged; a parent with neither gives a child with neither.
//
// Mutation: drop the InheritToolExposure call from either
// conversation.Manager fork method -> that path's child has no override
// and no activations and this fails.
func TestCreateBranch_ChildInheritsToolExposure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, api, sessMgr := openForkStack(t, dir)

	parent, err := sessMgr.Create(ctx, "parent")
	if err != nil {
		t.Fatal(err)
	}
	var anchor string
	for _, role := range []session.Role{"user", "assistant"} {
		m, err := sessMgr.AppendMessage(ctx, parent.ID, session.Message{Role: role, Content: string(role)})
		if err != nil {
			t.Fatal(err)
		}
		anchor = m.ID
	}
	parentActs := []toolexposure.Activation{
		{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 7},
		{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 3, Sticky: true},
	}
	if err := sessMgr.SetToolActivations(ctx, parent.ID, parentActs); err != nil {
		t.Fatal(err)
	}
	parentOff := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"secret": {Tier: toolexposure.TierOff}}}
	if err := sessMgr.SetToolExposure(ctx, parent.ID, parentOff); err != nil {
		t.Fatal(err)
	}
	bare, err := sessMgr.Create(ctx, "no activations")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessMgr.AppendMessage(ctx, bare.ID, session.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}

	forks := map[string]CreateBranchOptions{
		"branch from this turn": {ParentSessionID: parent.ID, ParentMessageID: anchor},
		"fork_conversation":     {ParentSessionID: parent.ID, ParentMessageID: anchor, CreationPath: "model_tool"},
		"legacy + Fork":         {ParentSessionID: parent.ID, Title: "side quest"},
	}
	children := map[string]string{}
	for name, opts := range forks {
		br, err := api.CreateBranch(ctx, opts)
		if err != nil {
			t.Fatalf("%s: CreateBranch: %v", name, err)
		}
		children[name] = br.ChildSessionID
	}
	bareBr, err := api.CreateBranch(ctx, CreateBranchOptions{ParentSessionID: bare.ID, Title: "bare"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}

	db, _, sessMgr = openForkStack(t, dir)
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	want := []toolexposure.Activation{
		{Name: "outlook__send-mail", Server: "outlook", LastUsedTurn: 0},
		{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 0, Sticky: true},
	}
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: noUserExposure{},
		Sessions: sessMgr,
		Projects: noProjectExposure{},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog := []toolexposure.CatalogTool{{Name: "secret__dump", Server: "secret", Running: true}}
	for name, child := range children {
		st, err := sessMgr.SessionToolExposure(ctx, child)
		if err != nil {
			t.Fatalf("%s: read child: %v", name, err)
		}
		if !reflect.DeepEqual(st.Activations, want) {
			t.Errorf("%s: child activations = %+v, want %+v", name, st.Activations, want)
		}
		rc, err := resolver.Resolve(ctx, child, catalog)
		if err != nil {
			t.Fatalf("%s: resolve child: %v", name, err)
		}
		if rt, _ := rc.Tool("secret__dump"); rt.Tier != toolexposure.TierOff || rt.Source != toolexposure.LevelSession {
			t.Errorf("%s: child resolves secret__dump %s from %s, want off from the inherited session override", name, rt.Tier, rt.Source)
		}
	}
	st, err := sessMgr.SessionToolExposure(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.Activations, parentActs) {
		t.Errorf("parent activations changed by the fork: %+v", st.Activations)
	}
	st, err = sessMgr.SessionToolExposure(ctx, bareBr.ChildSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Activations) != 0 || !st.Override.IsZero() {
		t.Errorf("child of a bare parent has activations %+v, override %+v", st.Activations, st.Override)
	}
}

type noUserExposure struct{}

func (noUserExposure) GetToolExposure(context.Context) (toolexposure.Settings, error) {
	return toolexposure.Settings{}, nil
}

type noProjectExposure struct{}

func (noProjectExposure) ProjectToolExposure(context.Context, string) (toolexposure.Exposure, error) {
	return toolexposure.Exposure{}, nil
}
