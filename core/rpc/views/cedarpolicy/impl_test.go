package cedarpolicy_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/cedarpolicy"
)

// TestAPI_NilEngine_ReturnsEmpty asserts the boot-time graceful
// degradation: a view constructed with no engine returns empty
// slices and an explicit error on reload, but never panics.
func TestAPI_NilEngine_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	api := cedarpolicy.NewAPI(nil)
	files, err := api.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies err: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("ListPolicies len=%d, want 0", len(files))
	}
	if err := api.ReloadPolicies(context.Background()); err == nil {
		t.Fatalf("ReloadPolicies should error when engine nil")
	}
	d, err := api.RecentDecisions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentDecisions err: %v", err)
	}
	if len(d) != 0 {
		t.Fatalf("RecentDecisions len=%d, want 0", len(d))
	}
}

func TestAPI_WithEngine_ListAndDecisions(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	// Issue an evaluate to populate the decision log.
	_ = e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionToolExec,
		cedar.ToolUID("websearch", "search"),
		nil,
	)
	api := cedarpolicy.NewAPI(e)

	files, err := api.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least the embedded default policy file")
	}

	if err := api.ReloadPolicies(context.Background()); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	d, err := api.RecentDecisions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentDecisions: %v", err)
	}
	if len(d) != 1 {
		t.Fatalf("RecentDecisions len=%d, want 1", len(d))
	}
	if d[0].Action != cedar.ActionToolExec {
		t.Fatalf("decision action = %q, want tool_exec", d[0].Action)
	}
}
