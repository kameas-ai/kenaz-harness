package engine_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy"
	"github.com/kameas-ai/kenaz-harness/core/policy/engine"
)

// fixedAction is the test-side Action implementation.
type fixedAction struct {
	kind   string
	inputs map[string]any
}

func (a fixedAction) Kind() string            { return a.kind }
func (a fixedAction) Inputs() map[string]any  { return a.inputs }

// allowAlwaysSource produces a single rule whose Match always returns
// allow. It models the "smoke-test policy" from WP01 T004.
func allowAlwaysSource(_ engine.CompiledBundle) ([]engine.EmbeddedRule, error) {
	return []engine.EmbeddedRule{{
		ActionKind: "test.smoke",
		PolicyID:   "policy-smoke",
		ClauseID:   "clause-allow",
		Match: func(_ context.Context, _ policy.Action) (engine.BackendDecision, bool) {
			return engine.BackendDecision{Outcome: policy.Allow}, true
		},
	}}, nil
}

func newTestEngine(t *testing.T, src func(engine.CompiledBundle) ([]engine.EmbeddedRule, error)) (*engine.Engine, *policy.MemoryEmitter) {
	t.Helper()
	em := policy.NewMemoryEmitter()
	be := engine.NewEmbeddedBackend(src)
	e, err := engine.New(engine.Options{Backend: be, Emitter: em})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return e, em
}

func TestEngineEvaluateSmokeAllow(t *testing.T) {
	e, em := newTestEngine(t, allowAlwaysSource)
	if err := e.Reload(context.Background(), nil); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	dec, err := e.Evaluate(context.Background(), fixedAction{
		kind:   "test.smoke",
		inputs: map[string]any{"provider": "anthropic"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec.Outcome != policy.Allow {
		t.Fatalf("expected Allow, got %s", dec.Outcome)
	}
	events := em.Events()
	hasAllow := false
	for _, ev := range events {
		if ev.Kind == policy.EventPolicyEvaluationAllowed {
			hasAllow = true
			break
		}
	}
	if !hasAllow {
		t.Fatalf("expected an EventPolicyEvaluationAllowed audit entry; got %+v", events)
	}
}

func TestEngineFailClosedDefault(t *testing.T) {
	e, em := newTestEngine(t, allowAlwaysSource)
	if err := e.Reload(context.Background(), nil); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	dec, err := e.Evaluate(context.Background(), fixedAction{kind: "no.such.kind"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec.Outcome != policy.Deny {
		t.Fatalf("expected fail-closed Deny, got %s", dec.Outcome)
	}
	if dec.ReasonCode != policy.ReasonPolicyUnavailable {
		t.Fatalf("expected ReasonPolicyUnavailable, got %s", dec.ReasonCode)
	}
	hasFailClosed := false
	for _, ev := range em.Events() {
		if ev.Kind == policy.EventPolicyUnavailableFailClosed {
			hasFailClosed = true
		}
	}
	if !hasFailClosed {
		t.Fatalf("expected EventPolicyUnavailableFailClosed audit entry")
	}
}

func TestEngineFailClosedBeforeReload(t *testing.T) {
	em := policy.NewMemoryEmitter()
	be := engine.NewEmbeddedBackend(nil)
	e, err := engine.New(engine.Options{Backend: be, Emitter: em})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	dec, err := e.Evaluate(context.Background(), fixedAction{kind: "anything"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec.Outcome != policy.Deny {
		t.Fatalf("expected fail-closed Deny before any policy load, got %s", dec.Outcome)
	}
}

func TestEngineExplainSurfacesMatch(t *testing.T) {
	e, _ := newTestEngine(t, allowAlwaysSource)
	if err := e.Reload(context.Background(), nil); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	exp, err := e.Explain(context.Background(), fixedAction{kind: "test.smoke"})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.Decision.Outcome != policy.Allow {
		t.Fatalf("Explain.Decision should be Allow, got %s", exp.Decision.Outcome)
	}
}

func TestEngineNilActionRejected(t *testing.T) {
	e, _ := newTestEngine(t, allowAlwaysSource)
	if err := e.Reload(context.Background(), nil); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := e.Evaluate(context.Background(), nil); err == nil {
		t.Fatalf("expected error on nil action")
	}
}

// TestOPAImportBoundary asserts WP01 acceptance: no package under
// core/policy may import OPA except core/policy/engine/opa. The check
// scans the source files in this worktree's policy tree at test time.
func TestOPAImportBoundary(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	root, _ = filepath.Abs(root)
	policyRoot := filepath.Join(root, "policy")

	violations := []string{}
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := readDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(dir, e)
			if isDir(full) {
				walk(full)
				continue
			}
			if !strings.HasSuffix(full, ".go") {
				continue
			}
			// Skip test files: the boundary applies to production
			// imports; tests legitimately mention the OPA path in
			// scanning logic and string literals.
			if strings.HasSuffix(full, "_test.go") {
				continue
			}
			rel, _ := filepath.Rel(policyRoot, full)
			if strings.HasPrefix(rel, "engine/opa/") || strings.HasPrefix(rel, "engine"+string(filepath.Separator)+"opa"+string(filepath.Separator)) {
				continue
			}
			body, err := readFile(full)
			if err != nil {
				continue
			}
			if strings.Contains(body, `"github.com/open-policy-agent/opa`) {
				violations = append(violations, rel)
			}
		}
	}
	walk(policyRoot)
	if len(violations) > 0 {
		t.Fatalf("OPA import boundary violated by: %v", violations)
	}
}
