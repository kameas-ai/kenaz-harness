package cedar

// engine_reload_test.go — concurrent evaluate-during-reload race tests.
// (cedar-policy-editor-ui-01KQ8TD6 WP04)
//
// Uses the white-box package (no _test suffix) so it can access the
// private Engine field types for setup without going through RPC.
//
// The -race detector combined with -short limits the test duration to
// keep CI fast, while still exercising the atomic.Pointer swap path
// under real goroutine pressure.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEngine_ConcurrentEvaluateDuringReload fires 50 concurrent
// goroutines that each call Evaluate, while a separate goroutine
// continuously saves new .cedar files and calls Reload. The test runs
// for 2 seconds (respects -short) and asserts:
//
//   - No goroutine panics or races (caught by the race detector).
//   - Every Evaluate call returns Allow or Deny — never a zero-value
//     Decision that would indicate a torn read.
func TestEngine_ConcurrentEvaluateDuringReload(t *testing.T) {
	if testing.Short() {
		// Under -short we still exercise the path but reduce duration.
	}
	t.Parallel()

	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Seed one valid file so the engine starts with a known-good bundle.
	seed := []byte(`permit (principal, action, resource);`)
	if err := os.WriteFile(filepath.Join(policyDir, "seed.cedar"), seed, 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}

	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx := context.Background()

	// duration caps the test run; keep short for CI.
	duration := 2 * time.Second
	if testing.Short() {
		duration = 500 * time.Millisecond
	}

	deadline := time.Now().Add(duration)

	var (
		wg         sync.WaitGroup
		evalCount  atomic.Int64
		zeroCount  atomic.Int64 // torn-read detector
		reloadCount atomic.Int64
	)

	// Reload goroutine: repeatedly writes a new policy file and reloads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for time.Now().Before(deadline) {
			// Alternate between a permit and a forbid to create
			// different PolicySets, exercising the swap path.
			var src string
			if i%2 == 0 {
				src = `permit (principal, action, resource);`
			} else {
				src = fmt.Sprintf(`
permit (principal, action, resource);
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"reload_test__%d"
);`, i)
			}
			name := filepath.Join(policyDir, fmt.Sprintf("reload-%d.cedar", i%5))
			// Overwrite in a tight loop — intentionally racy on the
			// file content to force varied reload results.
			_ = os.WriteFile(name, []byte(src), 0o644)
			if err := e.Reload(ctx); err != nil {
				// Reload errors on partial writes are acceptable.
				_ = err
			}
			reloadCount.Add(1)
			i++
			// Tiny yield to let evaluators run.
			time.Sleep(time.Millisecond)
		}
	}()

	// 50 Evaluate goroutines — sustained throughout the duration.
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				d := e.Evaluate(
					ctx,
					UserUID(),
					ActionToolExec,
					ToolUID("reload_test", "check"),
					nil,
				)
				evalCount.Add(1)
				// "unknown" outcome from Outcome.String() indicates a value
				// outside the defined enum — which would mean the atomic
				// pointer returned a torn or uninitialized PolicySet.
				if d.Outcome.String() == "unknown" {
					zeroCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("reloads=%d evaluations=%d zero-outcome=%d",
		reloadCount.Load(), evalCount.Load(), zeroCount.Load())

	if n := zeroCount.Load(); n > 0 {
		t.Errorf("detected %d torn-read Evaluate calls (zero Outcome); atomic swap is broken", n)
	}
	if n := evalCount.Load(); n == 0 {
		t.Error("no Evaluate calls completed — test did not exercise the concurrent path")
	}
	if n := reloadCount.Load(); n == 0 {
		t.Error("no Reload calls completed — test did not exercise the concurrent path")
	}
}

// TestEngine_ReloadPreservesPriorOnPartialFailure verifies that when
// one disk file is broken, the engine keeps serving valid decisions
// from the remaining good files (per-file failure tolerance).
//
// This complements TestEngine_InvalidPolicyFile_Tolerated in engine_test.go
// by exercising a hot Reload call on an already-booted engine, not just
// the initial load.
func TestEngine_ReloadPreservesPriorOnPartialFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Start with a good policy that explicitly denies a specific tool.
	const goodSrc = `
permit (principal, action, resource);
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"sentinel__restricted"
);`
	if err := os.WriteFile(filepath.Join(policyDir, "good.cedar"), []byte(goodSrc), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}

	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ctx := context.Background()

	// Confirm initial state: sentinel tool is denied.
	d := e.Evaluate(ctx, UserUID(), ActionToolExec, ToolUID("sentinel", "restricted"), nil)
	if d.Outcome != Deny {
		t.Fatalf("initial state: expected Deny for sentinel__restricted, got %s", d.Outcome)
	}

	// Now write a broken file alongside the good one and reload.
	if err := os.WriteFile(filepath.Join(policyDir, "broken.cedar"), []byte(`DEFINITELY NOT CEDAR`), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}
	// Reload should NOT return an error — broken file is tolerated.
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload with one broken file should not error, got: %v", err)
	}

	// Engine must still serve decisions from the good file.
	// With the embedded default permit + good file's forbid, sentinel must still be denied.
	d = e.Evaluate(ctx, UserUID(), ActionToolExec, ToolUID("sentinel", "restricted"), nil)
	if d.Outcome != Deny {
		t.Fatalf("after partial-failure Reload: expected Deny for sentinel__restricted, got %s (reason=%s)",
			d.Outcome, d.Reason)
	}

	// The broken file must surface in ListPolicies with ParseOK=false.
	var sawBroken bool
	for _, f := range e.ListPolicies() {
		if f.Name == "broken.cedar" && !f.ParseOK {
			sawBroken = true
		}
	}
	if !sawBroken {
		t.Error("broken.cedar should appear in ListPolicies with ParseOK=false")
	}
}
