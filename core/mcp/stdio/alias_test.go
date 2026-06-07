package stdio_test

import (
	"context"
	"testing"

	legacy "github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/stdio"
)

// TestAlias_PoolTypesEquivalent asserts the legacy import path
// (`core/mcp/stdio`) and the new canonical path
// (`core/mcp/transport/stdio`) reference the same underlying Pool
// type. The compiler enforces this through the type identity of
// the alias declarations; if either side drifts, the assignment
// below stops compiling.
func TestAlias_PoolTypesEquivalent(t *testing.T) {
	t.Parallel()
	// Both should construct a non-nil pool.
	p1 := legacy.NewPool(legacy.PoolOptions{})
	if p1 == nil {
		t.Fatalf("legacy.NewPool returned nil")
	}
	defer p1.Close(context.Background())
	// The legacy NewPool is the SAME function as
	// transport/stdio.NewPool — they share the underlying Pool
	// type. Cross-assign to prove it.
	var p2 *stdio.Pool = p1
	_ = p2
}

// TestAlias_StateConstantsAgree asserts the State constants
// re-exported through the alias match the canonical values.
func TestAlias_StateConstantsAgree(t *testing.T) {
	t.Parallel()
	if string(legacy.StateRunning) != string(stdio.StateRunning) {
		t.Fatalf("legacy.StateRunning != transport/stdio.StateRunning")
	}
	if string(legacy.StateStopped) != string(stdio.StateStopped) {
		t.Fatalf("legacy.StateStopped != transport/stdio.StateStopped")
	}
}

// TestAlias_RecipeStatusEquivalent asserts the RecipeStatus type
// is identical on both sides (it's a transport.RecipeStatus alias
// in both packages).
func TestAlias_RecipeStatusEquivalent(t *testing.T) {
	t.Parallel()
	var a legacy.RecipeStatus
	var b stdio.RecipeStatus = a
	_ = b
}
