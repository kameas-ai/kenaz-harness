package agentgraph

import (
	"context"
	"strings"
	"testing"
)

// panicExecutor is a test executor that always panics — standing in for
// a buggy node (bad type assertion, nil deref) or a panicking tool/seam.
type panicExecutor struct{ msg string }

func (panicExecutor) Kind() NodeKind { return NodeKindModel }

func (p panicExecutor) Execute(context.Context, *Env, *Node, PortValues) (Result, error) {
	panic(p.msg)
}

// okExecutor returns a benign result so the happy path stays exercised.
type okExecutor struct{}

func (okExecutor) Kind() NodeKind { return NodeKindModel }

func (okExecutor) Execute(context.Context, *Env, *Node, PortValues) (Result, error) {
	r := NewResult()
	r.Outputs["out"] = "ok"
	return r, nil
}

func TestSafeExecute_RecoversPanic(t *testing.T) {
	env := &Env{RunID: "r1", SessionID: "s1"}
	node := &Node{ID: "n1", Kind: NodeKindModel}

	res, err := safeExecute(context.Background(), panicExecutor{msg: "boom"}, env, node, PortValues{})
	if err == nil {
		t.Fatal("expected an error from a panicking executor, got nil")
	}
	if !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should name the node and the panic value, got: %v", err)
	}
	if !strings.Contains(err.Error(), "n1") {
		t.Fatalf("error should identify the node id, got: %v", err)
	}
	// A recovered panic must yield a non-nil (empty) Result, never a
	// half-built one that downstream code would misread.
	if res.Outputs == nil {
		t.Fatal("expected a non-nil Result.Outputs after recovery")
	}
}

func TestSafeExecute_HappyPathUnchanged(t *testing.T) {
	env := &Env{RunID: "r1"}
	node := &Node{ID: "n1", Kind: NodeKindModel}

	res, err := safeExecute(context.Background(), okExecutor{}, env, node, PortValues{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outputs["out"] != "ok" {
		t.Fatalf("expected pass-through output, got: %v", res.Outputs)
	}
}

// TestSafeExecute_NilNodeAndEnv guards the recovery path's own
// defensiveness: it must not itself panic when env/node are nil.
func TestSafeExecute_NilNodeAndEnv(t *testing.T) {
	_, err := safeExecute(context.Background(), panicExecutor{msg: "x"}, nil, nil, PortValues{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}
