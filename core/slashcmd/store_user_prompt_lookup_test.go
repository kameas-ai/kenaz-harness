package slashcmd_test

// dogfood 2026-10-08 round 2: typing "/bughunt <text>" for a freshly
// created global prompt-kind command produced `slashcmd: unknown command:
// "bughunt"`. The suspects were the lookup (leading slash, project
// scoping, file<->row sync). This pins that the lookup is NOT the cause:
// a global prompt command saved through SaveUser is found by name with an
// empty project id AND with an unrelated session project id, and renders
// through Dispatch.Run. The real cause was the composer routing a
// no-input user command to the built-in registry (fixed in
// SessionsView.vue, pinned by SessionsView.userSlashRouting.spec.ts).

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

func TestStore_FreshGlobalPromptCommand_FoundAndRuns(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	if err := store.SaveUser(ctx, slashcmd.UserCommand{
		Name: "bughunt", Scope: slashcmd.ScopeGlobal, Kind: slashcmd.KindPrompt,
		Description: "triage a bug", Body: "Triage this bug.", ModelInvokable: true,
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	for _, projectID := range []string{"", "proj-unrelated"} {
		cmd, err := store.LoadUserOne(ctx, "bughunt", projectID)
		if err != nil {
			t.Fatalf("LoadUserOne(bughunt, %q): %v", projectID, err)
		}
		if cmd.Kind != slashcmd.KindPrompt || cmd.Body != "Triage this bug." {
			t.Errorf("LoadUserOne(%q) = kind %q body %q", projectID, cmd.Kind, cmd.Body)
		}
	}
	res, err := slashcmd.NewDispatch(store, nil).Run(ctx, "bughunt", nil, slashcmd.SessionContext{SessionID: "s"})
	if err != nil || res.Text != "Triage this bug." || res.Metadata["prompt_rendered"] != true {
		t.Fatalf("Run = %+v, %v; want the rendered prompt flagged prompt_rendered", res, err)
	}
}
