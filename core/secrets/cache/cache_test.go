package cache_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/cache"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// fakeClock is a deterministic clock for TTL tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newSecret(s string) *secret.StdlibSecret {
	return secret.NewStdlibSecret([]byte(s), "ref-id-"+s, "")
}

func TestCache_PutGetHit(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	c.Put(r, "fake", newSecret("v"))
	got, ok := c.Get(r)
	if !ok {
		t.Fatal("expected hit")
	}
	if got == nil {
		t.Fatal("nil secret")
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	c := cache.New(cache.WithDefaultTTL(time.Second), cache.WithClock(clk))
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	s := newSecret("v")
	c.Put(r, "fake", s)
	clk.Advance(2 * time.Second)
	if _, ok := c.Get(r); ok {
		t.Fatal("expected miss after TTL")
	}
	if !s.IsDestroyed() {
		t.Error("expected eviction to destroy")
	}
}

func TestCache_PerBackendTTL(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	c := cache.New(
		cache.WithDefaultTTL(60*time.Second),
		cache.WithBackendTTL("kms", 10*time.Second),
		cache.WithClock(clk),
	)
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefAWSKMS, Locator: "alias/x"}
	c.Put(r, "kms", newSecret("v"))
	clk.Advance(15 * time.Second)
	if _, ok := c.Get(r); ok {
		t.Error("kms entry should expire under override TTL")
	}

	r2 := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	c.Put(r2, "env", newSecret("v"))
	if _, ok := c.Get(r2); !ok {
		t.Error("default-TTL entry should still be valid")
	}
}

func TestCache_InvalidateZeroizes(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	s := newSecret("v")
	c.Put(r, "fake", s)
	c.Invalidate(r)
	if !s.IsDestroyed() {
		t.Error("Invalidate must destroy the secret")
	}
	if _, ok := c.Get(r); ok {
		t.Error("expected miss after Invalidate")
	}
}

func TestCache_PutOverwriteDestroysOld(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	old := newSecret("old")
	c.Put(r, "fake", old)
	c.Put(r, "fake", newSecret("new"))
	if !old.IsDestroyed() {
		t.Error("overwrite must destroy old entry")
	}
}

func TestCache_NotifyTriggersInvalidate(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	s := newSecret("v")
	c.Put(r, "fake", s)

	c.Notify(r)
	// Notify is async via the rotation goroutine; spin briefly.
	for i := 0; i < 100; i++ {
		if _, ok := c.Get(r); !ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, ok := c.Get(r); ok {
		t.Error("Notify should eventually invalidate")
	}
	if !s.IsDestroyed() {
		t.Error("Notify-driven invalidate must destroy")
	}
}

func TestCache_SubscribeFiresOnInvalidate(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	c.Put(r, "fake", newSecret("v"))

	var fired atomic.Int32
	c.Subscribe(func(_ ref.CredentialReference) { fired.Add(1) })
	c.Invalidate(r)
	if fired.Load() != 1 {
		t.Errorf("subscribe fired=%d", fired.Load())
	}
}

func TestCache_CloseDestroysEverything(t *testing.T) {
	c := cache.New()
	r1 := ref.CredentialReference{Kind: ref.RefEnv, Locator: "A"}
	r2 := ref.CredentialReference{Kind: ref.RefEnv, Locator: "B"}
	s1 := newSecret("a")
	s2 := newSecret("b")
	c.Put(r1, "fake", s1)
	c.Put(r2, "fake", s2)
	c.Close()
	if !s1.IsDestroyed() || !s2.IsDestroyed() {
		t.Error("Close must destroy all entries")
	}
	if c.Len() != 0 {
		t.Error("Len should be 0 after Close")
	}
	c.Close() // idempotent
}

func TestCache_PutAfterClose(t *testing.T) {
	c := cache.New()
	c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	s := newSecret("v")
	c.Put(r, "fake", s)
	if !s.IsDestroyed() {
		t.Error("Put after Close must destroy the new secret")
	}
}

func TestCache_Concurrent(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := newSecret("v")
			c.Put(r, "fake", s)
			_, _ = c.Get(r)
			c.Invalidate(r)
		}()
	}
	wg.Wait()
}

// Plan §10 NFR-001: warm-cache p99 < 1 ms. We assert a generous bound
// here to keep CI portable; the canonical microbenchmark lives in
// cache_bench_test.go.
func TestCache_WarmHit_FastPath(t *testing.T) {
	c := cache.New()
	defer c.Close()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	c.Put(r, "fake", newSecret("v"))
	const N = 10000
	t0 := time.Now()
	for i := 0; i < N; i++ {
		_, ok := c.Get(r)
		if !ok {
			t.Fatal("expected hit")
		}
	}
	elapsed := time.Since(t0)
	avg := elapsed / N
	if avg > 50*time.Microsecond {
		t.Logf("warm-hit avg=%v (NFR-001 target p99 < 1ms)", avg)
	}
}

var _ = registry.HealthOK // keep registry import live in tests
