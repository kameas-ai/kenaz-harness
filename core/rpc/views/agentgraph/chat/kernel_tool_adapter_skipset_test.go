package chat

// Autonomy prompt-skip sets at the adapter seam
// (confirm-each-enforcement-01PMAG05 WP04).
//
// WP01 made confirm_each prompt. These tests pin what the autonomy knobs
// do to that prompt: AutoApproveFamilies skips it per family,
// destructiveActionPosture: cedar-only extends the skip to destructive
// tools, unclassifiable tools always prompt, and explicit Cedar deny
// remains the floor under every combination.

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// skipProbe runs one adapter call under the given knobs and verdict and
// reports whether the user was prompted and whether the call errored.
// The confirmation, if any, is approved — so a difference in `prompted`
// is attributable to the skip set and not to the answer.
func skipProbe(t *testing.T, knobs autonomy.ResolvedKnobs, policy, tool string) (prompted, isErr bool) {
	t.Helper()
	pool := &staticToolPool{server: "srv", tool: tool}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "srv", Tool: tool, Policy: policy, Reason: "test"},
	}

	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-skip").
		withConfirm(bus).
		withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })

	res, err := adapter.Call(context.Background(), coreag.ToolCall{Name: "srv__" + tool})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return prompted, res.IsError
}

// knobs builds a ResolvedKnobs with just the two fields the skip set
// reads, so each case states exactly what it is testing.
func knobs(posture autonomy.DestructivePosture, families ...string) autonomy.ResolvedKnobs {
	return autonomy.ResolvedKnobs{
		AutoApproveFamilies:      autonomy.NewFamilySet(families...),
		DestructiveActionPosture: posture,
	}
}

// FR-004: the skip is per family. Auto-approving reads must not
// auto-approve writes — the behaviour the old blanket check got wrong.
func TestPromptSkipSet_IsPerFamily(t *testing.T) {
	t.Parallel()

	readOnly := knobs(autonomy.DestructiveConfirm, autonomy.FamilyRead)

	if prompted, _ := skipProbe(t, readOnly, "confirm_each", "read_file"); prompted {
		t.Error("auto-approved read family still prompted")
	}
	if prompted, _ := skipProbe(t, readOnly, "confirm_each", "write_file"); !prompted {
		t.Error("write_file did not prompt under autoApproveFamilies=[read] — the skip is not per-family")
	}
	if prompted, _ := skipProbe(t, readOnly, "confirm_each", "echo"); !prompted {
		t.Error("echo (shell-safe) did not prompt under autoApproveFamilies=[read]")
	}
}

// destructiveActionPosture is the second real consumer: cedar-only adds
// the destructive family to the skip set, confirm keeps it out.
func TestPromptSkipSet_DestructivePosture(t *testing.T) {
	t.Parallel()

	// Everything the tier presets can name, but the posture still asks
	// about destructive actions.
	confirming := knobs(autonomy.DestructiveConfirm,
		autonomy.FamilyRead, autonomy.FamilyWrite, autonomy.FamilyShellSafe, autonomy.FamilyNetwork)
	if prompted, _ := skipProbe(t, confirming, "confirm_each", "delete_file"); !prompted {
		t.Error("destructiveActionPosture=confirm did not prompt for a destructive tool")
	}
	if prompted, _ := skipProbe(t, confirming, "confirm_each", "write_file"); prompted {
		t.Error("destructiveActionPosture=confirm prompted for a plain write")
	}

	cedarOnly := knobs(autonomy.DestructiveCedarOnly, autonomy.FamilyRead)
	if prompted, _ := skipProbe(t, cedarOnly, "confirm_each", "delete_file"); prompted {
		t.Error("destructiveActionPosture=cedar-only still prompted for a destructive tool")
	}
	// cedar-only adds destructive; it does not widen to unrelated families.
	if prompted, _ := skipProbe(t, cedarOnly, "confirm_each", "write_file"); !prompted {
		t.Error("cedar-only leaked into the write family")
	}
}

// Fail-closed: a tool the classifier cannot place prompts, even under
// the most permissive posture the harness can express.
func TestPromptSkipSet_UnclassifiableToolAlwaysPrompts(t *testing.T) {
	t.Parallel()

	everything := knobs(autonomy.DestructiveCedarOnly,
		autonomy.FamilyRead, autonomy.FamilyWrite, autonomy.FamilyShellSafe, autonomy.FamilyNetwork)
	if prompted, _ := skipProbe(t, everything, "confirm_each", "frobnicate"); !prompted {
		t.Fatal("an unclassifiable tool skipped the prompt — the classifier must fail closed")
	}
}

