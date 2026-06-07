package credstore_test

// Integration test: IssueForMCPSpawn → AllowAlways → persistent snippet
// written → engine reloaded → next gate evaluation is Allow (no re-prompt).
//
// cedar-credential-policy follow-up: AllowAlways persistent grant for mcp_spawn.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// makeGrantEngineForCredstore constructs a *cedar.Engine backed by a TempDir
// and returns the engine and the data directory root. Mirrors
// makeGrantEngine in cedar/integration_test.go but lives here to avoid
// cross-package test helpers.
func makeGrantEngineForCredstore(t *testing.T) (*cedar.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, cedar.PolicyDir), 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	eng, err := cedar.NewEngine(cedar.Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng, dir
}

// TestIssueForMCPSpawn_AllowAlways_PersistentGrant is the full integration
// walk:
//
//  1. Construct a store wired with a naGate, a prompt registry, AND the
//     persistent-grant writer (WithMCPSpawnPolicyWriter).
//  2. Call IssueForMCPSpawn in a goroutine (blocks on the interactive prompt).
//  3. Resolve the pending request with AllowAlways.
//  4. IssueForMCPSpawn returns nil (spawn allowed).
//  5. A cred_allow_mcp_*.cedar file is present in <dataDir>/policy/.
//  6. The cedar engine now evaluates the mcp_spawn credential as Allow.
//  7. A second IssueForMCPSpawn call returns nil immediately (Allow path,
//     no new pending request enqueued).
func TestIssueForMCPSpawn_AllowAlways_PersistentGrant(t *testing.T) {
	eng, dataDir := makeGrantEngineForCredstore(t)
	reg := cedar.NewRegistry() // no dispatcher needed for testing

	// Build the store with all three options for the AllowAlways path.
	s := credstore.New(
		newFakeResolver(nil),
		nil,
		credstore.WithCedarGate(naGate{}, nil), // naGate → NotApplicable → prompt
		credstore.WithPromptRegistry(reg),
		credstore.WithMCPSpawnPolicyWriter(dataDir, eng),
	)
	t.Cleanup(s.Close)

	backend := newFakeMCPBackend(map[string][]byte{
		locFor("grant-recipe", "TOKEN"): []byte("tok-xyz"),
	})
	ctx := context.Background()
	recipeID := "grant-recipe"

	// ── Step 2: IssueForMCPSpawn blocks on the interactive prompt ─────
	var (
		envResult map[string]string
		issueErr  error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		envResult, issueErr = s.IssueForMCPSpawn(ctx, recipeID, []string{"TOKEN"}, backend)
	}()

	// ── Step 3: resolve AllowAlways ────────────────────────────────────
	deadline := time.Now().Add(5 * time.Second)
	var reqID string
	for time.Now().Before(deadline) {
		pending := reg.ListPending()
		if len(pending) > 0 {
			reqID = pending[0].RequestID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("no pending request appeared within 5s")
	}
	if err := reg.Resolve(reqID, cedar.DecisionAllowAlways); err != nil {
		t.Fatalf("Resolve AllowAlways: %v", err)
	}

	// ── Step 4: IssueForMCPSpawn returns nil ──────────────────────────
	<-done
	if issueErr != nil {
		t.Fatalf("IssueForMCPSpawn after AllowAlways: %v", issueErr)
	}
	if envResult["TOKEN"] != "tok-xyz" {
		t.Fatalf("TOKEN: want tok-xyz, got %q", envResult["TOKEN"])
	}

	// ── Step 5: snippet file written ─────────────────────────────────
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	entries, err := os.ReadDir(policyDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var snippetFile string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cred_allow_mcp_") && strings.HasSuffix(e.Name(), ".cedar") {
			snippetFile = e.Name()
			break
		}
	}
	if snippetFile == "" {
		t.Fatalf("no cred_allow_mcp_*.cedar file found in %s after AllowAlways", policyDir)
	}
	t.Logf("snippet written: %s", snippetFile)

	// ── Step 6: engine evaluates mcp_spawn as Allow ───────────────────
	dec := eng.Evaluate(
		ctx,
		cedar.UserUID(),
		cedar.ActionUseCredential,
		cedar.CredentialUID(recipeID, "mcp_spawn"),
		nil,
	)
	if dec.Outcome != cedar.Allow {
		t.Fatalf("engine after snippet write: want Allow for mcp_spawn, got %s (%s)",
			dec.Outcome, dec.Reason)
	}

	// ── Step 7: second IssueForMCPSpawn is Allow (no prompt) ─────────
	// Build a second store with the REAL engine as the gate (not naGate)
	// to simulate the "next process boot" case where the engine loaded
	// the persisted snippet.
	s2 := credstore.New(
		newFakeResolver(nil),
		nil,
		credstore.WithCedarGate(eng, nil), // eng now has the permit
		credstore.WithPromptRegistry(reg),
		credstore.WithMCPSpawnPolicyWriter(dataDir, eng),
	)
	t.Cleanup(s2.Close)

	env2, err2 := s2.IssueForMCPSpawn(ctx, recipeID, []string{"TOKEN"}, backend)
	if err2 != nil {
		t.Fatalf("second IssueForMCPSpawn: want nil, got %v", err2)
	}
	if env2["TOKEN"] != "tok-xyz" {
		t.Fatalf("second call TOKEN: want tok-xyz, got %q", env2["TOKEN"])
	}
	// No new prompt was enqueued.
	if reg.PendingCount() != 0 {
		t.Fatalf("second call should not prompt: %d pending", reg.PendingCount())
	}
}
