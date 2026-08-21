package rpc

// harness-self-attach-01PMHS01 UNIT-6 — the attach. These tests drive
// the ACTUAL production wire: New()'s a.harnessServer, registered into
// a.dispatchPool's in-process transport arm (UNIT-5), evaluated
// against a.toolPermsResolver (UNIT-4). Before this unit,
// harnessServer.Server() (core/rpc/harness_wiring.go:290) had zero
// non-test callers and "harness-self" was never a name a.dispatchPool
// knew about — every test in this file failed until the attach block
// in New() (api.go, harnessServer construction) ran.

import (
	"context"
	"encoding/json"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// callHarnessTool dispatches through the REAL attached pool
// (api.dispatchPool.Call) and unwraps the MCP tools/call envelope,
// failing the test if the handler itself reported IsError — a Go-level
// nil error from Call only means the JSON-RPC round-trip succeeded;
// toolserver.Server.HandleEnvelope (core/mcp/builtin/toolserver/
// server.go) converts a handler error into IsError:true INSIDE a
// successful envelope, not into a Go error, so callers that check only
// `err != nil` silently pass even when every handler in the server
// returned errNotConfigured.
func callHarnessTool(t *testing.T, api *API, tool string, args json.RawMessage) transport.ToolsCallResult {
	t.Helper()
	raw, err := api.dispatchPool.Call(context.Background(), harnessmcp.ServerName, tool, args)
	if err != nil {
		t.Fatalf("Call(%s): %v — the server is not reachable through the attached pool", tool, err)
	}
	var result transport.ToolsCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Call(%s): decode ToolsCallResult: %v (raw=%s)", tool, err, raw)
	}
	if result.IsError {
		t.Fatalf("Call(%s): handler reported IsError=true: %s", tool, contentText(result))
	}
	return result
}

func contentText(r transport.ToolsCallResult) string {
	var out string
	for i, c := range r.Content {
		if i > 0 {
			out += " "
		}
		out += string(c)
	}
	return out
}

// TestHarnessSelfServerName_PinnedAgainstCedarPolicyText is C-008's
// regression pin: the three embedded Cedar policies match on the
// literal string "harness-self", and the attached server's name must
// be the SAME constant, not an independently-typed literal that could
// drift. This test reads the actual shipped .cedar file bytes (not a
// paraphrase of them) so a future rename of either side that breaks
// the match fails loudly here instead of silently ungating every
// harness_write_* tool via NotApplicable.
func TestHarnessSelfServerName_PinnedAgainstCedarPolicyText(t *testing.T) {
	if harnessmcp.ServerName != "harness-self" {
		t.Fatalf("harnessmcp.ServerName = %q, want \"harness-self\"", harnessmcp.ServerName)
	}
	snippets, err := harnessmcp.CedarSnippets()
	if err != nil {
		t.Fatalf("CedarSnippets: %v", err)
	}
	if len(snippets) != 3 {
		t.Fatalf("CedarSnippets returned %d file(s), want 3", len(snippets))
	}
	want := []byte(`context.server_name == "` + harnessmcp.ServerName + `"`)
	for name, body := range snippets {
		if !contains(body, want) {
			t.Errorf("%s does not match on context.server_name == %q (got %d bytes) — a server-name mismatch would make this policy NotApplicable and silently ungate the tool it governs", name, harnessmcp.ServerName, len(body))
		}
	}
}

func contains(haystack, needle []byte) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexBytes(haystack, needle) >= 0
}

