package rpc

// harness_self_mcp_disabled_gates_attach_test.go closes AC-008's real gap,
// found during the harness-self-attach-01PMHS01 triage-and-finish pass
// (2026-09-12).
//
// TestHarnessSelfMCPDisabled_KillSwitchReadsTheStore
// (harness_self_mcp_disabled_wiring_test.go) already proved
// OnboardingAPI.State().HarnessSelfMCPDisabled tracks the persisted setting
// live — that closed the SettingsView.vue banner's half of FR-007. But
// nothing on the actual attach/dispatch/listing path ever consulted the
// flag: before the fix this test pins, flipping the setting to true
// changed only that one display field. A direct harness_read_get_status
// call still resolved to allow, and the tool still appeared in an
// onboarding session's listing — the exact "toggle that reports it is on"
// shape CLAUDE.md's unwired-sweep doctrine names. Verified by hand before
// the fix: this test failed against the pre-fix resolver.
//
// The fix folds the check into cedarSessionKindResolver.Resolve
// (core/rpc/harness_session_kind_resolver.go) — the same resolver that
// already governs harness-self reachability (C-004) and visibility
// (C-003) for every session kind — rather than adding a second
// enforcement point. That means: no restart needed (re-reads the real
// store on every call, like the session-kind check it sits beside), and
// it denies for EVERY session kind including onboarding, per FR-007's own
// text ("none of its tools appear in any session — including
// onboarding").
//
// Mutation: neutralise the kill-switch branch in
// cedarSessionKindResolver.Resolve (e.g. `if false && ...`). Verified by
// hand: this test fails — both the disabled-listing assertion and the
// disabled-resolve assertion go red.

import (
	"context"
	"encoding/json"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

func TestHarnessSelfMCPDisabled_ActuallyGatesReachabilityAndVisibility(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")
	store := api.SettingsStore()
	if store == nil {
		t.Fatal("nil settings store")
	}
	sessionMgr := c.SessionManager()
	rec, err := sessionMgr.CreateWithKind(context.Background(), "onboarding", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}

	// Baseline: enabled (the fresh-install default) — the direct call
	// must succeed and the onboarding session's listing must be non-empty.
	if _, err := api.dispatchPool.Call(context.Background(), harnessmcp.ServerName, harnessmcp.ToolListProviders, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("baseline Call: %v", err)
	}
	pool := harnessShapedPool{}
	discoverer := llm.NewMCPToolDiscoverer(pool, api.toolPermsResolver)
	specs, err := discoverer.Tools(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("baseline Tools: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("baseline: onboarding session listing is empty — fixture broken before the real assertion")
	}

	if err := store.SaveHarnessSelfMCPDisabled(true); err != nil {
		t.Fatalf("SaveHarnessSelfMCPDisabled(true): %v", err)
	}

	// AC-008: a direct harness_read_get_status-shaped resolve must deny,
	// for the ONBOARDING session too — "including onboarding" per FR-007.
	res, err := api.toolPermsResolver.Resolve(context.Background(), rec.ID, harnessmcp.ServerName, harnessmcp.ToolGetStatus)
	if err != nil {
		t.Fatalf("Resolve after disable: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("onboarding session harness_read_get_status policy = %q after disable, want %q", res.Policy, toolloop.PolicyDeny)
	}

	specs2, err := discoverer.Tools(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("Tools after disable: %v", err)
	}
	if len(specs2) != 0 {
		t.Errorf("expected 0 harness-self tools listed for onboarding session after disable, got %d: %+v", len(specs2), specs2)
	}

	// Flip back — no restart, live re-read through the same API instance.
	if err := store.SaveHarnessSelfMCPDisabled(false); err != nil {
		t.Fatalf("SaveHarnessSelfMCPDisabled(false): %v", err)
	}
	res2, err := api.toolPermsResolver.Resolve(context.Background(), rec.ID, harnessmcp.ServerName, harnessmcp.ToolGetStatus)
	if err != nil {
		t.Fatalf("Resolve after re-enable: %v", err)
	}
	if res2.Policy == toolloop.PolicyDeny {
		t.Fatal("re-enabled: harness_read_get_status still denied for onboarding session — kill switch is not re-read live")
	}
}
