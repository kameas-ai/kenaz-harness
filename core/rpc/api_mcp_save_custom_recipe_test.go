package rpc

// api_mcp_save_custom_recipe_test.go — mcp-connector-lifecycle-01PMMC01
// WP06 (FR-006).
//
// CustomRecipeTab.vue's save() used to unconditionally throw — there was
// no backend to call. These tests drive the REAL chassis (rpc.New over a
// real Core + a real on-disk DataDir — blind spot 1 from CLAUDE.md: a
// fixture that saves through UserStore directly and reads back through
// UserStore.Load would prove nothing about the RPC-to-Tools-list wiring
// under test) and assert the saved recipe is readable through the SAME
// catalog the Tools list reads (WP03's wiring), and that it actually
// spawns.
import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
	toolsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
)

func recipeIDs(listed []toolsview.RecipeListing) []string {
	out := make([]string, 0, len(listed))
	for _, l := range listed {
		out = append(out, l.Recipe.ID)
	}
	return out
}

// TestSaveCustomRecipeRoundTrip is AC-007's first clause: a recipe
// authored in the Custom tab is persisted by UserStore.Save and appears
// in Tools_ListRecipes in the same process (i.e. through WP03's
// mcpLiveCatalog wiring, not read back via UserStore directly — that
// would be the fixture bypassing the layer under test).
//
// Mutation: make MCP_SaveCustomRecipe (or SaveCustomRecipe) write to a
// different directory than the one UserStore.Load reads (e.g. hardcode
// a second UserStore rooted at a throwaway t.TempDir() instead of
// a.mcpUserStore). This assertion must fail — a test that saved and then
// read via UserStore.Load directly (bypassing api.Tools()) would still
// pass under that mutation, which is exactly the bypass CLAUDE.md's
// blind-spot-1 warns about.
func TestSaveCustomRecipeRoundTrip(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	ctx := context.Background()

	saved, err := api.MCP().SaveCustomRecipe(ctx, mcpview.SaveCustomRecipeRequest{
		ID:          "wp06-custom-roundtrip",
		DisplayName: "WP06 Custom Roundtrip",
		Description: "test",
		Transport:   "stdio",
		Command:     []string{"some-binary", "--flag"},
	})
	if err != nil {
		t.Fatalf("SaveCustomRecipe: %v", err)
	}
	if saved.ID != "wp06-custom-roundtrip" {
		t.Fatalf("saved.ID = %q, want wp06-custom-roundtrip", saved.ID)
	}
	if saved.Source != recipes.SourceUser {
		t.Errorf("saved.Source = %q, want %q", saved.Source, recipes.SourceUser)
	}

	listed, err := api.Tools().ListRecipes(ctx)
	if err != nil {
		t.Fatalf("Tools().ListRecipes: %v", err)
	}
	var found bool
	for _, l := range listed {
		if l.Recipe.ID == "wp06-custom-roundtrip" {
			found = true
			if l.Recipe.DisplayName != "WP06 Custom Roundtrip" {
				t.Errorf("listed DisplayName = %q, want %q", l.Recipe.DisplayName, "WP06 Custom Roundtrip")
			}
		}
	}
	if !found {
		t.Fatalf("Tools_ListRecipes (same process, no restart) missing saved custom recipe; got ids: %v", recipeIDs(listed))
	}
}

// TestSaveCustomRecipeValidatesBeforePersisting asserts the same
// validation every shipped recipe goes through (recipes.Recipe.Validate)
// rejects a stdio recipe with no command BEFORE anything is written to
// disk — the save() form's client-side validation is not the only line
// of defense.
func TestSaveCustomRecipeValidatesBeforePersisting(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	ctx := context.Background()

	_, err = api.MCP().SaveCustomRecipe(ctx, mcpview.SaveCustomRecipeRequest{
		ID:          "wp06-invalid",
		DisplayName: "Invalid",
		Transport:   "stdio",
		// Command deliberately empty.
	})
	if err == nil {
		t.Fatal("SaveCustomRecipe with no command = nil error, want a validation error")
	}

	importsDir := filepath.Join(dataDir, "mcp", "recipes")
	entries, _ := os.ReadDir(importsDir)
	for _, e := range entries {
		if e.Name() == "wp06-invalid.yaml" {
			t.Fatalf("wp06-invalid.yaml was written to disk despite a failed validation")
		}
	}
}

