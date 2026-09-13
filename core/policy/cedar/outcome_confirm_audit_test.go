package cedar

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// fixedOutcomeGate returns a canned Decision for every Evaluate call.
// Used to exercise the WP01 "Confirm is handled explicitly" property on
// call sites nothing in production sends Confirm through today — Cedar's
// own Engine.Evaluate (decisions.go's fromCedar) never produces Confirm,
// so a fake Gate is the only way to construct the case at all.
type fixedOutcomeGate struct {
	decision Decision
	calls    int
}

func (g *fixedOutcomeGate) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) Decision {
	g.calls++
	out := g.decision
	out.Action = action
	out.Principal = principal.String()
	out.Resource = resource.String()
	return out
}

// TestOutcomeConfirmAuditLedger_* enumerate every production switch/if on
// cedar.Outcome that WP01 (risk-rated-autonomy-01PMRA01) audited, pinning
// that each treats Confirm explicitly rather than folding it into "not
// deny, so allow" — the exact fail-open shape enforce() used to have for
// NotApplicable, one layer up.
//
// Nothing in production sends Confirm through ANY of the four hooks.go
// call sites tested below today: layer 3 (cedar.ThreeLayerResolve, added
// in WP02) is wired only into the chat kernel tool adapter's own
// confirm-each ladder, which calls ThreeLayerResolve directly rather than
// routing through any of enforce/CheckCredentialAccess/GateMCPSpawn/
// CheckAuditBulkPurge. Every assertion here is a defensive-regression pin
// for a case with no live producer yet, not a description of a reachable
// production path — recorded explicitly because a green test that
// exercises an unreachable branch and a green test that exercises a real
// one look identical from the outside, and this repo's most common
// defect is a check that "passes on something adjacent to the property
// it claims."
//
// Full ledger of the switch/if audit performed for WP01 (six production
// switches, plus two additional non-switch "if outcome==Deny then allow"
// sites found and deliberately left untouched):
//
//  1. enforce()                    (hooks.go)        — tested below.
//  2. CheckCredentialAccess()      (hooks.go)        — tested below.
//  3. GateMCPSpawn()               (hooks.go)         — tested below.
//  4. CheckAuditBulkPurge()        (hooks.go)        — tested below.
//  5. Tool.cedarGate()             (core/tools/bash/bash.go) — Confirm
//     case added and documented, but UNREACHABLE from a real *cedar.Engine
//     (the field is the concrete *Engine type, not the Gate interface, and
//     Engine.Evaluate never emits Confirm) — no behavioural test is
//     possible without a larger refactor out of WP01's scope. Left as a
//     defensive, honestly-uncovered branch; see the doc comment at that
//     switch for the reasoning.
//  6. Gate.evaluate()              (core/tools/fs/gate.go) — same
//     unreachable-via-concrete-Engine shape as #5.
//
// Deliberately NOT modified (found during the audit, out of this
// mission's scope — layer 3 rates tool/MCP dispatch only, per the spec's
// non-goals): LLMPolicyGuard.Allow (llmguard.go) and the two
// credstore/refs + harness_session_kind_resolver "if outcome==Deny then
// allow" sites gate model-select / secret-ref-resolve / session-kind
// actions, none of which are tool/MCP dispatch.
func TestOutcomeConfirmAuditLedger_enforce(t *testing.T) {
	t.Parallel()
	err := enforce(Decision{Outcome: Confirm})
	if err == nil {
		t.Fatal("enforce(Confirm) = nil, want a *PolicyDeniedError — Confirm must never be treated as allow")
	}
	if _, ok := err.(*PolicyDeniedError); !ok {
		t.Fatalf("enforce(Confirm) = %T, want *PolicyDeniedError", err)
	}
}

