package cedar_test

// Unit and integration tests for WriteMCPSpawnSnippet and GateMCPSpawn
// AllowAlways persistent-grant path.
//
// cedar-credential-policy follow-up: AllowAlways persistent grant for mcp_spawn.
//
// Coverage:
//   1. TestWriteMCPSpawnSnippet_CanonicalFilename — filename is
//      "cred_allow_mcp_<sanitized>.cedar" and passes isPolicyFilename /
//      familyFromFilename (cred family).
//   2. TestWriteMCPSpawnSnippet_ValidCedarSyntax — snippet body parses as
//      valid Cedar 4 (via cedar.NewPolicySetFromBytes).
//   3. TestWriteMCPSpawnSnippet_NilOrEmptyNoop — nil engine or empty
//      dataDir returns nil without touching the filesystem.
//   4. TestWriteMCPSpawnSnippet_EngineReloadedAfterWrite — after
//      WriteMCPSpawnSnippet the engine evaluates the mcp_spawn credential
//      as Allow (no re-prompt on second gate call).
//   5. TestGateMCPSpawn_AllowAlways_PersistentGrant — full end-to-end:
//      prompt → AllowAlways → snippet file written → engine reloaded →
//      subsequent GateMCPSpawn short-circuits via Allow without prompting.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// ── helpers ────────────────────────────────────────────────────────────────

// makePolicyEngineWithDir constructs a *cedar.Engine backed by a TempDir.
// The policy directory is created so the engine can load from disk.
func makePolicyEngineWithDir(t *testing.T) (*cedar.Engine, string) {
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

// evalMCPSpawn calls engine.Evaluate for the mcp_spawn credential family
// and returns the Decision.
func evalMCPSpawn(t *testing.T, eng *cedar.Engine, recipeID string) cedar.Decision {
	t.Helper()
	return eng.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionUseCredential,
		cedar.CredentialUID(recipeID, "mcp_spawn"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String("purpose"): cedarlib.String("mcp_spawn"),
			cedarlib.String("ref_id"):  cedarlib.String(recipeID),
		},
	)
}

// ── 1. Canonical filename ──────────────────────────────────────────────────

// TestWriteMCPSpawnSnippet_CanonicalFilename verifies:
//   - The written file exists and has the expected name.
//   - The name starts with "cred_allow_mcp_" (satisfies familyFromFilename
//     "cred_allow_" prefix → Family = "cred").
//   - The name ends with ".cedar".
//   - Characters outside [a-z0-9\-.] in the recipeID are replaced with "_".
func TestWriteMCPSpawnSnippet_CanonicalFilename(t *testing.T) {
	t.Parallel()
	eng, dataDir := makePolicyEngineWithDir(t)

	cases := []struct {
		recipeID     string
		wantFilename string
	}{
		{"github", "cred_allow_mcp_github.cedar"},
		{"my-server", "cred_allow_mcp_my-server.cedar"},
		{"My_Server/123", "cred_allow_mcp_my_server_123.cedar"},
		{"SQLite DB", "cred_allow_mcp_sqlite_db.cedar"},
		{"recipe.v2", "cred_allow_mcp_recipe.v2.cedar"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.recipeID, func(t *testing.T) {
			t.Parallel()
			if err := cedar.WriteMCPSpawnSnippet(context.Background(), dataDir, eng, tc.recipeID); err != nil {
				t.Fatalf("WriteMCPSpawnSnippet(%q): %v", tc.recipeID, err)
			}
			path := filepath.Join(dataDir, cedar.PolicyDir, tc.wantFilename)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Fatalf("expected file %s not found", tc.wantFilename)
			}
			if !strings.HasPrefix(tc.wantFilename, "cred_allow_") {
				t.Fatalf("filename %q does not start with cred_allow_", tc.wantFilename)
			}
			if !strings.HasSuffix(tc.wantFilename, ".cedar") {
				t.Fatalf("filename %q does not end with .cedar", tc.wantFilename)
			}
		})
	}
}

// ── 2. Valid Cedar 4 syntax ────────────────────────────────────────────────