func indexBytes(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// TestHarnessSelfAttach_AC001_OnboardingSessionReachesRealHandlers is
// AC-001: a kind=onboarding session's harness_read_list_providers and
// harness_write_create_project calls reach the real handlers through
// the real attached server, and the project actually exists in the
// projects store afterward — not just "OK: true" in the tool result.
//
// harness_read_get_status is NOT used here even though it's one of
// AC-001's named tools in tasks.md: Managers.Status (StatusReporter)
// has no adapter — see buildHarnessManagers' comment — so that tool
// returns errNotConfigured for every caller today, attach or no
// attach. That gap is pre-existing (unreachable, and so unobserved,
// before this unit) and is called out in the mission report rather
// than silently worked around by picking an easier assertion for a
// tool this test doesn't actually exercise.
//
// Mutation: revert the attach (drop the RegisterInProcess call, or
// register under a different name than "harness-self"). Both must
// fail: Call returns dispatch.ErrInProcessServerNotFound.
func TestHarnessSelfAttach_AC001_OnboardingSessionReachesRealHandlers(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")
	sessionMgr := c.SessionManager()
	rec, err := sessionMgr.CreateWithKind(context.Background(), "onboarding session", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}

	// harness_read_list_providers must reach the real handler.
	listResult := callHarnessTool(t, api, harnessmcp.ToolListProviders, json.RawMessage(`{}`))
	if len(listResult.Content) == 0 {
		t.Fatal("Call(harness_read_list_providers) returned no content")
	}

	// harness_write_create_project must reach the real handler AND
	// actually create the project — asserted through the real
	// projects store, not the tool's own success flag.
	before, err := api.Projects().List(context.Background())
	if err != nil {
		t.Fatalf("Projects().List (before): %v", err)
	}

	args, _ := json.Marshal(map[string]string{"name": "AC-001 project", "description": "created by TestHarnessSelfAttach_AC001"})
	callHarnessTool(t, api, harnessmcp.ToolCreateProject, args)

	after, err := api.Projects().List(context.Background())
	if err != nil {
		t.Fatalf("Projects().List (after): %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("project count = %d, want %d (before=%d) — harness_write_create_project's OK result did not correspond to a real project row", len(after), len(before)+1, len(before))
	}
	found := false
	for _, p := range after {
		if p.Name == "AC-001 project" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Projects().List (after) = %+v, want to find \"AC-001 project\"", after)
	}
	_ = rec // rec.ID unused beyond proving CreateWithKind succeeded; the dispatchPool.Call above is sessionless (server-level dispatch, not per-session — see §10.5).
}

// TestHarnessSelfAttach_AC002_ChatSessionWriteToolDenied is AC-002,
// the security criterion. This drives the REAL merged resolver
// (a.toolPermsResolver, UNIT-4) against the REAL attached "harness-self"
// server name (UNIT-6) for a kind=chat session — the "harness-self"
// name is no longer a fake pool's fiction (harnessShapedPool in
// harness_session_kind_resolver_wiring_test.go), it is the one
// a.dispatchPool actually has a live handler registered under.
//
// Scope note (do not read past this as a full kernelToolAdapter
// end-to-end test): the real dispatch-time enforcement point is
// chat.kernelToolAdapter.dispatch, which is unexported in a different
// package and — per its own case string(toolloop.PolicyDeny) branch —
// returns an IsError result and never calls the tool pool at all when
// Resolve reports Deny. That exact branch is independently pinned by
// TestKernelToolAdapter_UnrecognisedVerdictDeniesAtDispatch (AC-016,
// core/rpc/views/agentgraph/chat/kernel_tool_adapter_unrecognised_
// policy_test.go), which this test does not duplicate. What THIS test
// adds is the fact this unit changes: that a.toolPermsResolver, when
// asked about the REAL attached "harness-self" server for a kind=chat
// session, still denies harness_write_add_provider — i.e. the attach
// did not somehow make the containment inapplicable — and that the
// providers store has zero entries both before and after, as a
// regression pin against a resolver or dispatch bug that reached the
// handler despite the Deny.
//
// Mutation A: remove harness_write_forbid.cedar from the embedded set.
// Mutation B: skip UNIT-2's policy install.
// Mutation C: revert UNIT-4's merged resolver.
// Mutation D: make UNIT-4's construction conditional again.
// All four are exercised by TestHarnessContainment_* in
// harness_session_kind_resolver_wiring_test.go and
// harness_session_kind_resolver_test.go against the SAME
// a.toolPermsResolver this test reads — a regression in any of them
// fails those tests, not a mutation this test re-derives by hand.
func TestHarnessSelfAttach_AC002_ChatSessionWriteToolDenied(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")
	sessionMgr := c.SessionManager()
	rec, err := sessionMgr.Create(context.Background(), "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := api.LLMConnector().ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders (before): %v", err)
	}

	res, err := api.toolPermsResolver.Resolve(context.Background(), rec.ID, harnessmcp.ServerName, harnessmcp.ToolAddProvider)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("kind=chat harness_write_add_provider policy = %q, want %q — the real attached server name did not change the containment verdict", res.Policy, toolloop.PolicyDeny)
	}
	if res.Reason == "" {
		t.Error("Deny reason is empty — Resolution.Reason should carry the Cedar denial text (UNIT-3)")
	}

	// The production dispatch path (chat.kernelToolAdapter, proven
	// elsewhere per the scope note above) never calls the pool on
	// Deny. Confirm the providers store is untouched — defense in
	// depth against any OTHER path reaching the handler.
	after, err := api.LLMConnector().ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders (after): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("provider count changed from %d to %d — a provider was created despite the Deny", len(before), len(after))
	}
}

// TestHarnessSelfAttach_AC009b_AuditEmitted is AC-009(b): a real
// harness_read_list_providers call through the attached server emits
// KindHarnessSelfToolCalled on the REAL audit sink (a.auditImpl), with
// {tool_name, success, duration_ms}. Before this unit, WithAudit
// (core/mcp/builtin/harness/audit.go) had zero production callers —
// api.go built the server with harnessmcp.RegisterAll alone, so every
// harness-self dispatch was unaudited no matter how it was reached.
// (harness_read_get_status would exercise the same audit wrapper, but
// see AC-001's comment on why it can't be used as a positive control
// today.)
func TestHarnessSelfAttach_AC009b_AuditEmitted(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")
	sessionMgr := c.SessionManager()
	if _, err := sessionMgr.CreateWithKind(context.Background(), "onboarding session", nil, session.SessionKindOnboarding); err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}

	callHarnessTool(t, api, harnessmcp.ToolListProviders, json.RawMessage(`{}`))

	entries, err := api.auditImpl.ListEntries(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	var found *audit.Entry
	for i := range entries {
		if entries[i].Subject == "harness-self.tool.called" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no harness-self.tool.called entry found among %d audit entries", len(entries))
	}
	if found.Category != "MCP" {
		t.Errorf("Category = %q, want \"MCP\"", found.Category)
	}
	for _, want := range []string{"tool_name=" + harnessmcp.ToolListProviders, "success=true"} {
		if !stringsContains(found.Trailing, want) {
			t.Errorf("Trailing = %q, want it to contain %q", found.Trailing, want)
		}
	}
	if !stringsContains(found.Trailing, "duration_ms=") {
		t.Errorf("Trailing = %q, want it to contain duration_ms=", found.Trailing)
	}
}

func stringsContains(s, substr string) bool {
	return len(substr) == 0 || indexBytes([]byte(s), []byte(substr)) >= 0
}
