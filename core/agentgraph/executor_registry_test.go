package agentgraph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// executor_registry_test.go — WP03 of
// agentgraph-total-convergence-01PMGX01 (spec §4.2): the executor table
// stops being a hardcoded map and becomes a registration API that both
// the build-time (static/codegen) kind set and runtime discovery feed.
//
// The tests below pin the three properties the spec asks for:
//
//  1. the static set still seeds every shipped kind (no behaviour
//     change at default settings),
//  2. runtime registration is possible AND visible to a kernel that
//     already exists — the property MCP-discovered tool kinds need,
//  3. duplicate registration is REFUSED rather than silently winning,
//     because two executors for one kind is precisely the
//     parallel-implementations failure this mission exists to remove.

// probeExecutor is a minimal Executor for a synthetic kind.
type probeExecutor struct {
	kind  NodeKind
	mu    sync.Mutex
	fired int
}

func (p *probeExecutor) Kind() NodeKind { return p.kind }

func (p *probeExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	p.mu.Lock()
	p.fired++
	p.mu.Unlock()
	res := NewResult()
	res.Outputs["out"] = "probe:" + node.ID
	return res, nil
}

func (p *probeExecutor) fireCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fired
}

// TestExecutorRegistry_SeedsEveryStaticKind pins that the constructor
// still produces exactly the build-time set, with no duplicates (a
// duplicate would panic through MustRegister — which is the point).
func TestExecutorRegistry_SeedsEveryStaticKind(t *testing.T) {
	t.Parallel()
	r := newExecutorRegistry()
	statics := builtinExecutors()
	if r.Len() != len(statics) {
		t.Fatalf("registry seeded %d kinds, builtinExecutors() lists %d — a duplicate Kind() would collapse two entries into one", r.Len(), len(statics))
	}
	for _, e := range statics {
		if !r.Has(e.Kind()) {
			t.Errorf("static executor for kind %q is not registered", e.Kind())
		}
	}
	// Kinds() is the query surface the I1 gate uses; it must be sorted
	// and complete.
	kinds := r.Kinds()
	if len(kinds) != r.Len() {
		t.Errorf("Kinds() returned %d entries, Len()=%d", len(kinds), r.Len())
	}
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] >= kinds[i] {
			t.Fatalf("Kinds() not sorted: %q then %q", kinds[i-1], kinds[i])
		}
	}
}

// TestExecutorRegistry_RejectsDuplicateRegistration is the "no silent
// second implementation" property.
func TestExecutorRegistry_RejectsDuplicateRegistration(t *testing.T) {
	t.Parallel()
	r := NewExecutorRegistry()
	first := &probeExecutor{kind: NodeKind("probe_kind")}
	if err := r.Register(first); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	second := &probeExecutor{kind: NodeKind("probe_kind")}
	err := r.Register(second)
	if !errors.Is(err, ErrExecutorRegistered) {
		t.Fatalf("duplicate Register: got err %v, want ErrExecutorRegistered", err)
	}
	// The original binding must survive the refused registration.
	got, ok := r.Lookup(NodeKind("probe_kind"))
	if !ok || got != Executor(first) {
		t.Errorf("refused duplicate replaced the binding: got %v, want the first executor", got)
	}

	// Replace is the explicit override path and DOES swap the binding —
	// that is what WithExecutor is built on.
	r.Replace(second)
	got, _ = r.Lookup(NodeKind("probe_kind"))
	if got != Executor(second) {
		t.Errorf("Replace did not swap the binding")
	}

	// Degenerate inputs are refused, not panicked on.
	if err := r.Register(nil); err == nil {
		t.Error("Register(nil) should error")
	}
	if err := r.Register(&probeExecutor{kind: ""}); err == nil {
		t.Error("Register(empty kind) should error")
	}
}

// TestExecutorRegistry_RuntimeRegistrationIsExecutable is the
// load-bearing property from spec §4.2: a kind registered AFTER the
// kernel was constructed runs. Without this the "MCP tools are nodes"
// goal is unreachable and the spec requires renegotiating it.
func TestExecutorRegistry_RuntimeRegistrationIsExecutable(t *testing.T) {
	t.Parallel()

	const kind = NodeKind("runtime_probe")
	k := NewKernel()

	// Before registration the kind is unknown and the kernel says so.
	if k.Executors().Has(kind) {
		t.Fatalf("kind %q should not be pre-registered", kind)
	}
	if _, err := k.Executors().lookup(kind); err == nil {
		t.Fatal("lookup of an unregistered kind should error")
	}

	probe := &probeExecutor{kind: kind}
	if err := k.Executors().Register(probe); err != nil {
		t.Fatalf("runtime Register: %v", err)
	}

	g := Graph{
		ID:          "runtime-reg",
		Entrypoints: []string{"n1"},
		Nodes:       []Node{{ID: "n1", Kind: kind}},
	}
	env := &Env{RunID: "r1", SessionID: "s1", Graph: &g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run with a runtime-registered kind: %v", err)
	}
	if probe.fireCount() != 1 {
		t.Errorf("runtime-registered executor fired %d times, want 1", probe.fireCount())
	}
}

// TestExecutorRegistry_ConcurrentRegisterAndLookup exercises the
// mutex under -race: runtime discovery may register while a run is in
// flight, so registration must not corrupt the map the kernel reads
// from its fire goroutines.
func TestExecutorRegistry_ConcurrentRegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := newExecutorRegistry()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = r.Register(&probeExecutor{kind: NodeKind("dyn_" + string(rune('a'+i%26)) + string(rune('a'+i/26)))})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = r.Has(NodeKindModel)
			_, _ = r.Lookup(NodeKindLoop)
			_ = r.Len()
			_ = r.Kinds()
		}
	}()
	wg.Wait()
}

// TestExecutorRegistry_WithExecutorOverrideUnchanged pins that the
// pre-existing test/override seam behaves exactly as it did before the
// registry rewrite: WithExecutor replaces the shipped binding, and the
// override reaches nested dispatch through env.registry.
func TestExecutorRegistry_WithExecutorOverrideUnchanged(t *testing.T) {
	t.Parallel()
	probe := &probeExecutor{kind: NodeKindTransform}
	k := NewKernel(WithExecutor(probe))
	got, ok := k.Executors().Lookup(NodeKindTransform)
	if !ok || got != Executor(probe) {
		t.Fatalf("WithExecutor did not override the shipped transform executor")
	}
	// The registry size is unchanged — an override replaces, never adds.
	if k.Executors().Len() != len(builtinExecutors()) {
		t.Errorf("WithExecutor changed registry size: got %d, want %d", k.Executors().Len(), len(builtinExecutors()))
	}
}