// TestWriteMCPSpawnSnippet_ValidCedarSyntax verifies the snippet body
// parses without error via cedar.NewPolicySetFromBytes (Cedar 4 parser).
func TestWriteMCPSpawnSnippet_ValidCedarSyntax(t *testing.T) {
	t.Parallel()
	eng, dataDir := makePolicyEngineWithDir(t)

	recipeID := "test-syntax-check"
	if err := cedar.WriteMCPSpawnSnippet(context.Background(), dataDir, eng, recipeID); err != nil {
		t.Fatalf("WriteMCPSpawnSnippet: %v", err)
	}

	filename := "cred_allow_mcp_test-syntax-check.cedar"
	path := filepath.Join(dataDir, cedar.PolicyDir, filename)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Cedar 4 parser rejects bad syntax.
	if _, parseErr := cedarlib.NewPolicySetFromBytes(filename, body); parseErr != nil {
		t.Fatalf("snippet body is not valid Cedar 4: %v\nbody:\n%s", parseErr, body)
	}

	// Must contain a permit block targeting the expected resource UID.
	content := string(body)
	if !strings.Contains(content, `use_credential`) {
		t.Error("snippet should reference action use_credential")
	}
	wantResource := `Credential::"test-syntax-check::mcp_spawn"`
	if !strings.Contains(content, wantResource) {
		t.Errorf("snippet should reference resource %s\nbody:\n%s", wantResource, content)
	}
}

// ── 3. Nil/empty no-op ─────────────────────────────────────────────────────

// TestWriteMCPSpawnSnippet_NilOrEmptyNoop verifies that WriteMCPSpawnSnippet
// returns nil without writing anything when dataDir is empty or engine is nil.
func TestWriteMCPSpawnSnippet_NilOrEmptyNoop(t *testing.T) {
	t.Parallel()
	eng, dataDir := makePolicyEngineWithDir(t)

	t.Run("nil_engine", func(t *testing.T) {
		t.Parallel()
		if err := cedar.WriteMCPSpawnSnippet(context.Background(), dataDir, nil, "recipe-a"); err != nil {
			t.Fatalf("nil engine: want nil error, got %v", err)
		}
		// No file should have been written.
		entries, _ := os.ReadDir(filepath.Join(dataDir, cedar.PolicyDir))
		for _, e := range entries {
			if strings.Contains(e.Name(), "recipe-a") {
				t.Fatalf("nil engine: unexpected file written: %s", e.Name())
			}
		}
	})

	t.Run("empty_datadir", func(t *testing.T) {
		t.Parallel()
		if err := cedar.WriteMCPSpawnSnippet(context.Background(), "", eng, "recipe-b"); err != nil {
			t.Fatalf("empty dataDir: want nil error, got %v", err)
		}
	})
}

// ── 4. Engine reloaded after write ────────────────────────────────────────

// TestWriteMCPSpawnSnippet_EngineReloadedAfterWrite verifies that after
// WriteMCPSpawnSnippet the engine evaluates the mcp_spawn credential as
// Allow for the target recipe (without prompting).
func TestWriteMCPSpawnSnippet_EngineReloadedAfterWrite(t *testing.T) {
	t.Parallel()
	eng, dataDir := makePolicyEngineWithDir(t)
	recipeID := "my-recipe"

	// Before write: default policy has no mcp_spawn rule → NotApplicable.
	d1 := evalMCPSpawn(t, eng, recipeID)
	if d1.Outcome != cedar.NotApplicable {
		t.Fatalf("before write: want NotApplicable, got %s (%s)", d1.Outcome, d1.Reason)
	}

	// Write snippet.
	if err := cedar.WriteMCPSpawnSnippet(context.Background(), dataDir, eng, recipeID); err != nil {
		t.Fatalf("WriteMCPSpawnSnippet: %v", err)
	}

	// After write+reload: engine should Allow the mcp_spawn credential.
	d2 := evalMCPSpawn(t, eng, recipeID)
	if d2.Outcome != cedar.Allow {
		t.Fatalf("after write: want Allow, got %s (%s)", d2.Outcome, d2.Reason)
	}

	// A different recipe is still NotApplicable (grant is scoped to recipeID).
	d3 := evalMCPSpawn(t, eng, "other-recipe")
	if d3.Outcome != cedar.NotApplicable {
		t.Fatalf("other recipe: want NotApplicable (grant scoped), got %s (%s)", d3.Outcome, d3.Reason)
	}
}

