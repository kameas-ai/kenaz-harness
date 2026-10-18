package settings

// fleet-org-config-inheritance-01NORGX01 WP05 (FR-010).
//
// Before this WP, audit.KindFleetConfigApplied / KindFleetConfigSignatureRejected
// / KindFleetConfigPartialFailure were declared with full payload types and
// privacy-invariant doc comments but had ZERO emit call sites anywhere in the
// tree (docs/unwired-ledger.md, 2026-09-12 entry) — a bundle apply ACKed
// "applied:true" with no auditable record naming the org, the bundle_id, or
// which provisioned recipes were installed. These tests pin the real,
// narrowly-scoped fix: compositeConfigApplier.ApplyBundle now emits
// KindFleetConfigApplied via the wired auditEmitter, but ONLY on a fully
// clean apply (KindFleetConfigApplied's own doc: "fires after a fleet config
// bundle has been fully verified and all sections applied successfully") —
// a partial-failure bundle must not claim that.
//
// KindFleetConfigSignatureRejected and KindFleetConfigPartialFailure remain
// unwired (they require plumbing through core/fleet's ConfigPoller.poll(),
// which catches signature failures before ApplyBundle is ever called, and
// per-section failure attribution respectively) — see docs/unwired-ledger.md
// for the tracked, dated, owned residue.

import (
	"context"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// fakeAuditEmitter is a race-safe recorder for EmitFleetEvent calls
// (CLAUDE.md's canonical race-safe test-fake pattern: mutex + snapshot()).
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []fakeAuditEvent
}

type fakeAuditEvent struct {
	kind    contextaudit.Kind
	payload any
}

func (f *fakeAuditEmitter) EmitFleetEvent(_ context.Context, kind contextaudit.Kind, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fakeAuditEvent{kind: kind, payload: payload})
	return nil
}

func (f *fakeAuditEmitter) snapshot() []fakeAuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeAuditEvent, len(f.events))
	copy(out, f.events)
	return out
}

// TestApplyBundle_CleanApply_EmitsFleetConfigApplied is the FR-010 pin: a
// fully-clean apply of a bundle carrying provisioned_mcp emits exactly one
// KindFleetConfigApplied event naming the bundle_id, the sections present,
// and the provisioned recipe ids.
//
// Mutation: remove the `if len(errs) == 0 { a.emitConfigApplied(...) }` call
// in ApplyBundle (reverting to the pre-WP05 zero-emit-sites state). Must
// fail — emitter.snapshot() would be empty.
func TestApplyBundle_CleanApply_EmitsFleetConfigApplied(t *testing.T) {
	cat := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{{ID: "slack", Source: recipes.SourceShipped, DisplayName: "Slack", Command: []string{"x"}}}
		},
		nil, nil,
	)
	api := &API{}
	api.SetMCPCatalog(cat)
	emitter := &fakeAuditEmitter{}
	api.SetAuditEmitter(emitter)
	applier := &compositeConfigApplier{state: api.fleet}

	b := &fleet.Bundle{
		BundleID:     42,
		IssuedAt:     time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		MCPAllowlist: []string{"slack"},
		ProvisionedMCP: []fleet.ProvisionedMCP{
			{RecipeID: "slack", URL: "https://mcp.slack.com"},
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("ApplyBundle errs = %v, want none", errs)
	}

	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want exactly 1: %+v", len(events), events)
	}
	if events[0].kind != contextaudit.KindFleetConfigApplied {
		t.Errorf("kind = %q, want %q", events[0].kind, contextaudit.KindFleetConfigApplied)
	}
	payload, ok := events[0].payload.(contextaudit.FleetConfigAppliedPayload)
	if !ok {
		t.Fatalf("payload type = %T, want contextaudit.FleetConfigAppliedPayload", events[0].payload)
	}
	if payload.BundleID != 42 {
		t.Errorf("BundleID = %d, want 42", payload.BundleID)
	}
	if !payload.IssuedAt.Equal(b.IssuedAt) {
		t.Errorf("IssuedAt = %v, want %v", payload.IssuedAt, b.IssuedAt)
	}
	wantSections := map[string]bool{"mcp_allowlist": true, "provisioned_mcp": true}
	if len(payload.Sections) != len(wantSections) {
		t.Errorf("Sections = %v, want exactly %v", payload.Sections, wantSections)
	}
	for _, s := range payload.Sections {
		if !wantSections[s] {
			t.Errorf("unexpected section %q in %v", s, payload.Sections)
		}
	}
	if len(payload.ProvisionedRecipeIDs) != 1 || payload.ProvisionedRecipeIDs[0] != "slack" {
		t.Errorf("ProvisionedRecipeIDs = %v, want [slack]", payload.ProvisionedRecipeIDs)
	}
}

