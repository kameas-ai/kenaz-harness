package fs

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Host starter policy (launch spec L09 D1, Option B): reads and edits
// inside the workspace run without a prompt; writes outside the workspace,
// dangerous-tier paths and harness state still ask. Off, the gate behaves
// exactly as before.
func TestStarterDefaults(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	workspace := filepath.Join(home, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := newRealChain(t, GateOptions{
		StateDirs:    HarnessStateDirs(dataDir),
		WorkspaceDir: func() string { return workspace },
	})

	asks := func(op Op, p string) {
		t.Helper()
		ch := rc.evalAsync(op, p)
		ps := rc.waitPending(t, 1)
		_ = rc.registry.Resolve(ps[0].RequestID, cedar.DecisionDeny)
		select {
		case r := <-ch:
			if r.d.Outcome != cedar.Deny {
				t.Fatalf("%s %s: outcome %s after a denied prompt", op, p, r.d.Outcome)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s %s: did not return", op, p)
		}
	}

	// Off (the legacy default): every unmatched request prompts.
	asks(OpRead, filepath.Join(outside, "a.txt"))
	asks(OpWrite, filepath.Join(workspace, "a.txt"))

	on := true
	rc.gate.SetStarterDefaults(func() bool { return on })

	rc.evalNoPrompt(t, OpRead, filepath.Join(outside, "a.txt"))
	rc.evalNoPrompt(t, OpRead, filepath.Join(workspace, "sub", "b.txt"))
	rc.evalNoPrompt(t, OpWrite, filepath.Join(workspace, "a.txt"))
	rc.evalNoPrompt(t, OpWrite, filepath.Join(workspace, "new", "dir", "c.txt"))

	// A link inside the workspace that points out of it is outside.
	// (Each denied ask below uses its own directory: the registry
	// remembers a denial for a directory for a short while.)
	link := filepath.Join(workspace, "out")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	asks(OpWrite, filepath.Join(link, "x.txt"))
	// Writes outside the workspace still ask.
	asks(OpWrite, filepath.Join(home, "other", "y.txt"))
	// Dangerous-tier reads still ask (an absolute entry: the ~/ entries
	// expand against the real home, not this test's temp dir).
	if runtime.GOOS != "windows" {
		asks(OpRead, "/etc/kenaz-starter-test-nonexistent")
	}
	// Harness state still asks, for reads and writes.
	asks(OpRead, filepath.Join(dataDir, "data.db"))
	asks(OpWrite, filepath.Join(dataDir, "policy", "x.cedar"))

	// Live: switching the default off brings the prompts back.
	on = false
	asks(OpRead, filepath.Join(home, "third", "b.txt"))

	// The starter policy never overrides a forbid.
	on = true
	forbid := `forbid(principal, action == Action::"read_filesystem", resource) when { context.canonical_path like "*/secret.txt" };`
	if err := os.MkdirAll(rc.policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rc.policyDir, "user_forbid_secret.cedar"), []byte(forbid), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, ok := rc.gate.opts.Engine.(interface{ Reload(context.Context) error }); ok {
		if err := r.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	d, err := rc.gate.Evaluate(context.Background(), OpRead, filepath.Join(outside, "secret.txt"))
	if err != nil || d.Outcome != cedar.Deny {
		t.Fatalf("forbidden read under the starter policy: %s %v, want Deny", d.Outcome, err)
	}
}
