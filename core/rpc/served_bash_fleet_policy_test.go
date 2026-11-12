package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// Served/workbench mode (decision D-h, 2026-11-10): kenaz__bash is ON by
// default and a Cedar forbid on use_tool / Tool::"kenaz__bash" — the org's
// fleet rule set in practice — turns it off. These tests drive the
// production rpc.New wiring (WithFleetGatedBash), the real shared Cedar
// engine, and the real fleet applier (SetTeamBundle ->
// ReplaceHarnessSnippets), and read the production EnabledFilter the model
// catalog and builtin dispatch consult.

const (
	fleetPermitBash = `permit(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
	fleetForbidBash = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash");`
	fleetForbidTodo = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__todo_write");`
	// Broad shape: forbids every tool, bash included.
	fleetForbidAllTools = `forbid(principal, action == Action::"use_tool", resource);`
	// Context-reading forbid on an attribute the context-free availability
	// probe does not carry: it errors there and must fail closed (bash off).
	// (Keys the engine zero-fills for use_tool, e.g. session_kind, do not
	// error: they evaluate against "" and are decided at dispatch.)
	fleetForbidBashCtx = `forbid(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash") when { context.workbench_tier == "restricted" };`
	// Context-reading permit: erroring permits never grant, but bash is on
	// by default, so it stays on.
	fleetPermitBashCtx = `permit(principal, action == Action::"use_tool", resource == Tool::"kenaz__bash") when { context.workbench_tier == "trusted" };`
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

func TestServedBash_OnByDefault_FleetForbidDisables(t *testing.T) {
	api := newBashPolicyAPI(t, WithFleetGatedBash())

	// Settings.BashEnabled defaults to false and has no say in a workbench.
	if !bashOffered(t, api) {
		t.Fatal("served: bash not offered by default (D-h: on unless a forbid matches; Settings must not count)")
	}

	applyFleetRules(t, api, map[string]string{"forbid-todo": fleetForbidTodo})
	if !bashOffered(t, api) {
		t.Fatal("served: an unrelated fleet forbid turned bash off")
	}

	applyFleetRules(t, api, map[string]string{"deny-bash": fleetForbidBash})
	if bashOffered(t, api) {
		t.Fatal("served: bash offered despite a fleet forbid on Tool::\"kenaz__bash\"")
	}

	applyFleetRules(t, api, map[string]string{"allow-bash": fleetPermitBash, "deny-bash": fleetForbidBash})
	if bashOffered(t, api) {
		t.Fatal("served: a fleet permit outweighed a fleet forbid")
	}

	applyFleetRules(t, api, map[string]string{"deny-all": fleetForbidAllTools})
	if bashOffered(t, api) {
		t.Fatal("served: bash offered despite a forbid on every tool")
	}

	applyFleetRules(t, api, map[string]string{"deny-bash-ctx": fleetForbidBashCtx})
	if bashOffered(t, api) {
		t.Fatal("served: a context-reading forbid must fail closed in the context-free probe")
	}

	applyFleetRules(t, api, map[string]string{"allow-bash-ctx": fleetPermitBashCtx})
	if !bashOffered(t, api) {
		t.Fatal("served: an erroring context-reading PERMIT turned bash off (only forbids disable)")
	}

	// Live: the snapshot dropping the forbid turns bash back on, no restart.
	applyFleetRules(t, api, map[string]string{"deny-bash": fleetForbidBash})
	if bashOffered(t, api) {
		t.Fatal("served: forbid not applied live")
	}
	applyFleetRules(t, api, nil)
	if !bashOffered(t, api) {
		t.Fatal("served: bash still off after the fleet snapshot dropped the forbid")
	}
}

// A policy-managed bash with no engine cannot establish "no forbid" and
// stays off (fail closed).
func TestServedBash_NilEngineFailsClosed(t *testing.T) {
	b := &bashAvailability{}
	b.useFleetPolicy(nil)
	if on, managed := b.fleetDecision(); !managed || on {
		t.Fatalf("nil engine: on=%v managed=%v, want off + managed", on, managed)
	}
	var desktop *bashAvailability
	if _, managed := desktop.fleetDecision(); managed {
		t.Fatal("nil availability must defer to Settings (desktop)")
	}
}

func TestDesktopBash_UnaffectedByFleetRules(t *testing.T) {
	api := newBashPolicyAPI(t) // no WithFleetGatedBash: desktop

	if bashOffered(t, api) {
		t.Fatal("desktop: bash on by default; Settings.BashEnabled (default off) must decide")
	}
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
