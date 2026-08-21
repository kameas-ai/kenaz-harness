package bundle

// UNIT-7 (bundle-download-and-verify-01PMZ909, spec AC-009): the gate
// is real, not NotApplicable. Before this unit, Bundle_Install
// consulted no policy gate at all (spec §1.7 F-3) — F-3's own text
// warns that the naive fix ("add a Check* call") is a trap on its own:
// with no .cedar file naming the action, every evaluation returns
// NotApplicable, cedar's enforce() maps that to allow, and the gate
// LOOKS wired while gating nothing. The shipped
// default_bundle_policy.cedar (core/policy/cedar/policies/) is what
// closes that trap; this file proves it does, not merely that the
// Go plumbing compiles.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestCheckBundleInstall_NoPolicyAtAll_IsTheDocumentedTrap pins the raw
// mechanism spec §5.6 and AC-009's "*Fails if*" clause describe: an
// engine with NO policy at all (not even the embedded default)
// evaluates ActionBundleInstall as NotApplicable, and enforce() maps
// that to a nil error — a gate call site that "looks wired" while
// gating nothing. This is not a bug in cedar.enforce (default-allow is
// the harness's documented stance); it exists here so the NEXT test —
// which proves the shipped default_bundle_policy.cedar closes exactly
// this gap in production — has a pinned baseline to contrast against.
func TestCheckBundleInstall_NoPolicyAtAll_IsTheDocumentedTrap(t *testing.T) {
	eng, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := eng.SetPolicyText("empty.cedar", []byte("")); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	d := eng.Evaluate(context.Background(), cedar.UserUID(), cedar.ActionBundleInstall, cedar.BundleUID("local_path:/tmp/x"), nil)
	if d.Outcome != cedar.NotApplicable {
		t.Fatalf("Outcome=%v, want NotApplicable for a policy set with no matching rule", d.Outcome)
	}
	if err := cedar.CheckBundleInstall(context.Background(), eng, "local_path:/tmp/x"); err != nil {
		t.Fatalf("CheckBundleInstall over a NotApplicable decision should not error (documented default-allow stance): %v", err)
	}
}

// TestInstall_ShippedPolicy_PermitsByDefault_NotByAccident proves the
// shipped default_bundle_policy.cedar produces a REAL Allow decision
// (not a NotApplicable that happens to enforce() as allow) for a
// freshly booted, IncludeEmbedded engine with nothing on disk — the
// production shape core/rpc/api.go's a.cedarGate() always constructs.
func TestInstall_ShippedPolicy_PermitsByDefault_NotByAccident(t *testing.T) {
	dataDir := t.TempDir()
	eng, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, IncludeEmbedded: true, LoadFromDisk: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d := eng.Evaluate(context.Background(), cedar.UserUID(), cedar.ActionBundleInstall, cedar.BundleUID("local_path:/tmp/x"), nil)
	if d.Outcome != cedar.Allow {
		t.Fatalf("Outcome=%v, want Allow — the shipped default_bundle_policy.cedar should match a real permit, not fall through to NotApplicable", d.Outcome)
	}
}

// TestInstall_AC009_OperatorForbidRefusesThenLiftingItRestoresInstall
// is AC-009 itself: with the shipped policy set loaded from
// <DataDir>/policy/ (the production LoadFromDisk shape) plus an
// operator-authored deny rule for ActionBundleInstall, Install must be
// refused. Removing that deny file and reloading must restore a real
// (matched-permit) Allow — proving the gate re-evaluates live rather
// than latching a decision, and that "no override present" resolves
// through the shipped permit, not a NotApplicable-happens-to-allow
// accident (the previous test already isolates that half; this one
// re-confirms it survives a load/forbid/unload/reload cycle).
func TestInstall_AC009_OperatorForbidRefusesThenLiftingItRestoresInstall(t *testing.T) {
	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, "policy")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	eng, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, IncludeEmbedded: true, LoadFromDisk: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	bundleDir := t.TempDir()
	artifactBytes := []byte("gated install artifact bytes")
	digest := sha256Hex(artifactBytes)
	if err := os.MkdirAll(filepath.Join(bundleDir, "policy"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "policy", "policy.toml"), artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	manifestYAML := `schema_version: 1
name: gated-bundle
version: 1.0.0
license: MIT
artifacts:
  - name: policy.toml
    kind: policy
    path: policy/policy.toml
    content_hash: "` + digest + `"
`
	if err := os.WriteFile(filepath.Join(bundleDir, "kenaz.yaml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	rw := &memReadWriter{}
	casDir := t.TempDir()
	realCAS, err := cache.New(casDir)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithCAS(CASFromCache(realCAS)), WithGate(eng))

	// Deny rule: an operator forbidding ActionBundleInstall outright.
	forbid := []byte(`forbid (
    principal,
    action == Action::"bundle.install",
    resource is Bundle
);`)
	if err := os.WriteFile(filepath.Join(policyDir, "99_forbid_bundle_install.cedar"), forbid, 0o644); err != nil {
		t.Fatalf("write forbid policy: %v", err)
	}
	if err := eng.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	_, err = api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err == nil {
		t.Fatalf("Install should be refused while the operator forbid rule is active")
	}
	if !cedar.IsPolicyDenied(err) {
		t.Fatalf("err=%v, want a *cedar.PolicyDeniedError (the gate must refuse BEFORE any channel is opened)", err)
	}
	if realCAS.Has(digest) {
		t.Fatalf("a policy-denied install must never touch the CAS — the gate runs before Registry.Open")
	}
	list, lerr := api.List(context.Background())
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("a policy-denied install must leave no lockfile row, got %+v", list)
	}

	// Lift the forbid: remove the file and reload. Install of the SAME
	// bundle must now succeed — the shipped permit resolves it, not a
	// stale denial and not a silent NotApplicable-happens-to-allow.
	if err := os.Remove(filepath.Join(policyDir, "99_forbid_bundle_install.cedar")); err != nil {
		t.Fatalf("remove forbid policy: %v", err)
	}
	if err := eng.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got, err := api.Install(context.Background(), InstallRequest{Kind: "local_path", Path: bundleDir})
	if err != nil {
		t.Fatalf("Install should succeed once the forbid rule is lifted: %v", err)
	}
	if got.Name != "gated-bundle" {
		t.Errorf("Install returned %+v", got)
	}
	if !realCAS.Has(digest) {
		t.Fatalf("artifact digest %s should be in the CAS after the un-denied install", digest)
	}
}

// TestInstall_GateRunsBeforeAnyChannelOpen confirms the gate is
// consulted even for an install whose channel Kind is entirely
// unregistered (would otherwise fail with ErrChannelUnknown) — proof
// the deny happens strictly before Registry.Open, matching spec §5.6's
// "before any fetch".
func TestInstall_GateRunsBeforeAnyChannelOpen(t *testing.T) {
	eng, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := eng.SetPolicyText("forbid.cedar", []byte(`forbid (
    principal,
    action == Action::"bundle.install",
    resource is Bundle
);`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	rw := &memReadWriter{}
	api := NewAPI(WithReader(rw), WithWriter(rw), WithGate(eng))
	_, err = api.Install(context.Background(), InstallRequest{Kind: "no-such-registered-kind", Path: "/tmp/whatever"})
	if !cedar.IsPolicyDenied(err) {
		t.Fatalf("err=%v, want *cedar.PolicyDeniedError — the gate must fire before Registry.Open even attempts to resolve the (unregistered) kind", err)
	}
}
