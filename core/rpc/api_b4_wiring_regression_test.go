package rpc

// B4 (unwired sweep, release/v0.72.0): behavioural regression protection
// for two of the three production wiring fields PR #308 shipped with zero
// real coverage.
//
// PR #308's own regression suites drove these fields by CONSTRUCTING the
// dependency themselves, never through core/rpc/api.go's real wiring:
//   - core/rpc/views/permissions/impl_test.go's newRealEngineAPI calls
//     permissions.New(Config{Engine: eng}) directly.
//   - Nothing drove contextsyncview.Impl.Gate at all.
// Both are the exact "test-side edit standing in for a production revert"
// shape CLAUDE.md's unwired-sweep doctrine warns is a false equivalence —
// deleting the one-line assignment at api.go left every prior test green.
//
// These tests drive the REAL rpc.New(c) construction path (hoistSiteAPI,
// shared with the WP05 hoist-site tests in
// api_cedar_engine_hoist_sites_test.go) and assert an OBSERVABLE
// behavioural effect, not merely that a field is non-nil — a static
// "field is assigned something" check (see check-cedar-gate-arguments.sh
// clause 5) cannot tell a correctly-wired collaborator from one wired to
// the wrong object; these can.
//
// The third field (chat.Config.SecretLookup, wired inside buildChatRunner)
// has no equivalent test here — see check-secret-lookup-wiring.sh's header
// for why a behavioural test for that field was judged too costly to add
// safely in this pass (driving a real tool-call round trip through
// buildChatRunner needs a scripted tool-calling LLM registry that exists
// today only inside the unexported chat package, and reconstructing an
// equivalent one at the rpc-package level all-but-guarantees drifting
// from the real dispatch path it is meant to prove reaches). That field's
// only regression protection is the static presence check.

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestB4_PermissionsEngineWiring_RevokeGrantReachesSharedEngine is the
// api.go-level counterpart to permissions/impl_test.go's
// TestRevokeGrant_ChangesEvaluateOutcome_FiveFamilies. That test proves
// permissions.API.RevokeGrant behaves correctly once handed a real
// engine; this one proves rpc.New actually HANDS it one.
//
// Falsification (verified by hand while writing this test): delete
// `Engine: permissionsEngine,` from api.go's permissionsview.Config{}
// literal (or revert to the pre-M1 `Engine: a.cedarEngine,` form with no
// nil guard — either way the Config's Engine ends up unset for this
// test's purposes) and this test fails: RevokeGrant deletes the .cedar
// grant file but the engine never reloads, so the SAME shared engine
// a.cedarGate() vends everywhere else keeps evaluating the deleted grant
// as Allow.
func TestB4_PermissionsEngineWiring_RevokeGrantReachesSharedEngine(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	ctx := context.Background()

	const pattern = "echo zz-b4-permissions-wiring"
	grantID := "bash_allow_zz_b4_permissions_wiring.cedar"
	body := "permit(\n  principal,\n  action == Action::\"run_bash_command\",\n  resource == BashCommand::\"" + pattern + "\"\n);\n"
	writeRawPolicy(t, dataDir, grantID, body)
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("initial ReloadPolicies: %v", err)
	}

	resource := cedar.BashCommandUID(pattern)
	before := api.cedarEngine.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, resource, nil)
	if before.Outcome != cedar.Allow {
		t.Fatalf("setup broken: pre-revoke Evaluate = %v, want Allow: %+v", before.Outcome, before)
	}

	if err := api.Permissions().RevokeGrant(ctx, grantID); err != nil {
		t.Fatalf("Permissions().RevokeGrant: %v", err)
	}

	after := api.cedarEngine.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, resource, nil)
	if after.Outcome == cedar.Allow {
		t.Fatalf("REGRESSION: revoked grant still permits the same call through the shared engine — "+
			"api.go's permissionsview.Config.Engine is not reaching a.cedarEngine (B4): %+v", after)
	}
}

// TestB4_ContextSyncGateWiring_ReachesSharedEngine is the api.go-level
// wiring proof for contextsyncview.Impl.Gate. No prior test in the repo
// drove this field at all.
//
// CheckContextSyncSessionPurge (core/policy/cedar/hooks.go) treats a
// literal nil Gate as an IMMEDIATE ALLOW ("g == nil { return nil }") —
// unlike most gate-hook helpers, which degrade a nil-through-AllowAll to
// a documented default-allow-but-still-evaluated posture. So the mutation
// this test is falsified by is not "starts denying everything" but the
// opposite and more dangerous failure: reverting `Gate: a.cedarGate(),`
// to an omitted field makes SessionSync_DeleteRemote bypass policy
// evaluation entirely — an operator-authored forbid rule against
// context_sync.session.purge would silently stop applying.
//
// Falsification (verified by hand): delete `Gate: a.cedarGate(),` from
// api.go's contextsyncview.Impl{} literal — the forbid-policy step below
// then fails to deny, because im.Gate is a true nil interface and
// CheckContextSyncSessionPurge never evaluates anything.
func TestB4_ContextSyncGateWiring_ReachesSharedEngine(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	cs := api.ContextSync()
	if cs == nil {
		t.Fatal("api.ContextSync() is nil — contextSyncAPI was not constructed")
	}

	// Default install: the shipped default_context_sync_policy.cedar
	// permit clears the gate, so the call proceeds past policy and fails
	// on the fleet layer instead (no fleet client configured in this test
	// chassis) — never "denied by policy".
	err := cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	t.Logf("pre-forbid SessionSync_DeleteRemote err=%v", err)
	if err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("context sync purge DENIED on a default install — the shipped default permit is not reaching the gate: %v", err)
	}

	res, serr := api.CedarPolicy().SavePolicy(ctx, "zz_b4_forbid_context_sync_purge.cedar", forbidPolicy(cedar.ActionContextSyncSessionPurge))
	if serr != nil || !res.OK {
		t.Fatalf("SavePolicy: ok=%v err=%v errs=%+v", res.OK, serr, res.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	err = cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	t.Logf("post-forbid SessionSync_DeleteRemote err=%v", err)
	if err == nil || !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("REGRESSION: context sync purge NOT refused after an in-session forbid+reload — "+
			"api.go's contextsyncview.Impl.Gate is not reaching the shared engine (B4): %v", err)
	}

	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_b4_forbid_context_sync_purge.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	err = cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	if err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("still denied after delete+reload: %v", err)
	}
}
