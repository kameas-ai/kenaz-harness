//go:build npx_e2e

package tools_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// TestFilesystemRecipe_NpxSmoke spawns the real upstream
// @modelcontextprotocol/server-filesystem package via npx and asserts
// the initialize handshake succeeds. Skips when npx is unavailable.
//
// Gated `-tags=npx_e2e` so default CI doesn't reach the network and
// download the package on every run. This is the v1 smoke against
// the real server — pair it with the manual A1-A7 walkthrough in
// docs/mcp-recipes.md when validating a release.
func TestFilesystemRecipe_NpxSmoke(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH; skipping real-server smoke")
	}
	dataDir := t.TempDir()
	allowed := t.TempDir()

	pool := stdio.NewPool(stdio.PoolOptions{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:           "filesystem",
		DisplayName:  "Filesystem",
		Command:      []string{"npx", "-y", "@modelcontextprotocol/server-filesystem"},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
		InitTimeoutMs: 30000,
	}}}
	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})

	// 60s deadline — first-run npx cold-fetch can take >15s on a
	// slow connection. The supervisor's own init timeout is the
	// guard against an actually-broken server.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := map[string]any{"allowed_directories": []any{allowed}}
	if _, err := api.InstallRecipe(ctx, "filesystem", map[string]string{}, cfg); err != nil {
		t.Fatalf("InstallRecipe (real npx): %v", err)
	}
	t.Cleanup(func() { _ = api.UninstallRecipe(context.Background(), "filesystem") })

	rs, err := api.RecipeStatus(ctx, "filesystem")
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if rs.State != string(stdio.StateRunning) {
		t.Fatalf("recipe state = %q, want running", rs.State)
	}

	// Real-server tools/list — must include at least read_file and
	// list_directory per the upstream surface.
	toolList, err := pool.Tools(ctx)
	if err != nil {
		t.Fatalf("pool.Tools: %v", err)
	}
	want := map[string]bool{"read_file": false, "list_directory": false}
	for _, tl := range toolList {
		if tl.Server != "filesystem" {
			continue
		}
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("real filesystem server tools missing %q (got: %+v)", name, toolList)
		}
	}
}