// The tier presets produce the escalating permissiveness they advertise:
// identical dispatch behaviour at every tier was the defect (spec §1.1).
func TestPromptSkipSet_TiersDiffer(t *testing.T) {
	t.Parallel()

	type want struct{ read, write, shell, destructive bool } // true == prompted
	cases := []struct {
		tier autonomy.Tier
		want want
	}{
		{autonomy.TierStrict, want{read: true, write: true, shell: true, destructive: true}},
		{autonomy.TierCautious, want{read: false, write: true, shell: true, destructive: true}},
		{autonomy.TierDefault, want{read: false, write: false, shell: true, destructive: true}},
		{autonomy.TierBold, want{read: false, write: false, shell: false, destructive: false}},
		{autonomy.TierAutonomous, want{read: false, write: false, shell: false, destructive: false}},
	}
	for _, tc := range cases {
		k := knobsFromTier(tc.tier)
		checks := []struct {
			tool string
			want bool
		}{
			{"read_file", tc.want.read},
			{"write_file", tc.want.write},
			{"echo", tc.want.shell},
			{"delete_file", tc.want.destructive},
		}
		for _, c := range checks {
			prompted, _ := skipProbe(t, k, "confirm_each", c.tool)
			if prompted != c.want {
				t.Errorf("tier %v, tool %q: prompted = %v, want %v", tc.tier, c.tool, prompted, c.want)
			}
		}
	}
}

// The classifier's shell-safe family is a curated inspection-only
// surface, not "anything shell-shaped" — so general execution tools
// prompt at EVERY tier, including the two whose presets name shell-safe.
//
// This is the review finding that made the family an allowlist: with a
// segment table, `bash` / `run_command` / `shell_exec` / `spawn_process`
// / `execute_sql` all classified shell-safe, so bold and autonomous
// auto-approved arbitrary shell execution and arbitrary SQL. The
// assertion below is the tripwire — if the classifier ever readmits
// them, the most permissive posture the harness can express stops
// asking about `bash`.
func TestPromptSkipSet_GeneralExecutionAlwaysPrompts(t *testing.T) {
	t.Parallel()

	execTools := []string{"bash", "sh", "exec", "run_command", "shell_exec", "spawn_process", "execute_sql"}
	for _, tier := range []autonomy.Tier{autonomy.TierBold, autonomy.TierAutonomous} {
		k := knobsFromTier(tier)
		if !k.AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
			t.Fatalf("precondition: tier %v must name shell-safe", tier)
		}
		for _, tool := range execTools {
			if prompted, _ := skipProbe(t, k, "confirm_each", tool); !prompted {
				t.Errorf("tier %v: %q skipped the prompt — shell-safe must not mean shell", tier, tool)
			}
		}
	}
}

// The shell-safe family is not orphaned by the restriction above: the
// curated names still classify into it, so autoApproveFamilies:
// [shell-safe] continues to mean something an operator can rely on.
func TestPromptSkipSet_ShellSafeFamilyStillReachable(t *testing.T) {
	t.Parallel()

	shellSafeOnly := knobs(autonomy.DestructiveConfirm, autonomy.FamilyShellSafe)
	for _, tool := range []string{"echo", "pwd", "whoami", "uname"} {
		if prompted, _ := skipProbe(t, shellSafeOnly, "confirm_each", tool); prompted {
			t.Errorf("autoApproveFamilies=[shell-safe] still prompted for %q — the family is orphaned", tool)
		}
	}
	// And it stays a narrow grant: naming shell-safe does not widen to
	// writes or to general execution.
	if prompted, _ := skipProbe(t, shellSafeOnly, "confirm_each", "write_file"); !prompted {
		t.Error("shell-safe leaked into the write family")
	}
	if prompted, _ := skipProbe(t, shellSafeOnly, "confirm_each", "bash"); !prompted {
		t.Error("shell-safe leaked into general execution")
	}
}

// FR-005: explicit Cedar deny is the floor under every tier and every
// knob combination, and it never prompts.
func TestPromptSkipSet_CedarDenyIsTheFloorEverywhere(t *testing.T) {
	t.Parallel()

	tiers := []autonomy.Tier{
		autonomy.TierStrict, autonomy.TierCautious, autonomy.TierDefault,
		autonomy.TierBold, autonomy.TierAutonomous,
	}
	tools := []string{"read_file", "write_file", "exec_command", "delete_file", "frobnicate"}
	for _, tier := range tiers {
		k := knobsFromTier(tier)
		for _, tool := range tools {
			prompted, isErr := skipProbe(t, k, "deny", tool)
			if !isErr {
				t.Errorf("tier %v, tool %q: deny was not honoured", tier, tool)
			}
			if prompted {
				t.Errorf("tier %v, tool %q: deny prompted the user", tier, tool)
			}
		}
	}
}

// promptSkipSet's translation from knobs is a copy, not a mapping table:
// the four canonical family names are shared by value between the
// autonomy and toolloop packages. If either side renames one, this test
// is the tripwire.
func TestPromptSkipSet_FamilyNamesAreSharedByValue(t *testing.T) {
	t.Parallel()

	pairs := [][2]string{
		{autonomy.FamilyRead, toolloop.FamilyRead},
		{autonomy.FamilyWrite, toolloop.FamilyWrite},
		{autonomy.FamilyShellSafe, toolloop.FamilyShellSafe},
		{autonomy.FamilyNetwork, toolloop.FamilyNetwork},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("family name drift: autonomy=%q toolloop=%q", p[0], p[1])
		}
	}

	set := promptSkipSet(knobs(autonomy.DestructiveCedarOnly, autonomy.FamilyRead, autonomy.FamilyNetwork))
	got := set.Sorted()
	want := []string{toolloop.FamilyDestructive, toolloop.FamilyNetwork, toolloop.FamilyRead}
	if len(got) != len(want) {
		t.Fatalf("skip set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("skip set = %v, want %v", got, want)
		}
	}
}
