package sentry

// impl_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-7 / WP12. AC-030, driven through the real Impl (the actual
// crash-path wiring Sentry_GenerateLocalReport + Sentry_GetLastFive
// call), not just the underlying core/sentry primitives —
// core/sentry/wp12_test.go covers those directly since it cannot import
// this package without a cycle risk; this file is the wiring proof.

import (
	"context"
	"testing"
)

// TestImpl_GenerateLocalReport_AppendsToCache is AC-030's wiring proof:
// Impl.GenerateLocalReport itself (not a hand-rolled call to
// coresentry.AppendToCache) must append a cache entry that
// Impl.GetLastFive then returns.
//
// Mutation (performed by hand, see the WP12 commit body for the run):
// removing the coresentry.AppendToCache call from Impl.GenerateLocalReport
// makes this fail.
func TestImpl_GenerateLocalReport_AppendsToCache(t *testing.T) {
	dir := t.TempDir()
	impl := &Impl{DataDir: dir}
	ctx := context.Background()

	before, err := impl.GetLastFive(ctx)
	if err != nil {
		t.Fatalf("GetLastFive (before): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("setup: expected empty cache, got %d entries", len(before))
	}

	result, err := impl.GenerateLocalReport(ctx)
	if err != nil {
		t.Fatalf("GenerateLocalReport: %v", err)
	}
	if result.Path == "" {
		t.Fatalf("GenerateLocalReport returned an empty path")
	}

	after, err := impl.GetLastFive(ctx)
	if err != nil {
		t.Fatalf("GetLastFive (after): %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("GetLastFive after GenerateLocalReport = %d entries, want 1 — the crash path is not appending to the cache", len(after))
	}
	if after[0].Kind != "local_report" {
		t.Errorf("cached entry Kind = %q, want %q", after[0].Kind, "local_report")
	}
}
