package cache_test

import (
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/cache"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

// BenchmarkCache_WarmHit measures the warm-hit path. NFR-001 requires
// p99 < 1 ms; this benchmark is the canonical instrumentation.
func BenchmarkCache_WarmHit(b *testing.B) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "BENCH"}
	c.Put(r, "fake", newSecret("v"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(r)
	}
}