// TestOutcomeConfirmAuditLedger_enforce_UnchangedForAllowAndNotApplicable
// pins the "nothing produces Confirm yet, so behaviour is unchanged"
// half of WP01's proof requirement.
func TestOutcomeConfirmAuditLedger_enforce_UnchangedForAllowAndNotApplicable(t *testing.T) {
	t.Parallel()
	if err := enforce(Decision{Outcome: Allow}); err != nil {
		t.Fatalf("enforce(Allow) = %v, want nil (WP01 must not change this)", err)
	}
	if err := enforce(Decision{Outcome: NotApplicable}); err != nil {
		t.Fatalf("enforce(NotApplicable) = %v, want nil (WP01 must not move this hole here — that is WP02's job, and only inside the chat kernel adapter's own resolver)", err)
	}
	if err := enforce(Decision{Outcome: Deny}); err == nil {
		t.Fatal("enforce(Deny) = nil, want *PolicyDeniedError (WP01 must not change this)")
	}
}

func TestOutcomeConfirmAuditLedger_CheckCredentialAccess(t *testing.T) {
	t.Parallel()
	for _, strict := range []bool{false, true} {
		g := &fixedOutcomeGate{decision: Decision{Outcome: Confirm}}
		err := CheckCredentialAccess(context.Background(), g, "openai", "provider_call", strict)
		if err == nil {
			t.Fatalf("strictMode=%v: CheckCredentialAccess(Confirm) = nil, want an error — this call site has no prompt registry to ask through, so Confirm must fail closed", strict)
		}
		if g.calls != 1 {
			t.Fatalf("strictMode=%v: gate called %d times, want 1", strict, g.calls)
		}
	}
}

// TestOutcomeConfirmAuditLedger_CheckCredentialAccess_NotApplicableUnchanged
// pins that WP01 did not touch the pre-existing NotApplicable/strictMode
// contract this function documents.
func TestOutcomeConfirmAuditLedger_CheckCredentialAccess_NotApplicableUnchanged(t *testing.T) {
	t.Parallel()
	lenient := &fixedOutcomeGate{decision: Decision{Outcome: NotApplicable}}
	if err := CheckCredentialAccess(context.Background(), lenient, "openai", "provider_call", false); err != nil {
		t.Fatalf("strictMode=false: CheckCredentialAccess(NotApplicable) = %v, want nil (unchanged)", err)
	}
	strict := &fixedOutcomeGate{decision: Decision{Outcome: NotApplicable}}
	if err := CheckCredentialAccess(context.Background(), strict, "openai", "provider_call", true); err == nil {
		t.Fatal("strictMode=true: CheckCredentialAccess(NotApplicable) = nil, want errCredentialAccessDenied (unchanged)")
	}
}

func TestOutcomeConfirmAuditLedger_CheckAuditBulkPurge(t *testing.T) {
	t.Parallel()
	g := &fixedOutcomeGate{decision: Decision{Outcome: Confirm}}
	if err := CheckAuditBulkPurge(context.Background(), g); err == nil {
		t.Fatal("CheckAuditBulkPurge(Confirm) = nil, want an error (default-forbid action, no prompt mechanism at this call site)")
	}
}

// TestOutcomeConfirmAuditLedger_GateMCPSpawn_NilRegistryMatchesNotApplicable
// exercises the new `case NotApplicable, Confirm:` branch in GateMCPSpawn
// directly (rather than an unlabelled default:), and pins that — with no
// registry wired — Confirm reaches the SAME documented pre-boot
// default-allow stance NotApplicable already had. WP01 does not widen
// that pre-existing stance; it only makes the branch explicit so a
// future change to one case cannot silently stop covering the other.
func TestOutcomeConfirmAuditLedger_GateMCPSpawn_NilRegistryMatchesNotApplicable(t *testing.T) {
	t.Parallel()
	confirmGate := &fixedOutcomeGate{decision: Decision{Outcome: Confirm}}
	errConfirm := GateMCPSpawn(context.Background(), confirmGate, nil, "some-recipe", "", nil)

	naGate := &fixedOutcomeGate{decision: Decision{Outcome: NotApplicable}}
	errNA := GateMCPSpawn(context.Background(), naGate, nil, "some-recipe", "", nil)

	if errConfirm != errNA {
		t.Fatalf("GateMCPSpawn(nil registry): Confirm gave %v, NotApplicable gave %v — WP01 requires these stay identical (both nil) via the same explicit branch", errConfirm, errNA)
	}
	if errConfirm != nil {
		t.Fatalf("GateMCPSpawn(Confirm, registry=nil) = %v, want nil (documented pre-boot default-allow, unchanged by WP01)", errConfirm)
	}
}
