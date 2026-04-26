//go:build integration

package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// buildFakeServerForFilesystemE2E compiles the in-tree fake MCP
// server. The build is opt-in via the integration build tag so the
// default unit-test run stays hermetic.
func buildFakeServerForFilesystemE2E(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	srcDir := filepath.Join(filepath.Dir(here), "..", "..", "..", "mcp", "stdio", "testdata", "fake-mcp-server")
	tmpDir, err := os.MkdirTemp("", "kaneaz-fs-e2e-")
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

// TestFilesystemRecipe_E2E_InstallSpawnDispatchUninstall walks the
// filesystem-recipe install path (A1) end-to-end against the in-tree
// fake MCP server. The fake server doesn't speak the actual
// filesystem tool surface (read_file etc.) — its job here is to prove
// that:
//
//	(1) InstallRecipe → resolveConfig → ToServerSpec → Pool.OpenOne
//	    successfully spawns a real subprocess wired through the
//	    filesystem-shaped catalog entry (ArgsTemplate +
//	    directory_list ConfigOption).
//	(2) Pool.Tools surfaces the spawned server's tools under the
//	    expected `<recipe-id>__<tool-name>` namespace.
//	(3) Pool.Call dispatches against the recipe id and returns the
//	    fake server's result frame.
//	(4) UninstallRecipe tears down the server and the pool no longer
//	    lists it.
//
// The real filesystem tool list comes from the upstream npm package
// at runtime; verifying that surface is the job of T040
// (-tags=npx_e2e) and the manual A1-A7 checklist. This test asserts
// the install/dispatch/uninstall plumbing is correct against any MCP
// server with the filesystem-style argv shape.
func TestFilesystemRecipe_E2E_InstallSpawnDispatchUninstall(t *testing.T) {
	bin := buildFakeServerForFilesystemE2E(t)
	dataDir := t.TempDir()
	allowed := t.TempDir()

	pool := stdio.NewPool(stdio.PoolOptions{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:           "filesystem",
		DisplayName:  "Filesystem",
		Command:      []string{bin},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Display:  "Allowed directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
	}}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()

	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    pool,
		Secrets: backend,
		DataDir: dataDir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg := map[string]any{"allowed_directories": []any{allowed}}
	if _, err := api.InstallRecipe(ctx, "filesystem", map[string]string{}, cfg); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	rs, err := api.RecipeStatus(ctx, "filesystem")
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if rs.State != string(stdio.StateRunning) {
		t.Fatalf("recipe state = %q, want running", rs.State)
	}

	// Persisted Config must carry the allowed_directories the user
	// supplied (canonicalised; symlink resolution may rewrite the
	// /var/folders prefix on macOS so we just assert non-empty).
	persisted, ok := enabled.Get("filesystem")
	if !ok {
		t.Fatal("filesystem not present in EnabledRecipes after install")
	}
	dirs, ok := persisted.Config["allowed_directories"].([]string)
	if !ok || len(dirs) != 1 {
		t.Fatalf("persisted allowed_directories = %v, want one entry", persisted.Config["allowed_directories"])
	}

	// Pool.Tools should surface the fake server's two tools under the
	// recipe-id-namespaced server field.
	toolList, err := pool.Tools(ctx)
	if err != nil {
		t.Fatalf("pool.Tools: %v", err)
	}
	seen := map[string]bool{}
	for _, tl := range toolList {
		if tl.Server == "filesystem" {
			seen[tl.Name] = true
		}
	}
	if !seen["fake_echo"] || !seen["fake_count"] {
		t.Fatalf("pool.Tools for filesystem = %+v, want fake_echo + fake_count", toolList)
	}

	// Dispatch a tool call through the pool. fake_echo wraps its
	// argument back as a text content block.
	args := json.RawMessage(`{"hello":"world"}`)
	out, err := pool.Call(ctx, "filesystem", "fake_echo", args)
	if err != nil {
		t.Fatalf("pool.Call: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("pool.Call returned empty result")
	}

	// Uninstall: subprocess gone, EnabledRecipes entry dropped.
	if err := api.UninstallRecipe(ctx, "filesystem"); err != nil {
		t.Fatalf("UninstallRecipe: %v", err)
	}
	if got := pool.Server("filesystem"); got != nil {
		t.Fatalf("pool.Server(filesystem) = %v after Uninstall, want nil", got)
	}
	if _, present := enabled.Get("filesystem"); present {
		t.Fatalf("EnabledRecipes still has filesystem entry after uninstall")
	}

	listed, err := api.ListRecipes(ctx)
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	for _, r := range listed {
		if r.Recipe.ID == "filesystem" && r.Enabled {
			t.Fatalf("ListRecipes still reports filesystem enabled after uninstall: %+v", r)
		}
	}
}

// TestFilesystemRecipe_E2E_DenyListBlocksInstall asserts the
// path-validation deny-list short-circuits the install before any
// subprocess is spawned. A6 covers this in the manual checklist; the
// e2e harness pins it down so a future deny-list refactor can't
// accidentally let "/etc" through.
func TestFilesystemRecipe_E2E_DenyListBlocksInstall(t *testing.T) {
	bin := buildFakeServerForFilesystemE2E(t)
	dataDir := t.TempDir()

	pool := stdio.NewPool(stdio.PoolOptions{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:           "filesystem",
		Command:      []string{bin},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
	}}}
	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := map[string]any{"allowed_directories": []any{"/etc"}}
	_, err := api.InstallRecipe(ctx, "filesystem", map[string]string{}, cfg)
	if err == nil {
		t.Fatal("InstallRecipe with /etc: want error, got nil")
	}
	if pool.Server("filesystem") != nil {
		t.Fatalf("pool.Server(filesystem) is non-nil after rejected install")
	}
}
