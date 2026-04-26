//go:build integration

package rpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// buildFakeServerForE2E compiles the in-tree fake MCP server and
// returns the absolute path to the binary. The build is opt-in via
// the integration build tag so the default unit-test run stays
// fast.
func buildFakeServerForE2E(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	srcDir := filepath.Join(filepath.Dir(here), "..", "mcp", "stdio", "testdata", "fake-mcp-server")
	tmpDir, err := os.MkdirTemp("", "kaneaz-fake-e2e-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	exe := filepath.Join(tmpDir, "fake-mcp-server")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fake server: %v", err)
	}
	return exe
}

// TestMCPRecipe_E2E_ToolDispatchThroughPool walks the A1 path:
//
//	construct *core.Core against a temp DataDir
//	construct a tools.API with a one-entry test catalog pointing
//	at the in-tree fake MCP server binary
//	InstallRecipe → server spawns, persists, the live pool exposes
//	its tools
//	Pool.Call(server, fake_echo, args) → response threads back
//
// This is the dynamic install + tool dispatch contract that powers
// the "user toggles a recipe and the chat surface sees its tools"
// flow. The test does NOT exercise the toolloop's GenerationRequest
// machinery — that surface is owned by the llm view's
// impl_toolloop_test, which uses the fixture pool. Here we assert
// the pool itself dispatches correctly through the dynamic add path.
func TestMCPRecipe_E2E_ToolDispatchThroughPool(t *testing.T) {
	bin := buildFakeServerForE2E(t)
	dataDir := t.TempDir()

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	pool := stdio.NewPool(stdio.PoolOptions{})
	c.SetMCP(pool)

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{
		{
			ID:          "fake-test",
			DisplayName: "Fake Test Server",
			Command:     []string{bin},
			EnvKeys:     nil,
		},
	}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()

	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    pool,
		Secrets: backend,
		DataDir: dataDir,
		// No keychain writer wired — the recipe has no env keys.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := api.InstallRecipe(ctx, "fake-test", map[string]string{}, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	// Verify the pool exposes the fake server's tools.
	rs, err := api.RecipeStatus(ctx, "fake-test")
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if rs.State != string(stdio.StateRunning) {
		t.Fatalf("recipe state = %q, want running", rs.State)
	}

	listed, err := api.ListRecipes(ctx)
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	if len(listed) != 1 || !listed[0].Enabled {
		t.Fatalf("listed = %v, want one enabled row", listed)
	}

	// Dispatch a fake_echo call via the pool — A1 contract.
	args := json.RawMessage(`{"hello":"world"}`)
	out, err := pool.Call(ctx, "fake-test", "fake_echo", args)
	if err != nil {
		t.Fatalf("pool.Call: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("pool.Call returned empty result")
	}

	// Uninstall should kill the subprocess. Sample server-instance
	// state to verify the pool released the entry.
	if err := api.UninstallRecipe(ctx, "fake-test"); err != nil {
		t.Fatalf("UninstallRecipe: %v", err)
	}
	if got := pool.Server("fake-test"); got != nil {
		t.Fatalf("pool.Server(fake-test) = %v after Uninstall, want nil", got)
	}

	// Final teardown.
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
