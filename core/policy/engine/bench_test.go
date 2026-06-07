package engine_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy"
	"github.com/kameas-ai/kenaz-harness/core/policy/engine"
)

// BenchmarkEvaluate baselines NFR-001 (sub-1 ms p99). Run with
// `go test -bench=BenchmarkEvaluate -benchmem ./core/policy/engine/...`.
// The full hot-path budget is defended in WP14 cross-cutting tests.
func BenchmarkEvaluate(b *testing.B) {
	em := policy.NewMemoryEmitter()
	be := engine.NewEmbeddedBackend(allowAlwaysSource)
	e, err := engine.New(engine.Options{Backend: be, Emitter: em})
	if err != nil {
		b.Fatalf("engine.New: %v", err)
	}
	if err := e.Reload(context.Background(), nil); err != nil {
		b.Fatalf("Reload: %v", err)
	}
	act := fixedAction{kind: "test.smoke", inputs: map[string]any{"provider": "anthropic"}}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Evaluate(ctx, act); err != nil {
			b.Fatal(err)
		}
	}
}
