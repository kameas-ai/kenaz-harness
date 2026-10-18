package rpc

// model-scheduled-jobs-01PMSJ01 WP10 — harness_write_create_scheduled_run.
//
// Before this WP, harness.Managers had no ScheduledRunWriter field:
// scheduledchatview.API.CreateAsModel (WP09) was a real, gate-enforced
// entry point with zero production callers — the model could never
// reach it, so "the model MAY schedule jobs to run" (the owner ruling
// this whole mission exists to satisfy) was still false in the shipped
// binary. These tests drive the REAL production wire end to end:
// api.dispatchPool.Call(harnessmcp.ServerName, ToolCreateScheduledRun, ...)
// -> the attached harness-self server -> handleCreateScheduledRun ->
// scheduledRunWriterAdapter (core/rpc/harness_wiring.go) ->
// scheduledchatview.API.CreateAsModel -> real sqlite
// (scheduler.SQLiteChatStore). Nothing here re-derives WP09's gate
// logic; it proves the TOOL surface reaches it intact.
//
// No onboarding session is created in these tests: per
// harness_graph_authoring_tool_test.go's doc comment,
// api.dispatchPool.Call does not itself consult the session-kind
// containment (that lives in the separate chat.kernelToolAdapter
// dispatch path, proven elsewhere) — these tests need the
// scheduled-run Cedar gate's own decision, which is orthogonal.

