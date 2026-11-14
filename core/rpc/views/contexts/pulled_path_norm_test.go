package contexts_test

// pulled_path_norm_test.go — the pulled-entry guards compare a NORMALISED
// path (backslashes to slashes, path.Clean), so "team/./_fleet/x" and
// "team//_fleet/x" are refused like "team/_fleet/x".

import (
	"context"
	"errors"
	"testing"

	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

var nonCanonicalPulled = []string{
	"team/./_fleet/x",
	"team//_fleet/x",
	"./org/_fleet/x",
	"/team/_fleet/x",
	"org/../org/_fleet/x",
	"team\\_fleet\\x",
	"team/_fleet/./x",
}

func TestPulledPath_NonCanonicalSpellingsAreReadOnly(t *testing.T) {
	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := contextsview.New(lib)
	ctx := context.Background()
	for _, p := range nonCanonicalPulled {
		if err := api.Save(ctx, p, "edited"); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
			t.Errorf("Save(%q) err = %v, want read-only", p, err)
		}
		if err := api.Delete(ctx, p); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
			t.Errorf("Delete(%q) err = %v, want read-only", p, err)
		}
		if err := api.Rename(ctx, "team/mine.md", p); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
			t.Errorf("Rename(-> %q) err = %v, want read-only", p, err)
		}
	}
	// Ordinary paths still work.
	if err := api.Save(ctx, "team/_fleetish/notes.md", "ok"); err != nil {
		t.Fatalf("Save ordinary: %v", err)
	}
}

func TestPulledPath_NonCanonicalSpellingsNotShareable(t *testing.T) {
	user := "user-a"
	api, push, _ := ledgerRig(t, &user)
	ctx := context.Background()
	for _, p := range nonCanonicalPulled {
		if _, err := api.Context_Publish(ctx, publishReq(p, "team", "team-backend")); !errors.Is(err, contextsview.ErrPulledEntryNotShareable) {
			t.Errorf("Publish(%q) err = %v, want ErrPulledEntryNotShareable", p, err)
		}
		if _, err := api.Context_Promote(ctx, p); !errors.Is(err, contextsview.ErrPulledEntryNotShareable) {
			t.Errorf("Promote(%q) err = %v, want ErrPulledEntryNotShareable", p, err)
		}
	}
	if push.calls != 0 {
		t.Fatalf("push calls = %d, want none", push.calls)
	}
}
