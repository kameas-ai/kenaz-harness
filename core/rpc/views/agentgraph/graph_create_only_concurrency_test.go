package agentgraph_test

// graph_create_only_concurrency_test.go — N2 from the second round of
// PR #304's independent review.
//
// Create-only for a non-user save must be ATOMIC, not stat-then-rename.
// The first repair (F1) fixed the semantics — existence stopped meaning
// "does it parse" — but two concurrent model drafts for the same id
// could both pass os.Stat, both write the same shared `full + ".tmp"`
// path, and both rename: one silently loses, and interleaved writes to
// the shared scratch name can land a spliced file.
//
// This test lives at the Manager seam deliberately. The first attempt
// drove it through the MCP dispatch path in core/rpc, where it passed
// against the BROKEN implementation — that path serialises per session,
// so the goroutines never overlapped and the test proved nothing. A
// concurrency test that cannot observe the race is worse than no test:
// it reports coverage it does not have.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestSaveGraph_CreateOnly_ConcurrentModelSaves_OneWinner asserts exactly
// one of N concurrent model-initiated saves of the same id wins, the
// losers are refused with GraphExistsError, and the file on disk is one
// writer's payload intact — never a splice.
//
// Falsifiable: replace the O_CREATE|O_EXCL write in saveGraph with
// os.Stat + shared-tmp + os.Rename and this fails on the winner count or
// on the spliced-content assertion. Verified to fail that way.
func TestSaveGraph_CreateOnly_ConcurrentModelSaves_OneWinner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(
		graphview.WithDataDir(dir),
		graphview.WithAuthoringEnabled(func() bool { return true }),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	a := graphview.New(mgr)
	ctx := context.Background()

	const id = "zz_create_only_concurrent"
	const n = 16

	// Distinct bodies so a spliced file is detectable: each carries a
	// unique node id, and an intact result contains exactly one.
	body := func(i int) string {
		return fmt.Sprintf("spec_version: \"1\"\nid: %s\nentrypoints: [n%d]\nnodes:\n  - id: n%d\n    kind: plan\n    attrs:\n      verbosity: verbose\n", id, i, i)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins int
	var exists int
	var others []error
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			serr := a.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: body(i)}, "model")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case serr == nil:
				wins++
			case isGraphExists(serr):
				exists++
			default:
				others = append(others, serr)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("unexpected errors from concurrent saves: %v", others)
	}
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1 (GraphExistsError refusals = %d)", wins, exists)
	}
	if exists != n-1 {
		t.Fatalf("GraphExistsError refusals = %d, want %d", exists, n-1)
	}

	got, rerr := os.ReadFile(filepath.Join(dir, "agent_graph", "library", id+".yaml"))
	if rerr != nil {
		t.Fatalf("read persisted graph: %v", rerr)
	}
	present := 0
	for i := 0; i < n; i++ {
		// Match the whole token: "id: n1" is a prefix of "id: n10".
		if strings.Contains(string(got), fmt.Sprintf("id: n%d\n", i)) {
			present++
		}
	}
	if present != 1 {
		t.Fatalf("persisted graph carries %d distinct node ids — two writers interleaved.\n%s", present, got)
	}
	if _, perr := coreag.LoadYAML(got); perr != nil {
		t.Fatalf("persisted graph does not parse — spliced write: %v\n%s", perr, got)
	}

	// No scratch files left behind.
	entries, _ := os.ReadDir(filepath.Join(dir, "agent_graph", "library"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("scratch file left behind: %s", e.Name())
		}
	}
}

func isGraphExists(err error) bool {
	var gee *graphview.GraphExistsError
	return errors.As(err, &gee)
}