// TestSavedCustomRecipeSpawns is AC-003/AC-007's "actually spawns"
// clause: saving a recipe is not sufficient evidence it works — install
// it and assert the pool reaches a live state and exposes at least one
// tool. Uses the in-tree fake MCP server (core/mcp/transport/stdio/
// testdata/fake-mcp-server), the same binary core/mcp/transport/stdio's
// own non-integration-tagged tests build directly (helper_test.go), so
// this runs in the default `go test ./core/...` sweep rather than being
// gated behind an opt-in build tag.
func TestSavedCustomRecipeSpawns(t *testing.T) {
	bin := buildFakeServerForSaveCustomRecipeTest(t)
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	ctx := context.Background()

	const id = "wp06-spawns"
	if _, err := api.MCP().SaveCustomRecipe(ctx, mcpview.SaveCustomRecipeRequest{
		ID:          id,
		DisplayName: "WP06 Spawns",
		Transport:   "stdio",
		Command:     []string{bin},
	}); err != nil {
		t.Fatalf("SaveCustomRecipe: %v", err)
	}

	installCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	status, err := api.Tools().InstallRecipe(installCtx, id, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("InstallRecipe(%q): %v", id, err)
	}
	if status.State != "running" {
		t.Fatalf("post-install state = %q, want running", status.State)
	}

	rs, err := api.Tools().RecipeStatus(ctx, id)
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if rs.State != "running" {
		t.Fatalf("RecipeStatus state = %q, want running", rs.State)
	}
	if rs.ToolCount == 0 {
		t.Errorf("ToolCount = 0, want at least one namespaced tool from the fake server")
	}

	if err := api.Tools().UninstallRecipe(ctx, id); err != nil {
		t.Fatalf("UninstallRecipe: %v", err)
	}
}

// buildFakeServerForSaveCustomRecipeTest compiles the in-tree fake MCP
// server and returns the absolute path to the binary. Separate from
// api_mcp_e2e_test.go's buildFakeServerForE2E (that file carries the
// `integration` build tag, so its helper is unavailable here).
func buildFakeServerForSaveCustomRecipeTest(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	srcDir := filepath.Join(filepath.Dir(here), "..", "mcp", "transport", "stdio", "testdata", "fake-mcp-server")
	tmpDir := t.TempDir()
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

// TestSaveCustomRecipeWithoutStoreReturnsSentinel pins the sentinel
// SaveCustomRecipe's docstring promises for the no-DataDir boot (the
// rpc.New(nil) test-harness path). It is a typed-nil regression guard:
// wiring the option as
//
//	mcp.NewAPI(..., mcp.WithRecipeSaver(a.mcpUserStore))
//
// unconditionally hands NewAPI a non-nil RecipeSaver interface value
// holding a nil *recipes.UserStore whenever there is no DataDir, so the
// `saver == nil` guard never fires and ErrRecipeSaverNotConfigured
// becomes unreachable — a declared-but-never-returned error, the exact
// unwired class mcp-connector-lifecycle-01PMMC01 exists to end.
//
// Mutation: drop the `if a.mcpUserStore != nil` guard around the
// WithRecipeSaver append in New(). errors.Is must stop matching and this
// test must fail.
func TestSaveCustomRecipeWithoutStoreReturnsSentinel(t *testing.T) {
	api := New(nil)
	_, err := api.MCP().SaveCustomRecipe(context.Background(), mcpview.SaveCustomRecipeRequest{
		ID:          "wp06-no-store",
		DisplayName: "No Store",
		Transport:   "stdio",
		Command:     []string{"some-binary"},
	})
	if err == nil {
		t.Fatal("SaveCustomRecipe with no user store = nil error, want ErrRecipeSaverNotConfigured")
	}
	if !errors.Is(err, mcpview.ErrRecipeSaverNotConfigured) {
		t.Errorf("err = %v, want it to match ErrRecipeSaverNotConfigured", err)
	}
}
