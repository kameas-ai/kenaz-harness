package cedar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newTestEngineWithSource writes source as a single on-disk policy file
// and returns an Engine loaded from it (with no embedded bundle, so the
// installed rule is the only rule in play). Mirrors the pattern in
// TestEngine_ReloadFromDisk (engine_test.go).
func newTestEngineWithSource(t *testing.T, source string) *Engine {
	t.Helper()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "01_test.cedar"), []byte(source), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// Tests for GateScheduledChatExecute's fail-safe (model-scheduled-jobs-
// 01PMSJ01 WP09, owner ruling B-3, spec.md §6.1, AC-011/AC-011b).
//
// The load-bearing property under test: for created_by=="model",
// enforce()'s ordinary NotApplicable -> Allow mapping must NOT govern.
// Every "must deny" case below is paired with the "and it is permitted
// when it should be" case per the mission brief's instruction, so a
// deny is never mistaken for a misconfiguration.

// TestGateScheduledChatExecute_ModelWithAllowlist_ShippedPolicyPermits
// is the positive half: the real embedded default bundle, a
// model-created row WITH a declared allowlist, must Allow.
func TestGateScheduledChatExecute_ModelWithAllowlist_ShippedPolicyPermits(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d, err := GateScheduledChatExecute(context.Background(), e, "run-1", "model", true)
	if err != nil {
		t.Fatalf("expected Allow (has_tool_allowlist=true), got denied: %v (decision=%+v)", err, d)
	}
	if d.Outcome != Allow {
		t.Fatalf("Outcome=%s, want Allow", d.Outcome)
	}
	if d.MatchedPolicy == "" {
		t.Fatal("expected MatchedPolicy to be set on Allow — default_scheduled_run_policy.cedar should have matched")
	}
}

// TestGateScheduledChatExecute_ModelWithoutAllowlist_ShippedPolicyDenies
// is F1's Cedar-context half: the shipped policy's own `when` clause
// requires has_tool_allowlist == true, so a model-created row with NO
// declared allowlist evaluates NotApplicable even with the shipped
// bundle installed — and the fail-closed wrapper must turn that into a
// deny, not a default-allow.
func TestGateScheduledChatExecute_ModelWithoutAllowlist_ShippedPolicyDenies(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d, err := GateScheduledChatExecute(context.Background(), e, "run-1", "model", false)
	if err == nil {
		t.Fatalf("expected deny (has_tool_allowlist=false), got Allow (decision=%+v)", d)
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
}

// TestGateScheduledChatExecute_ModelCreated_NoShippedPolicy_Refuses is
// AC-011b, the test that makes ruling B-3 non-vacuous: with NO
// tool.scheduled_run.execute rule installed at all (simulating the
// shipped policy file being absent/removed), a model-created run must
// still refuse. This is the "absence of a rule" proof the mission
// brief requires — a test that only installs a forbid rule proves
// Cedar works, not that the fail-safe holds.
func TestGateScheduledChatExecute_ModelCreated_NoShippedPolicy_Refuses(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Sanity: this engine really does evaluate the action as
	// NotApplicable, not Deny via some other rule — otherwise this test
	// would pass for the wrong reason.
	raw := e.Evaluate(context.Background(), UserUID(), ActionScheduledRunExecute, ScheduledChatRunUID("run-1"), nil)
	if raw.Outcome != NotApplicable {
		t.Fatalf("precondition failed: expected NotApplicable with no policy installed, got %s", raw.Outcome)
	}

	d, err := GateScheduledChatExecute(context.Background(), e, "run-1", "model", true)
	if err == nil {
		t.Fatalf("expected deny with no shipped policy installed, got Allow (decision=%+v)", d)
	}
	var pde *PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected *PolicyDeniedError, got %T: %v", err, err)
	}
}

// TestGateScheduledChatExecute_ModelCreated_NilGate_Refuses covers the
// pre-boot / nil-gate branch of the same fail-closed contract.
func TestGateScheduledChatExecute_ModelCreated_NilGate_Refuses(t *testing.T) {
	t.Parallel()
	d, err := GateScheduledChatExecute(context.Background(), nil, "run-1", "model", true)
	if err == nil {
		t.Fatalf("expected deny with nil gate, got Allow (decision=%+v)", d)
	}
	if d.Outcome != Deny {
		t.Fatalf("Outcome=%s, want Deny", d.Outcome)
	}
}

// TestGateScheduledChatExecute_UserCreated_Unaffected is the paired
// "and it is permitted when it should be" case for every deny test
// above: none of this WP's changes narrow the ordinary user-created
// path, on any of the three gate shapes (shipped policy, no policy, nil
// gate) that deny the model-created path.
func TestGateScheduledChatExecute_UserCreated_Unaffected(t *testing.T) {
	t.Parallel()

	t.Run("shipped_policy_no_allowlist", func(t *testing.T) {
		t.Parallel()
		e, err := NewEngine(Options{IncludeEmbedded: true})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		d, err := GateScheduledChatExecute(context.Background(), e, "run-1", "user", false)
		if err != nil {
			t.Fatalf("user-created run should default-allow, got denied: %v", err)
		}
		if d.Outcome != NotApplicable {
			t.Fatalf("Outcome=%s, want NotApplicable (default-allow via enforce())", d.Outcome)
		}
	})

	t.Run("no_policy_installed", func(t *testing.T) {
		t.Parallel()
		e, err := NewEngine(Options{IncludeEmbedded: false, LoadFromDisk: false})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		if _, err := GateScheduledChatExecute(context.Background(), e, "run-1", "user", false); err != nil {
			t.Fatalf("user-created run should default-allow even with no policy installed, got denied: %v", err)
		}
	})

	t.Run("nil_gate", func(t *testing.T) {
		t.Parallel()
		d, err := GateScheduledChatExecute(context.Background(), nil, "run-1", "user", false)
		if err != nil {
			t.Fatalf("user-created run should default-allow on nil gate, got denied: %v", err)
		}
		if d.Outcome != Allow {
			t.Fatalf("Outcome=%s, want Allow", d.Outcome)
		}
	})
}

// TestGateScheduledChatCreate_InjectsCreatedByContext proves the
// context attribute actually reaches Cedar (not just that the Go call
// compiles) — installs a policy that forbids create when
// context.created_by == "model" and observes a real deny, then the
// paired positive: the same gate permits a user-created row.
func TestGateScheduledChatCreate_InjectsCreatedByContext(t *testing.T) {
	t.Parallel()
	forbidModelCreate := `
forbid (
    principal,
    action == Action::"tool.scheduled_run.create",
    resource
) when {
    context.created_by == "model"
};
`
	e := newTestEngineWithSource(t, forbidModelCreate)

	d, err := GateScheduledChatCreate(context.Background(), e, "run-1", "model")
	if err == nil {
		t.Fatalf("expected deny for created_by=model, got Allow (decision=%+v)", d)
	}
	if d.Outcome != Deny {
		t.Fatalf("Outcome=%s, want Deny", d.Outcome)
	}

	// Paired positive: the same installed forbid rule does not touch
	// created_by=="user".
	d2, err := GateScheduledChatCreate(context.Background(), e, "run-2", "user")
	if err != nil {
		t.Fatalf("user-created row should not be denied by a model-only forbid rule: %v", err)
	}
	if d2.Outcome != NotApplicable {
		t.Fatalf("Outcome=%s, want NotApplicable (default-allow via enforce())", d2.Outcome)
	}
}
