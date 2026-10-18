package rpc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// graph-fs-gate-01GFSG01 WP01 (FR-3 / FR-4(b)) — a user .cedar file that
// fails to parse must fail the agent-graph path CLOSED.
//
// On main before this WP, Engine.Reload skipped the corrupt file and kept
// running on the embedded defaults (which permit file_write for every
// resource), and the graph PolicyGateAdapter bound the raw engine — so
// the CheckFileWrite assertions below returned nil: the user's forbid
// rules silently vanished and the graph write went through.

// graphPolicyOf returns the PolicyGate the production graph manager
// installs onto every kernel Env (chat and library-graph alike).
func graphPolicyOf(t *testing.T, api *API) coreag.PolicyGate {
	t.Helper()
	if api.graphMgr == nil {
		t.Fatal("graphMgr nil")
	}
	env := &coreag.Env{}
	api.graphMgr.EnvDefaults()(env)
	if env.Policy == nil {
		t.Fatal("graph env.Policy not wired")
	}
	return env.Policy
}

func requirePolicyDenied(t *testing.T, what string, err error) *cedar.PolicyDeniedError {
	t.Helper()
	var pde *cedar.PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("%s: want *cedar.PolicyDeniedError (fail closed), got %v", what, err)
	}
	return pde
}

func TestGraphPolicy_CorruptUserPolicyMidSession_FailsClosed(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	ctx := context.Background()
	pol := graphPolicyOf(t, api)
	target := filepath.Join(dataDir, "out.txt")

	// Baseline: a healthy default install permits the graph write.
	if err := pol.CheckFileWrite(ctx, target); err != nil {
		t.Fatalf("baseline graph write denied on a healthy install: %v", err)
	}

	writeRawPolicy(t, dataDir, "zz_user_forbid.cedar", "this is not cedar {{{")
	_ = api.CedarPolicy().ReloadPolicies(ctx)

	pde := requirePolicyDenied(t, "graph file_write with a corrupt user policy", pol.CheckFileWrite(ctx, target))
	if !strings.Contains(pde.Decision.Reason, "zz_user_forbid.cedar") {
		t.Errorf("denial does not name the failing file: %q", pde.Decision.Reason)
	}
	requirePolicyDenied(t, "graph file_read", pol.CheckFileRead(ctx, target))
	requirePolicyDenied(t, "graph state_write", pol.CheckStateWrite(ctx, "file"))
	requirePolicyDenied(t, "graph tool", pol.CheckTool(ctx, "kenaz__bash"))
	// state_read (harness-owned buffers) is not in the fail-closed set.
	if err := pol.CheckStateRead(ctx, "bash_output"); err != nil {
		t.Errorf("state_read should not fail closed: %v", err)
	}

	// The parse error is retrievable from the Settings > Policy surface.
	files, err := api.CedarPolicy().ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	sawErr := false
	for _, f := range files {
		if f.Name == "zz_user_forbid.cedar" && !f.ParseOK && f.ParseErr != "" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("parse error not retrievable via ListPolicies: %+v", files)
	}
	if api.cedarEngine.UserPolicyLoadError() == nil {
		t.Fatal("UserPolicyLoadError() nil with a corrupt user policy")
	}

	// The denial is audited in the engine's decision log.
	decs, _ := api.CedarPolicy().RecentDecisions(ctx, 20)
	audited := false
	for _, d := range decs {
		if d.Outcome == cedar.Deny && d.MatchedPolicy == "fail-closed/policy-load-error" {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("fail-closed denial not in RecentDecisions: %+v", decs)
	}

	// Scope: the SHARED engine's other consumers keep their documented
	// fail-open posture (TestCedarHoist_CorruptPolicyMidSession_StaysFailOpen).
	if err := api.memStoreRef.Add(ctx, memoryChunk()); err != nil {
		t.Fatalf("memory write (non-graph site) failed closed: %v", err)
	}

	// Fixing the file and reloading lifts the fail-closed posture.
	writeRawPolicy(t, dataDir, "zz_user_forbid.cedar", "// fixed\n")
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("reload after fix: %v", err)
	}
	if err := pol.CheckFileWrite(ctx, target); err != nil {
		t.Fatalf("graph write still denied after the policy was fixed: %v", err)
	}
}

func TestGraphPolicy_CorruptUserPolicyAtBoot_FailsClosed(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	writeRawPolicy(t, dataDir, "broken.cedar", "permit ( {{{")
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)

	pol := graphPolicyOf(t, api)
	requirePolicyDenied(t, "graph file_write with a corrupt policy at boot",
		pol.CheckFileWrite(context.Background(), filepath.Join(dataDir, "x")))
}

// No policy configured at all (nil Core / empty DataDir) is absence, not
// corruption: the graph path keeps its AllowAll posture.
func TestGraphPolicy_NoPolicyConfigured_StaysPermissive(t *testing.T) {
	mgr, _, _, _ := newGraphManagerWithDeps(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	env := &coreag.Env{}
	mgr.EnvDefaults()(env)
	if env.Policy == nil {
		t.Fatal("env.Policy not wired")
	}
	if err := env.Policy.CheckFileWrite(context.Background(), "/tmp/x"); err != nil {
		t.Fatalf("no-policy chassis denied a graph write: %v", err)
	}
}