// ── 5. GateMCPSpawn AllowAlways end-to-end ────────────────────────────────

// allowGate is a cedar.Gate that always returns Allow.
type allowGateForSnippet struct{}

func (allowGateForSnippet) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.Allow,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
	}
}

// naGateForSnippet is a cedar.Gate that always returns NotApplicable.
type naGateForSnippet struct{}

func (naGateForSnippet) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.NotApplicable,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
	}
}

// TestGateMCPSpawn_AllowAlways_PersistentGrant exercises the full flow:
//
//  1. First GateMCPSpawn call with naGateForSnippet → NotApplicable → prompt fires.
//  2. User resolves AllowAlways.
//  3. Snippet file is written to <dataDir>/policy/cred_allow_mcp_<sanitized>.cedar.
//  4. Engine is reloaded.
//  5. Second GateMCPSpawn call with the now-loaded engine → Allow (no prompt).
func TestGateMCPSpawn_AllowAlways_PersistentGrant(t *testing.T) {
	t.Parallel()

	eng, dataDir := makePolicyEngineWithDir(t)
	reg := cedar.NewRegistry() // no dispatcher — tests drive round-trips directly
	recipeID := "allow-always-recipe"

	// ── Step 1: first call triggers the interactive prompt ─────────────
	done := make(chan error, 1)
	go func() {
		done <- cedar.GateMCPSpawn(
			context.Background(),
			naGateForSnippet{},
			reg,
			recipeID,
			dataDir,
			eng,
		)
	}()

	// ── Step 2: wait for the pending request and resolve AllowAlways ───
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

	// GateMCPSpawn must return nil (spawn is allowed).
	if err := <-done; err != nil {
		t.Fatalf("GateMCPSpawn after AllowAlways: %v", err)
	}

	// ── Step 3: snippet file must exist ────────────────────────────────
	sanitized := strings.NewReplacer("/", "_", " ", "_").Replace(
		strings.ToLower(recipeID),
	)
	// The actual sanitization is: keep [a-z0-9\-.], replace rest with _.
	// For "allow-always-recipe" the result is "allow-always-recipe".
	expectedFile := "cred_allow_mcp_" + sanitized + ".cedar"
	// Use a small loop to account for any sanitization differences.
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	entries, err := os.ReadDir(policyDir)
	if err != nil {
		t.Fatalf("ReadDir policy dir: %v", err)
	}
	var found string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cred_allow_mcp_") && strings.HasSuffix(e.Name(), ".cedar") {
			found = e.Name()
			break
		}
	}
	if found == "" {
		t.Fatalf("no cred_allow_mcp_*.cedar file found in %s; expected %s", policyDir, expectedFile)
	}

	// ── Step 4: engine must have reloaded — evaluate to Allow ─────────
	d := evalMCPSpawn(t, eng, recipeID)
	if d.Outcome != cedar.Allow {
		t.Fatalf("after AllowAlways: engine should Allow mcp_spawn for %q, got %s (%s)",
			recipeID, d.Outcome, d.Reason)
	}

	// ── Step 5: second GateMCPSpawn via Allow engine (no prompt) ──────
	// The engine now carries a permit, so the gate short-circuits at Allow.
	// We use the real engine as the gate (implements cedar.Gate).
	secondErr := cedar.GateMCPSpawn(
		context.Background(),
		eng, // real engine — should Allow immediately
		reg,
		recipeID,
		dataDir,
		eng,
	)
	if secondErr != nil {
		t.Fatalf("second GateMCPSpawn should be Allow (no prompt), got: %v", secondErr)
	}
	// Registry must be clean — no re-prompt was issued.
	if reg.PendingCount() != 0 {
		t.Fatalf("second call should not have enqueued a prompt: %d pending", reg.PendingCount())
	}
}
