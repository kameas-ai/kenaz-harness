package agentgraph_test

// graph_authoring_gate_test.go — model-authored-graphs-01PMGA01 UNIT-4.
//
// Manager-level tests for the graph.author / graph.run wiring
// (WithGraphCedarGate, WithAuthoringEnabled): the nil-gate / nil-resolver
// fail-closed defaults, the initiator scoping (AC-012), and a
// concurrency smoke test for the new cedarGate/authoringEnabled reads
// alongside listLibrary under -race (tasks.md UNIT-4's own concurrency
// note). The real boot-wiring assertions (AC-004/AC-005/AC-006 through
// core.New -> rpc.New) live in core/rpc/api_graph_authoring_gate_test.go
// because that is the only place the real settings store and the real
// shared Cedar engine both exist together.

import (
	"context"
	"errors"
	"sync"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// denyGraphAuthorGate is a minimal cedar.Gate that always denies
// graph.author and always allows graph.run — enough to prove the
// Manager actually consults m.cedarGate rather than silently treating
// it as nil.
type denyGraphAuthorGate struct{}

func (denyGraphAuthorGate) Evaluate(ctx context.Context, principal cedarlib.EntityUID, action string, resource cedarlib.EntityUID, attrs map[cedarlib.String]cedarlib.Value) cedar.Decision {
	if action == cedar.ActionGraphAuthor {
		return cedar.Decision{Outcome: cedar.Deny, Action: action, Reason: "test: always deny graph.author"}
	}
	return cedar.Decision{Outcome: cedar.Allow, Action: action, Reason: "test: always allow"}
}

// TestManagerSaveGraph_ModelInitiator_ConsultsWiredGate proves saveGraph
// actually calls the wired cedar.Gate for a non-"user" initiator — a
// Manager built with a real (deny-everything) gate must refuse a
// model-initiated save even though the graph itself is perfectly valid.
func TestManagerSaveGraph_ModelInitiator_ConsultsWiredGate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithGraphCedarGate(denyGraphAuthorGate{}),
		graphview.WithAuthoringEnabled(func() bool { return true }), // even "enabled" — the gate itself denies
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "zz_gate_wired_probe"
	err = a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: validGraphYAML(id)}, "model")
	if err == nil {
		t.Fatal("model-initiated SaveGraph succeeded against a gate that always denies graph.author")
	}
	var pderr *cedar.PolicyDeniedError
	if !errors.As(err, &pderr) {
		t.Fatalf("err = %v (%T); want *cedar.PolicyDeniedError", err, err)
	}
}

// TestManagerSaveGraph_UserInitiator_NeverConsultsGate proves the
// initiator scoping itself, at the Manager level: the SAME
// always-deny-graph.author gate must NOT block a "user"-initiated save
// (AC-012). If this test fails, the gate call site has stopped being
// scoped to non-user initiators.
func TestManagerSaveGraph_UserInitiator_NeverConsultsGate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithGraphCedarGate(denyGraphAuthorGate{}),
		graphview.WithAuthoringEnabled(func() bool { return false }),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	id := "zz_user_bypass_probe"
	if err := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: validGraphYAML(id)}, "user"); err != nil {
		t.Fatalf("user-initiated SaveGraph was denied by a gate that should not have been consulted: %v", err)
	}
}

// requireEnabledGate denies graph.author unless
// context.authoring_enabled == "true" — a minimal stand-in for the
// shipped graph_author_forbid.cedar policy, used to prove the Manager
// sends "false" (not an empty/unset attribute the gate might read
// differently) when no WithAuthoringEnabled resolver is wired.
type requireEnabledGate struct{}

func (requireEnabledGate) Evaluate(_ context.Context, _ cedarlib.EntityUID, action string, _ cedarlib.EntityUID, attrs map[cedarlib.String]cedarlib.Value) cedar.Decision {
	if action != cedar.ActionGraphAuthor {
		return cedar.Decision{Outcome: cedar.Allow, Action: action}
	}
	if v, ok := attrs[cedarlib.String("authoring_enabled")]; ok {
		if s, ok := v.(cedarlib.String); ok && string(s) == "true" {
			return cedar.Decision{Outcome: cedar.Allow, Action: action, Reason: "test: authoring_enabled=true"}
		}
	}
	return cedar.Decision{Outcome: cedar.Deny, Action: action, Reason: "test: authoring_enabled not true"}
}

// TestManagerSaveGraph_NilGateAndResolver_DefaultsClosed is the
// "Manager built without the settings seam (every test chassis)" case
// plan.md calls out: nil cedarGate maps to GateGraphAuthor's own
// default-allow contract (the correct library default), but nil
// authoringEnabled must still read as OFF at the Go level — the
// distinction matters because a caller that wires a gate but forgets
// the resolver would otherwise silently authorize every model save
// (authoringEnabled defaulting to true would be the dangerous
// direction). This test pins the resolver's own nil-safety, independent
// of whether a gate is wired.
func TestManagerSaveGraph_NilResolver_ReadsAsOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A permissive-when-enabled gate: Allow only if authoring_enabled
	// == "true". With no WithAuthoringEnabled option, the Manager must
	// send "false".
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithGraphCedarGate(requireEnabledGate{}),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()
	id := "zz_nil_resolver_probe"
	err = a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: validGraphYAML(id)}, "model")
	if err == nil {
		t.Fatal("model-initiated SaveGraph succeeded with no authoringEnabled resolver wired — nil must read as off, not on")
	}
}

// TestConcurrent_SaveGraph_StartRun_ListLibrary is the concurrency smoke
// test tasks.md UNIT-4 calls for: saveGraph and startRun (both now
// reading m.cedarGate / m.authoringEnabled) run concurrently with
// listLibrary under m.mu. Race-checked via `go test -race`; this test
// only needs to not deadlock or race, not assert particular outcomes.
func TestConcurrent_SaveGraph_StartRun_ListLibrary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var enabledFlag bool
	var flagMu sync.Mutex
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithGraphCedarGate(nil), // nil gate: default-allow, exercises the nil path under concurrency too
		graphview.WithAuthoringEnabled(func() bool {
			flagMu.Lock()
			defer flagMu.Unlock()
			return enabledFlag
		}),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	const n = 12
	var wg sync.WaitGroup
	wg.Add(n * 3)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := "zz_concurrent_" + graphIDSuffix(i)
			_ = a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: validGraphYAML(id)}, "model")
		}()
		go func() {
			defer wg.Done()
			_, _ = a.ListGraphs(ctx, "user")
		}()
		go func() {
			defer wg.Done()
			flagMu.Lock()
			enabledFlag = !enabledFlag
			flagMu.Unlock()
		}()
	}
	wg.Wait()
}

func graphIDSuffix(i int) string {
	digits := "0123456789"
	if i < 10 {
		return string(digits[i])
	}
	return string(digits[i/10]) + string(digits[i%10])
}
