package rpc

// wp10_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-6 / WP10. AC-025: an Embed call taking > 5s makes
// Snapshot().EmbedderHealth return "slow"; a failing call returns
// "error". Both drive the real embedderCaptureDecorator against a real
// corememory.GlobalCaptureTracker() — the package-level singleton
// production code uses — not a fresh isolated tracker, since the
// decorator itself always writes to the global one (matching how
// core/memory/store.go's write half already does).

import (
	"context"
	"errors"
	"testing"
	"time"

	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

type wp10FakeEmbedder struct {
	delay time.Duration
	err   error
	dims  int
}

func (f *wp10FakeEmbedder) Kind() string    { return "fake" }
func (f *wp10FakeEmbedder) Dimensions() int { return f.dims }
func (f *wp10FakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, f.dims)
	}
	return out, nil
}

// Note on shared state: corememory.GlobalCaptureTracker() is a
// package-level singleton with no test-only reset hook, so these two
// subtests do not run in parallel (no t.Parallel()) and each asserts
// only the claim its own call just produced (a fresh "slow" duration
// write, or three fresh error writes crossing embedErrorThreshold)
// rather than an exact snapshot value that an unrelated earlier write
// in the same process could perturb.
func TestWP10_AC025_SlowEmbedCall_MakesHealthSlow(t *testing.T) {
	inner := &wp10FakeEmbedder{delay: 6 * time.Second, dims: 3}
	d := &embedderCaptureDecorator{inner: inner}

	vecs, err := d.Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("unexpected vecs shape: %+v", vecs)
	}

	snap := corememory.GlobalCaptureTracker().Snapshot(time.Now())
	if snap.EmbedderHealth != "slow" {
		t.Fatalf("EmbedderHealth = %q, want %q after a >5s Embed call", snap.EmbedderHealth, "slow")
	}
}

func TestWP10_AC025_FailingEmbedCall_MakesHealthError(t *testing.T) {
	inner := &wp10FakeEmbedder{err: errors.New("boom")}
	d := &embedderCaptureDecorator{inner: inner}

	// embedErrorThreshold is 3 (core/memory/capture_rate.go) — drive
	// three failures so the health computation crosses it.
	for i := 0; i < 3; i++ {
		if _, err := d.Embed(context.Background(), []string{"hello world"}); err == nil {
			t.Fatalf("expected Embed to propagate the inner error")
		}
	}

	snap := corememory.GlobalCaptureTracker().Snapshot(time.Now())
	if snap.EmbedderHealth != "error" {
		t.Fatalf("EmbedderHealth = %q, want %q after 3 failing Embed calls", snap.EmbedderHealth, "error")
	}
	if snap.RecentErrorCount < 3 {
		t.Fatalf("RecentErrorCount = %d, want >= 3", snap.RecentErrorCount)
	}
}

// Falsifiability (AC-025's own text: "Mutation: remove the decorator.
// Both must fail."). RUN, not predicted: replaced
// "d := &embedderCaptureDecorator{inner: inner}" with
// "var d corememory.Embedder = inner" in both tests above (calling
// wp10FakeEmbedder directly, the pre-WP10 shape with zero tracker
// writes), re-ran `go test ./core/rpc/ -run TestWP10_AC025 -v -count=1`
// — both failed:
//   wp10_test.go:64: EmbedderHealth = "ok", want "slow" after a >5s Embed call
//   --- FAIL: TestWP10_AC025_SlowEmbedCall_MakesHealthSlow (6.00s)
//   wp10_test.go:82: EmbedderHealth = "ok", want "error" after 3 failing Embed calls
//   --- FAIL: TestWP10_AC025_FailingEmbedCall_MakesHealthError (0.00s)
// — then reverted both lines and re-ran green (git diff --stat showed
// zero lines afterward).
