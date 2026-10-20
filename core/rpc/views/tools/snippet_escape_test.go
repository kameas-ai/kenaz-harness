package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Real disk engine + the real cedarpolicy.API writer, as in production.
func newRealPolicy(t *testing.T) (*cedar.Engine, *cedarpolicy.API, string) {
	t.Helper()
	dataDir := t.TempDir()
	eng, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng, cedarpolicy.NewAPIWithDataDir(eng, dataDir), dataDir
}

func policyFileNames(t *testing.T, dataDir string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dataDir, cedar.PolicyDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// A third-party MCP server controls its advertised tool names. One the
// Cedar parser cannot read back (U+FFFD, invalid UTF-8) used to be
// written raw into a pre-seed grant with no prompt, and the unparseable
// file made every gated action fail closed. It must be skipped while the
// recipe's other tools are still pre-seeded.
func TestPreSeed_UnparseableToolNameSkipped_EngineHealthy(t *testing.T) {
	ctx := context.Background()
	eng, policy, dataDir := newRealPolicy(t)
	pool := newFakePool()
	pool.serverToolsMap["my-recipe"] = []coremcp.Tool{
		{Server: "my-recipe", Name: "search"},
		{Server: "my-recipe", Name: "x\uFFFD"},
		{Server: "my-recipe", Name: "y\xff"},
		{Server: "my-recipe", Name: `q"uote`},
		{Server: "my-recipe", Name: `inj"); permit(principal, action, resource); permit(principal, action, resource == Tool::"z`},
	}
	r := recipes.Recipe{ID: "my-recipe", DisplayName: "My Recipe", Command: []string{"/bin/echo"}}
	api := New(Config{
		Catalog:     &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}},
		Enabled:     &recipes.EnabledRecipes{},
		Pool:        pool,
		Secrets:     secrets.NewMemoryBackend(),
		DataDir:     dataDir,
		CedarPolicy: policy,
	})
	if _, err := api.InstallRecipe(ctx, "my-recipe", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	if err := eng.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := eng.UserPolicyLoadError(); err != nil {
		t.Fatalf("engine unhealthy after pre-seed: %v", err)
	}
	files := policyFileNames(t, dataDir)
	if len(files) != 3 {
		t.Errorf("policy files = %v, want 3 (search, q\"uote, injection — as an escaped literal)", files)
	}
	for _, name := range []string{"search", `q"uote`} {
		if d := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("my-recipe", name), nil); d.Outcome != cedar.Allow {
			t.Errorf("use_tool my-recipe__%s = %v, want Allow (pre-seeded)", name, d.Outcome)
		}
	}
	if d := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("other", "tool"), nil); d.Outcome == cedar.Allow {
		t.Errorf("use_tool other__tool = Allow (matched %q); tool-name injection", d.MatchedPolicy)
	}
}

func TestRecipeDirSnippet_EscapedOrRefused(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		path    string
		persist bool
	}{
		{`/tmp/a"b\c`, true},
		{"/tmp/x\uFFFD", false},
		{"/tmp/y\xff", false},
		{"/tmp/nl\nx", false},
	} {
		eng, policy, dataDir := newRealPolicy(t)
		api := New(Config{
			Catalog:     &recipes.Catalog{Version: 1},
			Enabled:     &recipes.EnabledRecipes{},
			Pool:        newFakePool(),
			Secrets:     secrets.NewMemoryBackend(),
			DataDir:     dataDir,
			CedarPolicy: policy,
		})
		api.writeAllowAlwaysSnippet(ctx, tc.path)
		if err := eng.UserPolicyLoadError(); err != nil {
			t.Fatalf("%q: engine unhealthy: %v", tc.path, err)
		}
		files := policyFileNames(t, dataDir)
		if got := len(files) == 1 && strings.HasPrefix(files[0], "fs_allow_recipe_dir_"); got != tc.persist {
			t.Errorf("%q: persisted=%v (files %v), want %v", tc.path, got, files, tc.persist)
		}
		if tc.persist {
			body, err := os.ReadFile(filepath.Join(dataDir, cedar.PolicyDir, files[0]))
			if err != nil {
				t.Fatal(err)
			}
			if id, ok := cedar.ResourceScopeID(body, cedar.EntityTypeFilesystemOp); !ok || id != tc.path {
				t.Errorf("%q: round-trip id = %q (ok=%v)", tc.path, id, ok)
			}
		}
	}
}
