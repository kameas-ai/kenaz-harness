package fleet

import (
	"context"
	"encoding/json"
	"testing"

	gocedar "github.com/cedar-policy/cedar-go"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// E2E-11: a call blocked by the org's Fleet policy is reported with the
// bundle's publish metadata, not Cedar internals; the metadata follows the
// installed snapshot (cleared with it).
func TestApplyCedarDelta_FleetDenialCopyCarriesBundleMeta(t *testing.T) {
	e := baseEngine(t)
	denial := func() (string, bool) {
		d := e.Evaluate(context.Background(), gocedar.EntityUID{}, cedarpolicy.ActionUseTool,
			cedarpolicy.ToolUID("github", "search"), nil)
		return cedarpolicy.UserFacingDenial(d)
	}

	raw, _ := json.Marshal(map[string]any{
		"rules":              map[string]string{"r1": forbidGithubTool},
		"rule_text":          forbidGithubTool,
		"published_at":       "2026-10-31T12:00:00Z",
		"published_by_email": "admin@example.com",
	})
	if err := applyRaw(t, e, string(raw)); err != nil {
		t.Fatal(err)
	}
	msg, ok := denial()
	if want := "Blocked by your organization's Fleet policy (published by admin@example.com on 2026-10-31 12:00 UTC)"; !ok || msg != want {
		t.Fatalf("denial copy = %q,%v; want %q", msg, ok, want)
	}

	// Today's wire shape: published_at only.
	if err := applyRaw(t, e, deltaJSON(t, map[string]string{"r1": forbidGithubTool})); err != nil {
		t.Fatal(err)
	}
	if msg, _ := denial(); msg != "Blocked by your organization's Fleet policy (published 2026-10-31 00:00 UTC)" {
		t.Fatalf("denial copy = %q", msg)
	}

	// Cleared snapshot: no fleet rule, no fleet denial.
	if err := applyRaw(t, e, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := denial(); ok {
		t.Fatal("fleet denial copy after the snapshot was cleared")
	}
}
