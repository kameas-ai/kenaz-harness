package cedar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Tests for CheckContextSyncSessionPurge / CheckContextSyncProjectPurge
// (fleet-enforcement-truth-01PMZ505 WP13, owner ruling G-7,
// docs/escalation-register-2026-08-19.md Part 10: "gate the DESTRUCTIVE
// operations now — purge and delete — fail-closed").
//
// The load-bearing property under test, mirroring
// scheduled_run_test.go's GateScheduledChatExecute suite: these helpers
// must NOT inherit enforce()'s NotApplicable -> Allow mapping. Every
// "must deny" case is paired with a "permitted when it should be" case so
// a deny is never mistaken for a general misconfiguration, and the
// "no shipped policy" case is asserted explicitly — that is the exact
// case that bit the scheduled-run gate during its own development.

// newEmbeddedPlusDiskEngine returns an Engine with the real embedded
// bundle (so default_context_sync_policy.cedar's shipped permit is in
// play) PLUS an operator-authored file layered on top from disk. Mirrors
// the pattern in engine_test.go's disk-forbid-overrides-embedded-permit
// case.
func newEmbeddedPlusDiskEngine(t *testing.T, diskSource string) *Engine {
	t.Helper()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "01_operator.cedar"), []byte(diskSource), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// ── Session purge ────────────────────────────────────────────────────────────

// TestCheckContextSyncSessionPurge_ShippedPolicyPermits is the positive
// case: the real embedded bundle (default_context_sync_policy.cedar)
// permits the local user to purge their own session — the existing
// pre-gate UX must keep working.
func TestCheckContextSyncSessionPurge_ShippedPolicyPermits(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := CheckContextSyncSessionPurge(context.Background(), e, "sess-1"); err != nil {
		t.Fatalf("expected Allow via shipped default policy, got denied: %v", err)
	}
}

// TestCheckContextSyncSessionPurge_ExplicitForbid_Denies proves the gate
// DISCRIMINATES rather than blanket-refusing: an operator-authored forbid
// rule, layered on top of the shipped permit, must actually deny.
func TestCheckContextSyncSessionPurge_ExplicitForbid_Denies(t *testing.T) {
	t.Parallel()
	forbid := `
forbid (
    principal,
    action == Action::"context_sync.session.purge",
    resource
);
`
	e := newEmbeddedPlusDiskEngine(t, forbid)

	err := CheckContextSyncSessionPurge(context.Background(), e, "sess-1")
	if err == nil {
		t.Fatal("expected deny with an operator forbid rule installed, got Allow")
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
	if pde.Decision.Outcome != Deny {
		t.Fatalf("Outcome=%s, want Deny", pde.Decision.Outcome)
	}
}

// TestCheckContextSyncSessionPurge_NoShippedPolicy_Denies is the case
// that bit the scheduled-run gate: with NO context_sync.session.purge
// rule installed at all (no embedded bundle, no disk files — simulating
// the shipped policy file being absent or an operator wiping the policy
// directory), the purge must STILL refuse, not silently default-allow.
// The precondition assertion (raw Evaluate == NotApplicable) proves this
// test denies for the right reason — that enforce()'s ordinary mapping
// would have produced Allow here, and this helper deliberately does not
// use enforce().
func TestCheckContextSyncSessionPurge_NoShippedPolicy_Denies(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	raw := e.Evaluate(context.Background(), UserUID(), ActionContextSyncSessionPurge, SessionUID("sess-1"), nil)
	if raw.Outcome != NotApplicable {
		t.Fatalf("precondition failed: expected NotApplicable with no policy installed, got %s", raw.Outcome)
	}

	err = CheckContextSyncSessionPurge(context.Background(), e, "sess-1")
	if err == nil {
		t.Fatal("expected deny with no shipped policy installed, got Allow")
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
	if pde.Decision.Outcome != NotApplicable {
		t.Fatalf("Decision.Outcome=%s, want NotApplicable (the case enforce() would have let through)", pde.Decision.Outcome)
	}
}

// TestCheckContextSyncSessionPurge_NilGate_Allows covers the pre-boot /
// test-chassis branch: no Gate object exists at all. This mirrors every
// other gate-hook helper's degrade-safe contract (nil means "no engine
// wired", not "engine wired and denying"). In production a.cedarGate()
// never returns a literal nil — it degrades to cedar.AllowAll{}, which
// DOES deny here (see the NoShippedPolicy test above) — so this branch is
// a test/pre-boot convenience, not a live bypass.
func TestCheckContextSyncSessionPurge_NilGate_Allows(t *testing.T) {
	t.Parallel()
	if err := CheckContextSyncSessionPurge(context.Background(), nil, "sess-1"); err != nil {
		t.Fatalf("expected Allow with nil gate, got denied: %v", err)
	}
}

// ── Project purge ────────────────────────────────────────────────────────────

func TestCheckContextSyncProjectPurge_ShippedPolicyPermits(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := CheckContextSyncProjectPurge(context.Background(), e, "proj-1"); err != nil {
		t.Fatalf("expected Allow via shipped default policy, got denied: %v", err)
	}
}

func TestCheckContextSyncProjectPurge_ExplicitForbid_Denies(t *testing.T) {
	t.Parallel()
	forbid := `
forbid (
    principal,
    action == Action::"context_sync.project.purge",
    resource
);
`
	e := newEmbeddedPlusDiskEngine(t, forbid)

	err := CheckContextSyncProjectPurge(context.Background(), e, "proj-1")
	if err == nil {
		t.Fatal("expected deny with an operator forbid rule installed, got Allow")
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
	if pde.Decision.Outcome != Deny {
		t.Fatalf("Outcome=%s, want Deny", pde.Decision.Outcome)
	}
}

func TestCheckContextSyncProjectPurge_NoShippedPolicy_Denies(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	raw := e.Evaluate(context.Background(), UserUID(), ActionContextSyncProjectPurge, ProjectUID("proj-1"), nil)
	if raw.Outcome != NotApplicable {
		t.Fatalf("precondition failed: expected NotApplicable with no policy installed, got %s", raw.Outcome)
	}

	err = CheckContextSyncProjectPurge(context.Background(), e, "proj-1")
	if err == nil {
		t.Fatal("expected deny with no shipped policy installed, got Allow")
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
	if pde.Decision.Outcome != NotApplicable {
		t.Fatalf("Decision.Outcome=%s, want NotApplicable (the case enforce() would have let through)", pde.Decision.Outcome)
	}
}

func TestCheckContextSyncProjectPurge_NilGate_Allows(t *testing.T) {
	t.Parallel()
	if err := CheckContextSyncProjectPurge(context.Background(), nil, "proj-1"); err != nil {
		t.Fatalf("expected Allow with nil gate, got denied: %v", err)
	}
}