// TestApplyBundle_PartialFailure_DoesNotEmitFleetConfigApplied proves the
// scoping: KindFleetConfigApplied documents itself as firing only after
// "all sections applied successfully" — a bundle that produced any apply
// error must not emit it (KindFleetConfigPartialFailure is the correct kind
// for that case, and remains unwired — see docs/unwired-ledger.md).
func TestApplyBundle_PartialFailure_DoesNotEmitFleetConfigApplied(t *testing.T) {
	api := &API{}
	emitter := &fakeAuditEmitter{}
	api.SetAuditEmitter(emitter)
	// No SetMCPCatalog call: a non-empty provisioned_mcp section becomes a
	// named apply error (TestApplyBundle_ProvisionedMCP_UnwiredCatalog_Fails
	// pins this same shape).
	applier := &compositeConfigApplier{state: api.fleet}

	b := &fleet.Bundle{
		BundleID:       7,
		ProvisionedMCP: []fleet.ProvisionedMCP{{RecipeID: "slack"}},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("precondition: expected a non-empty error slice for an unwired MCP catalog")
	}
	if events := emitter.snapshot(); len(events) != 0 {
		t.Errorf("expected zero emitted events on a partial-failure apply, got %+v", events)
	}
}

// TestApplyBundle_NoEmitterWired_DoesNotPanic proves the nil-safe default:
// a build that never calls SetAuditEmitter (every pre-WP05 build, and any
// build where fleet audit wiring is intentionally absent) applies bundles
// exactly as before — no panic, no behavior change.
func TestApplyBundle_NoEmitterWired_DoesNotPanic(t *testing.T) {
	cat := recipes.NewMergedCatalog(nil, nil, nil)
	api := &API{}
	api.SetMCPCatalog(cat) // SetAuditEmitter deliberately NOT called
	applier := &compositeConfigApplier{state: api.fleet}

	b := &fleet.Bundle{BundleID: 1, ProvisionedMCP: []fleet.ProvisionedMCP{{RecipeID: "slack"}}}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("ApplyBundle errs = %v, want none", errs)
	}
}

// TestApplyBundle_EmitsOrgIdentityWhenCached proves FR-010's "naming the
// org": when a fleet.Identity is cached on disk (the normal post-enroll
// state), the emitted payload's OrgID/OrgName are populated from it.
func TestApplyBundle_EmitsOrgIdentityWhenCached(t *testing.T) {
	dataDir := t.TempDir()
	if err := fleet.SaveIdentity(dataDir, fleet.Identity{
		OrgID:   "org_123",
		OrgName: "Acme Corp",
	}); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	api := &API{}
	api.SetFleetClient(nil, dataDir) // dataDir only — no client behavior under test
	emitter := &fakeAuditEmitter{}
	api.SetAuditEmitter(emitter)
	applier := &compositeConfigApplier{state: api.fleet}

	b := &fleet.Bundle{BundleID: 3, MCPAllowlist: []string{"github"}}
	if errs := applier.ApplyBundle(context.Background(), b); len(errs) != 0 {
		t.Fatalf("ApplyBundle errs = %v, want none", errs)
	}

	events := emitter.snapshot()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want exactly 1", len(events))
	}
	payload := events[0].payload.(contextaudit.FleetConfigAppliedPayload)
	if payload.OrgID != "org_123" || payload.OrgName != "Acme Corp" {
		t.Errorf("OrgID/OrgName = %q/%q, want org_123/Acme Corp", payload.OrgID, payload.OrgName)
	}
}
