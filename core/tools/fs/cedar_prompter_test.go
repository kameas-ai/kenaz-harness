package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// waitForRequestID polls (bounded) for the dispatcher to have captured a
// pending request id. Avoids a fixed sleep: the dispatcher fires
// synchronously from inside RequestInteractive before it blocks on the
// resolve channel, but from a different goroutine than the test's
// Resolve call.
func waitForRequestID(t *testing.T, mu *sync.Mutex, idPtr *string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		id := *idPtr
		mu.Unlock()
		if id != "" {
			return id
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for RequestInteractive to dispatch a pending request")
	return ""
}

// TestCedarPrompter_NilRegistry_MatchesNoOpPrompter is the nil-tolerant
// contract CedarPrompter documents: a nil Registry (the test-harness /
// nil-core wiring state) behaves exactly like NoOpPrompter, with no
// error.
func TestCedarPrompter_NilRegistry_MatchesNoOpPrompter(t *testing.T) {
	t.Parallel()
	p := &CedarPrompter{}
	resp, err := p.Prompt(context.Background(), PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/x"})
	if err != nil {
		t.Fatalf("nil Registry: unexpected error %v", err)
	}
	if resp != PromptDeny {
		t.Fatalf("nil Registry: got %v, want PromptDeny (matches NoOpPrompter)", resp)
	}
}

// TestCedarPrompter_Deny_ThroughRealRegistry proves a user Deny reaches
// the gate as a Deny outcome through the real Registry/Prompter chain
// (not a stub).
func TestCedarPrompter_Deny_ThroughRealRegistry(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var capturedID string
	registry := cedar.NewRegistry(cedar.WithDispatcher(
		cedar.PromptDispatcherFunc(func(_ context.Context, _ string, pending cedar.PendingRequest) {
			mu.Lock()
			capturedID = pending.RequestID
			mu.Unlock()
		}),
	))
	engine, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	gate := NewGate(GateOptions{
		Engine:   engine,
		Prompter: &CedarPrompter{Registry: registry},
	})

	var (
		decision cedar.Decision
		evalErr  error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		decision, evalErr = gate.Evaluate(context.Background(), OpWrite, "/tmp/cedar-prompter-deny-test.txt")
	}()

	id := waitForRequestID(t, &mu, &capturedID)
	if err := registry.Resolve(id, cedar.DecisionDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	wg.Wait()

	if evalErr != nil {
		t.Fatalf("Evaluate: %v", evalErr)
	}
	if decision.Outcome != cedar.Deny {
		t.Fatalf("Evaluate outcome = %s, want Deny", decision.Outcome)
	}
}

// TestCedarPrompter_AllowAlways_ThroughRealRegistry_PersistsSnippet is
// UNIT-20's AC-16b, driven through the REAL Prompter this WP adds
// (trust-surfaces-that-fire-01PMZ202 WP22 / B-4) rather than a stub —
// the WP's own text calls a stub-Prompter version of this test vacuous,
// since it is only satisfiable once a real Prompter exists. Proves the
// full chain: Gate.Evaluate -> NotApplicable -> CedarPrompter.Prompt ->
// cedar.Registry.RequestInteractive -> a resolved AllowAlways decision
// -> a written cedar snippet under <DataDir>/policy that a FRESH engine
// loaded from the same DataDir picks up (survives an engine reload).
func TestCedarPrompter_AllowAlways_ThroughRealRegistry_PersistsSnippet(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)

	var mu sync.Mutex
	var capturedID string
	registry := cedar.NewRegistry(cedar.WithDispatcher(
		cedar.PromptDispatcherFunc(func(_ context.Context, _ string, pending cedar.PendingRequest) {
			mu.Lock()
			capturedID = pending.RequestID
			mu.Unlock()
		}),
	))

	engine, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	gate := NewGate(GateOptions{
		Engine:    engine,
		Prompter:  &CedarPrompter{Registry: registry},
		PolicyDir: policyDir,
	})

	const target = "/tmp/cedar-prompter-real-registry-test.txt"

	var (
		decision cedar.Decision
		evalErr  error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		decision, evalErr = gate.Evaluate(context.Background(), OpWrite, target)
	}()

	id := waitForRequestID(t, &mu, &capturedID)
	if err := registry.Resolve(id, cedar.DecisionAllowAlways); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	wg.Wait()

	if evalErr != nil {
		t.Fatalf("Evaluate: %v", evalErr)
	}
	if decision.Outcome != cedar.Allow {
		t.Fatalf("Evaluate outcome = %s, want Allow", decision.Outcome)
	}

	entries, err := os.ReadDir(policyDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a cedar snippet under PolicyDir, got err=%v entries=%v", err, entries)
	}
	body, err := os.ReadFile(filepath.Join(policyDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading snippet: %v", err)
	}
	if !strings.Contains(string(body), target) {
		t.Fatalf("snippet does not reference the granted path:\n%s", body)
	}

	// Survives an engine reload: a FRESH engine loaded from the same
	// DataDir picks up the persisted grant with no Prompter involved at
	// all. write_filesystem has no permit in the embedded default
	// policy (C-15 — the only occurrence of the string is a comment),
	// so an Allow here can only come from the snippet just written.
	reloaded, err := cedar.NewEngine(cedar.Options{
		DataDir:         dataDir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine (reload from DataDir): %v", err)
	}
	// Re-evaluate through a FRESH Gate over the reloaded engine, with no
	// Prompter configured (NoOpPrompter default -> PromptDeny). If the
	// snippet did not survive the reload, write_filesystem resolves
	// NotApplicable (C-15: no other permit exists for it) and this
	// gate denies; if it did survive, the exact-path permit matches and
	// this gate allows without ever consulting a Prompter — proving
	// persistence, not merely re-testing the in-memory grant.
	reloadGate := NewGate(GateOptions{Engine: reloaded})
	reloadDecision, err := reloadGate.Evaluate(context.Background(), OpWrite, target)
	if err != nil {
		t.Fatalf("reload Evaluate: %v", err)
	}
	if reloadDecision.Outcome != cedar.Allow {
		t.Fatalf("reloaded engine outcome = %s, want Allow (grant did not survive reload)", reloadDecision.Outcome)
	}
}