import (
	"context"
	"encoding/json"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// TestHarnessSelfAttach_WP10_ToolCreatesModelOwnedScheduledRun is the
// mission's core positive proof: a model-facing tool call, through the
// real attached server, produces a real scheduled_chat_runs row with
// created_by="model" and the declared tool_allowlist — not just an
// OK:true tool result.
//
// Mutation: revert buildHarnessManagers' scheduledChatAPI wiring (drop
// the `if scheduledChatAPI != nil { m.ScheduledRunWriter = ... }`
// block, or the call-site argument in api.go). Must fail: the handler
// falls back to errNotConfigured and IsError becomes true.
func TestHarnessSelfAttach_WP10_ToolCreatesModelOwnedScheduledRun(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")

	before, err := api.ScheduledChat().List(context.Background())
	if err != nil {
		t.Fatalf("ScheduledChat().List (before): %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"name":           "WP10 test schedule",
		"promptTemplate": "summarise unread messages",
		"cron":           "0 9 * * *",
		"toolAllowlist":  []string{"harness_read_list_providers"},
	})
	callHarnessTool(t, api, harnessmcp.ToolCreateScheduledRun, args)

	after, err := api.ScheduledChat().List(context.Background())
	if err != nil {
		t.Fatalf("ScheduledChat().List (after): %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("scheduled run count = %d, want %d (before=%d) — the tool's OK result did not correspond to a real row",
			len(after), len(before)+1, len(before))
	}

	var foundIdx = -1
	for i, e := range after {
		if e.Name == "WP10 test schedule" {
			foundIdx = i
			break
		}
	}
	if foundIdx < 0 {
		t.Fatalf("ScheduledChat().List (after) = %+v, want to find \"WP10 test schedule\"", after)
	}
	found := after[foundIdx]
	if found.CreatedBy != scheduler.ScheduledRunCreatedByModel {
		t.Errorf("CreatedBy = %q, want %q — FR-005 requires server-side provenance, and the tool must go through CreateAsModel, never Create", found.CreatedBy, scheduler.ScheduledRunCreatedByModel)
	}
	if found.Cron != "0 9 * * *" {
		t.Errorf("Cron = %q, want %q", found.Cron, "0 9 * * *")
	}
	if len(found.ToolAllowlist) != 1 || found.ToolAllowlist[0] != "harness_read_list_providers" {
		t.Errorf("ToolAllowlist = %v, want [harness_read_list_providers]", found.ToolAllowlist)
	}
}

// TestHarnessSelfAttach_WP10_MissingToolAllowlistRefused pins owner
// ruling B-3 ("PERMIT ONLY WITHIN A TOOL ALLOWLIST") at the tool
// surface: a model-facing create call with no tool_allowlist must be
// refused, and refused BEFORE any row is written — not merely
// refused-with-a-row-left-behind.
//
// Mutation: drop the `len(p.ToolAllowlist) == 0` check from
// handleCreateScheduledRun (or the equivalent check inside
// scheduledchatview.API.CreateAsModel). Must fail: a row appears with
// an empty tool_allowlist and CreatedBy="model", which
// GateScheduledChatExecute's own doc names as the fail-safe's central
// hazard (a model-created schedule with no declared allowlist that
// could still fire).
func TestHarnessSelfAttach_WP10_MissingToolAllowlistRefused(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")

	before, err := api.ScheduledChat().List(context.Background())
	if err != nil {
		t.Fatalf("ScheduledChat().List (before): %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"name":           "WP10 no-allowlist schedule",
		"promptTemplate": "do something unattended",
		"cron":           "0 9 * * *",
		// toolAllowlist deliberately omitted.
	})
	res := callHarnessToolRaw(t, api, "", harnessmcp.ToolCreateScheduledRun, args)
	if !res.IsError {
		t.Fatalf("Call(%s) with no tool_allowlist did not report IsError — a model-created schedule with no allowlist must be refused", harnessmcp.ToolCreateScheduledRun)
	}

	after, err := api.ScheduledChat().List(context.Background())
	if err != nil {
		t.Fatalf("ScheduledChat().List (after): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("scheduled run count changed from %d to %d — a refused create wrote a row anyway", len(before), len(after))
	}
}

// TestHarnessSelfAttach_WP10_CreatedByModelForbidPolicyBlocksTool is
// the third leg tasks.md's WP10 asks for: "a policy forbidding
// model-created schedules blocks it" — driven through the ACTUAL tool
// surface, not just scheduledchatview.API directly (that lower-level
// path is already pinned by
// TestCedarWiring_ScheduledChatCreate_DenyPolicyIsEnforced in
// api_cedar_gate_wiring_test.go). The forbid rule is scoped to
// context.created_by == "model" specifically — proving the policy
// surface WP09 built (not a blanket forbid) is what the tool's call
// reaches, and that a user-authored create under the SAME policy still
// succeeds.
//
// Mutation: drop the `context.created_by` attribute from
// GateScheduledChatCreate's Evaluate call (cedar/hooks.go). Must fail:
// the forbid rule below can no longer distinguish user- from
// model-provenance, so either both are denied (breaking the second half
// of this test) or Cedar's own `when` clause fails to match and the
// create wrongly succeeds.
func TestHarnessSelfAttach_WP10_CreatedByModelForbidPolicyBlocksTool(t *testing.T) {
	policy := "forbid (\n" +
		"    principal == User::\"local\",\n" +
		"    action == Action::\"" + cedar.ActionScheduledRunCreate + "\",\n" +
		"    resource\n" +
		") when {\n" +
		"    context.created_by == \"model\"\n" +
		"};\n"
	api := cedarWiringAPI(t, policy)

	args, _ := json.Marshal(map[string]any{
		"name":           "WP10 forbidden schedule",
		"promptTemplate": "should never be created",
		"cron":           "0 9 * * *",
		"toolAllowlist":  []string{"harness_read_list_providers"},
	})
	res := callHarnessToolRaw(t, api, "", harnessmcp.ToolCreateScheduledRun, args)
	if !res.IsError {
		t.Fatalf("Call(%s) under a created_by==model forbid policy did not report IsError", harnessmcp.ToolCreateScheduledRun)
	}

	list, err := api.ScheduledChat().List(context.Background())
	if err != nil {
		t.Fatalf("ScheduledChat().List: %v", err)
	}
	for _, e := range list {
		if e.Name == "WP10 forbidden schedule" {
			t.Fatalf("forbidden model-created schedule was persisted anyway: %+v", e)
		}
	}

	// The same policy must NOT block a user-authored create — proving
	// the forbid rule is scoped to created_by=="model", not a blanket
	// deny that would make this test's first half vacuous.
	if _, err := api.ScheduledChat().Create(context.Background(), scheduledChatInput()); err != nil {
		t.Fatalf("user-authored scheduled chat create was denied under a created_by==model-scoped policy: %v", err)
	}
}
