package bash_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// leakSentinel is a distinctive plaintext value, grep-able in any leaked
// artifact, matching the reviewer's reproduction ("SUPERSECRET-PLAINTEXT-123").
const leakSentinel = "SUPERSECRET-PLAINTEXT-123"

// syncBuffer is a race-safe io.Writer + reader, per CLAUDE.md's race-safe
// test fake pattern: writes come from the tool's logger (its own goroutine
// via slog's internal commonHandler mutex), reads come from the test body.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// setupSecretResolverCtx wires a resolver + per-turn sanitizer context with
// one locator resolving to leakSentinel, mirroring the production wiring
// driveRun installs (refs.WithResolver / refs.WithTurnSanitizer) and the
// exact setup the reviewer's reproduction used.
func setupSecretResolverCtx(t *testing.T) context.Context {
	t.Helper()
	idx := secrets.NewExposureIndex()
	idx.Add(secrets.ExposedEntry{
		Locator:     "user:tok",
		Description: "test secret",
		Scope:       secrets.ScopeSession,
		KindHint:    secrets.KindHintBearer,
	}, []byte(leakSentinel))
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)
	return ctx
}

// TestRunInBackground_DoesNotPersistOrLogResolvedSecret is the falsification
// test for LEAK 2: bash's background arm persists and logs the
// POST-substitution command line.
//
//   - core/tools/bash/background.go passes the resolved commandLine to
//     BackgroundSpawn, which core/rpc/builtins_wiring.go wires straight into
//     taskReg.Register -> core/tasks/registry.go Insert -> store_sql.go's
//     `INSERT INTO tasks (..., cmd, ...)`. This test's fake registry stands
//     in for that store write; it asserts on the exact Cmd value the real
//     SQL INSERT would receive, not on any other in-memory value.
//   - background.go also logs truncateForLog(commandLine, 120) under
//     bash.background.spawned. This test asserts on the actual formatted
//     log line a real *slog.Logger emits (the same Logger production wires
//     via core/rpc/builtins_wiring.go:195 logging.L()), not on an
//     intermediate string.
func TestRunInBackground_DoesNotPersistOrLogResolvedSecret(t *testing.T) {
	ctx := setupSecretResolverCtx(t)

	type bgTask struct {
		sessionID   string
		cmd         string
		description string
	}
	var mu sync.Mutex
	tasks := make(map[string]bgTask)
	ended := make(map[string]int)

	spawn := func(_ context.Context, sessionID, cmd, description string, _ int) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		id := fmt.Sprintf("task-fake-%d", len(tasks)+1)
		tasks[id] = bgTask{sessionID: sessionID, cmd: cmd, description: description}
		return id, nil
	}
	end := func(_ context.Context, taskID string, exitCode int) {
		mu.Lock()
		defer mu.Unlock()
		ended[taskID] = exitCode
	}
	snapshot := func() (map[string]bgTask, map[string]int) {
		mu.Lock()
		defer mu.Unlock()
		tc := make(map[string]bgTask, len(tasks))
		for k, v := range tasks {
			tc[k] = v
		}
		ec := make(map[string]int, len(ended))
		for k, v := range ended {
			ec[k] = v
		}
		return tc, ec
	}

	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	tool := bash.New(bash.Options{
		SandboxRoot:      "/tmp",
		Allowlist:        []string{"echo", "sh", "bash"},
		BackgroundSpawn:  spawn,
		BackgroundEnd:    end,
		Logger:           logger,
	})

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo @secret:user:tok",
		"run_in_background": true,
		"description":       "leak test",
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TaskID == "" {
		t.Fatal("task_id is empty")
	}

	// Wait for the background process to actually finish so the exit-code
	// path (and the exited log line) can't race the assertions below.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, ec := snapshot()
		if _, ok := ec[out.TaskID]; ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	tc, _ := snapshot()
	task, ok := tc[out.TaskID]
	if !ok {
		t.Fatalf("task %s not found in registry", out.TaskID)
	}

	// This is the exact value that would be written to the tasks.cmd SQL
	// column in production. It must be the pre-substitution command, not
	// the resolved plaintext.
	if strings.Contains(task.cmd, leakSentinel) {
		t.Errorf("LEAK: registry Cmd carries resolved plaintext: %q", task.cmd)
	}
	if task.cmd != "echo @secret:user:tok" {
		t.Errorf("registry Cmd = %q; want unresolved form %q", task.cmd, "echo @secret:user:tok")
	}

	logged := logBuf.String()
	if strings.Contains(logged, leakSentinel) {
		t.Errorf("LEAK: bash.background.spawned log line carries resolved plaintext: %s", logged)
	}
}
