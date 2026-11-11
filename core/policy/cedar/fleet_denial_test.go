package cedar

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFleetDenialMessage(t *testing.T) {
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		m    *FleetPolicyMeta
		want string
	}{
		{nil, "Blocked by your organization's Fleet policy"},
		{&FleetPolicyMeta{}, "Blocked by your organization's Fleet policy"},
		{&FleetPolicyMeta{PublishedAt: at}, "Blocked by your organization's Fleet policy (published 2026-10-31 12:00 UTC)"},
		{&FleetPolicyMeta{PublishedBy: "admin@example.com"}, "Blocked by your organization's Fleet policy (published by admin@example.com)"},
		{&FleetPolicyMeta{PublishedBy: "admin@example.com", PublishedAt: at}, "Blocked by your organization's Fleet policy (published by admin@example.com on 2026-10-31 12:00 UTC)"},
	} {
		if got := FleetDenialMessage(tc.m); got != tc.want {
			t.Errorf("FleetDenialMessage(%+v) = %q, want %q", tc.m, got, tc.want)
		}
	}
}

// A use_tool denial by a fleet rule carries the user-facing copy (with the
// installed policy's metadata) and no Cedar internals; a local rule's
// denial does not, and its raw Error() text is unchanged.
func TestPolicyDeniedError_UserMessage(t *testing.T) {
	const forbid = `forbid(principal, action == Action::"use_tool", resource == Tool::"github__create_issue");`
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)

	t.Run("fleet rule", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.ReplaceHarnessSnippets(FleetTeamSnippetPrefix, map[string][]byte{
			FleetTeamSnippetPrefix + "r1": []byte(forbid),
		}); err != nil {
			t.Fatal(err)
		}
		SetFleetPolicyMeta([]string{"r1"}, &FleetPolicyMeta{PublishedAt: at})
		t.Cleanup(func() { SetFleetPolicyMeta(nil, nil) })
		err := CheckUseTool(t.Context(), e, "github", "create_issue")
		var pe *PolicyDeniedError
		if !errors.As(err, &pe) {
			t.Fatalf("want PolicyDeniedError, got %v", err)
		}
		msg := pe.UserMessage()
		if msg != "Blocked by your organization's Fleet policy (published 2026-11-07 09:30 UTC)" {
			t.Fatalf("UserMessage = %q", msg)
		}
		for _, internal := range []string{"Tool::", "use_tool", "forbid", "fleet-team/", "harness-self"} {
			if strings.Contains(msg, internal) {
				t.Fatalf("user copy leaks Cedar internals (%q): %q", internal, msg)
			}
		}
		// The raw detail is still on the error for logs.
		if !strings.Contains(pe.Error(), "fleet-team/r1") {
			t.Fatalf("raw error lost the matched policy: %q", pe.Error())
		}
		// Meta for another snapshot's rule ids is not attached.
		SetFleetPolicyMeta([]string{"r2"}, &FleetPolicyMeta{PublishedAt: at})
		if msg := pe.UserMessage(); msg != FleetDenialText {
			t.Fatalf("stale meta attached: %q", msg)
		}
		// Cleared meta → plain copy.
		SetFleetPolicyMeta(nil, nil)
		d := e.Evaluate(t.Context(), UserUID(), ActionUseTool, ToolUID("github", "create_issue"), nil)
		if got, ok := UserFacingDenial(d); !ok || got != FleetDenialText {
			t.Fatalf("UserFacingDenial = %q,%v; want %q,true", got, ok, FleetDenialText)
		}
	})

	t.Run("local rule", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if err := e.LoadHarnessSnippets(map[string][]byte{"local/r1": []byte(forbid)}); err != nil {
			t.Fatal(err)
		}
		err := CheckUseTool(t.Context(), e, "github", "create_issue")
		var pe *PolicyDeniedError
		if !errors.As(err, &pe) {
			t.Fatalf("want PolicyDeniedError, got %v", err)
		}
		if msg := pe.UserMessage(); msg != "" {
			t.Fatalf("local denial got fleet copy %q", msg)
		}
	})

	t.Run("fleet allow is not a denial", func(t *testing.T) {
		e, _ := newDiskEngine(t, nil)
		if _, ok := UserFacingDenial(e.Evaluate(t.Context(), UserUID(), ActionUseTool, ToolUID("kenaz", "bash"), nil)); ok {
			t.Fatal("non-deny decision produced denial copy")
		}
	})
}
