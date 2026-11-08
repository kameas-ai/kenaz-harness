package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// Served/workbench mode: kenaz__bash is offered to the model iff the org's
// fleet Cedar policy explicitly permits use_tool on it. These tests drive
// the production rpc.New wiring (WithFleetGatedBash), the real shared
// Cedar engine, and the real fleet applier (SetTeamBundle ->
// ReplaceHarnessSnippets), and read the production EnabledFilter the
// model catalog and builtin dispatch consult.

const (
	fleetPermitBash = `permit(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
	fleetForbidBash = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
	fleetForbidTodo = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__todo_write");`
)

func newBashPolicyAPI(t *testing.T, opts ...Option) *API {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, append([]Option{WithSettingsStore(newTestStore(t))}, opts...)...)
	t.Cleanup(api.Shutdown)
	if api.cedarEngine == nil {
		t.Fatal("no shared Cedar engine over a real DataDir")
	}
	if api.builtinFilter == nil {
		t.Fatal("no production builtin filter")
	}
	if _, ok := api.Builtins().Lookup(corebash.Name); !ok {
		t.Fatal("bash tool not registered")
	}
	return api
}

func applyFleetRules(t *testing.T, api *API, rules map[string]string) {
	t.Helper()
	if err := corefleet.NewCedarBundleApplier(api.cedarEngine).SetTeamBundle(t.Context(), &corefleet.CedarDelta{Rules: rules}); err != nil {
		t.Fatalf("SetTeamBundle: %v", err)
	}
}

// bashOffered reports whether bash is both listed to the model and
// dispatchable; the two must agree.
func bashOffered(t *testing.T, api *API) bool {
	t.Helper()
	_, callable := api.builtinFilter.Lookup(corebash.Name)
	listed := false
	for _, n := range api.builtinFilter.Names() {
		if n == corebash.Name {
			listed = true
		}
	}
	if listed != callable {
		t.Fatalf("bash listed=%v but callable=%v", listed, callable)
	}
	return callable
}

func TestServedBash_FleetPolicyDecidesAvailability(t *testing.T) {
	api := newBashPolicyAPI(t, WithFleetGatedBash())

	// The local Settings toggle has no say in a workbench.
	if err := api.settingsImpl.Store().SaveBash(true); err != nil {
		t.Fatal(err)
	}

	if bashOffered(t, api) {
		t.Fatal("bash offered with no fleet permit (embedded default tool permit must not count; Settings must not count)")
	}

	applyFleetRules(t, api, map[string]string{"forbid-todo": fleetForbidTodo})
	if bashOffered(t, api) {
		t.Fatal("bash offered by a fleet rule set that does not permit it")
	}

	applyFleetRules(t, api, map[string]string{"allow-bash": fleetPermitBash})
	if !bashOffered(t, api) {
		t.Fatal("bash not offered with an explicit fleet permit")
	}

	applyFleetRules(t, api, map[string]string{"allow-bash": fleetPermitBash, "deny-bash": fleetForbidBash})
	if bashOffered(t, api) {
		t.Fatal("bash offered with a fleet forbid alongside the permit")
	}

	// Live: removing the permit from the snapshot turns it off, no restart.
	applyFleetRules(t, api, map[string]string{"allow-bash": fleetPermitBash})
	if !bashOffered(t, api) {
		t.Fatal("bash not re-enabled after forbid removed")
	}
	applyFleetRules(t, api, nil)
	if bashOffered(t, api) {
		t.Fatal("bash still offered after the fleet snapshot dropped the permit")
	}
}

func TestDesktopBash_UnaffectedByFleetRules(t *testing.T) {
	api := newBashPolicyAPI(t) // no WithFleetGatedBash: desktop

	applyFleetRules(t, api, map[string]string{"allow-bash": fleetPermitBash})
	if bashOffered(t, api) {
		t.Fatal("desktop: fleet permit turned bash on; Settings.BashEnabled (default off) must decide")
	}
	if err := api.settingsImpl.Store().SaveBash(true); err != nil {
		t.Fatal(err)
	}
	applyFleetRules(t, api, map[string]string{"deny-bash": fleetForbidBash})
	if !bashOffered(t, api) {
		t.Fatal("desktop: fleet forbid hid bash; Settings.BashEnabled=true must decide availability")
	}
}

// The availability probe and the chat use_tool gate must evaluate the SAME
// resource for bash, or an admin's rule could enable a tool the call gate
// then evaluates under a different name.
func TestServedBash_ProbeUsesChatGateToolUID(t *testing.T) {
	if corebash.Name != toolloop.BuiltinServerName+"__bash" {
		t.Fatalf("bash tool name drifted: %q", corebash.Name)
	}
	// The dispatcher splits "kenaz__bash" into server="kenaz", tool="bash"
	// before the session-kind resolver builds its resource.
	got := useToolResource(toolloop.BuiltinServerName, "bash").String()
	if want := `Tool::"kenaz__bash"`; got != want {
		t.Fatalf("use_tool resource = %s, want %s", got, want)
	}
}
