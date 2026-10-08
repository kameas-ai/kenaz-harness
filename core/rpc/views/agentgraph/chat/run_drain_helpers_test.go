package chat

import (
	"context"
	"testing"
	"time"
)

// drainRunnerOnCleanup waits for every in-flight run of runner (its exit
// path included) before the test's storage is torn down. Register it AFTER
// the db.Close cleanup: t.Cleanup is LIFO, so it then runs first. Without
// it the run goroutine's deferred DeleteStreamCheckpoint raced db.Close
// (CI run 37705330106).
func drainRunnerOnCleanup(t *testing.T, runner *ChatRunner) {
	t.Helper()
	t.Cleanup(func() { drainRunner(t, runner) })
}

// drainRunner is the defer-form of drainRunnerOnCleanup, for tests that
// close their DB with defer (defer it after the db.Close defer).
func drainRunner(t *testing.T, runner *ChatRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Drain(ctx); err != nil {
		t.Errorf("ChatRunner.Drain: %v — a run outlived the test", err)
	}
}
