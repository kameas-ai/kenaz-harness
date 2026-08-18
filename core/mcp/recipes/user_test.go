package recipes_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// validUserYAML is one well-formed user-recipe used across tests.
// The shape mirrors shipped.json's recipe schema but in YAML.
const validUserYAML = `
id: my-tool
display_name: My Tool
description: A user-installed MCP server
category: other
command:
  - npx
  - -y
  - my-tool-server
env_keys: []
capabilities:
  tools: true
  resources: false
  prompts: false
  sampling: false
docs_url: ""
init_timeout_ms: 5000
ping_period_ms: 30000
sampling_policy:
  allowed: false
  default: false
`

func writeUserRecipe(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestUserStoreLoadMissingDirReturnsEmpty(t *testing.T) {
	// Boot ergonomics: a fresh install has no <DataDir>/mcp/recipes/
	// — Load must NOT error.
	dir := t.TempDir()
	store := recipes.NewUserStore(dir, nil)

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
	if store.Recipes() == nil {
		t.Error("Recipes() returned nil; expected []Recipe{}")
	}
}

func TestUserStoreLoadEmptyDataDirErrors(t *testing.T) {
	store := recipes.NewUserStore("", nil)
	if _, err := store.Load(); err == nil {
		t.Fatal("Load with empty DataDir = nil, want error")
	}
}

func TestUserStoreLoadSingleYAML(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "my-tool.yaml", validUserYAML)

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID != "my-tool" {
		t.Errorf("ID = %q, want my-tool", got[0].ID)
	}
	if got[0].Source != recipes.SourceUser {
		t.Errorf("Source = %q, want %q", got[0].Source, recipes.SourceUser)
	}
	if got[0].DisplayName != "My Tool" {
		t.Errorf("DisplayName = %q", got[0].DisplayName)
	}
	if len(got[0].Command) != 3 || got[0].Command[0] != "npx" {
		t.Errorf("Command = %v", got[0].Command)
	}
}

func TestUserStoreLoadAlsoYmlExtension(t *testing.T) {
	// Both .yaml and .yml are accepted.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "yaml-one.yaml", validUserYAML)
	// Different id so both load (collision logic isn't under test here).
	body2 := `
id: yml-two
display_name: Yml Two
description: ""
category: other
command:
  - npx
env_keys: []
capabilities: {tools: true, resources: false, prompts: false, sampling: false}
docs_url: ""
init_timeout_ms: 0
ping_period_ms: 0
sampling_policy: {allowed: false, default: false}
`
	writeUserRecipe(t, root, "yml-two.yml", body2)

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 recipes (yaml + yml), got %d", len(got))
	}
}

func TestUserStoreSkipsMalformedYAML(t *testing.T) {
	// One bad file must NOT take down the rest of the load.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "good.yaml", validUserYAML)
	writeUserRecipe(t, root, "bad.yaml", "::: not valid yaml :::")

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v (must not propagate per-file errors)", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (the good file)", len(got))
	}
	if got[0].ID != "my-tool" {
		t.Errorf("survivor ID = %q, want my-tool", got[0].ID)
	}
}

func TestUserStoreSkipsInvalidRecipe(t *testing.T) {
	// A recipe that parses but fails Validate (bad ID) is skipped.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "good.yaml", validUserYAML)
	bad := `
id: BAD-CASE
display_name: x
description: ""
category: other
command: [x]
env_keys: []
capabilities: {tools: false, resources: false, prompts: false, sampling: false}
docs_url: ""
init_timeout_ms: 0
ping_period_ms: 0
sampling_policy: {allowed: false, default: false}
`
	writeUserRecipe(t, root, "bad-id.yaml", bad)

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1; bad-id should be skipped", len(got))
	}
}

func TestUserStoreImportsTaggedAsImported(t *testing.T) {
	// Files under _imports/ must be tagged Source="imported".
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	imports := filepath.Join(root, "_imports")
	writeUserRecipe(t, root, "user.yaml", validUserYAML)

	importedBody := `
id: imported-tool
display_name: Imported
description: ""
category: other
command:
  - mcp-server-imported
env_keys: []
capabilities: {tools: true, resources: false, prompts: false, sampling: false}
docs_url: ""
init_timeout_ms: 0
ping_period_ms: 0
sampling_policy: {allowed: false, default: false}
`
	writeUserRecipe(t, imports, "imported-tool.yaml", importedBody)

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	gotByID := map[string]recipes.Recipe{}
	for _, r := range got {
		gotByID[r.ID] = r
	}
	if u, ok := gotByID["my-tool"]; !ok {
		t.Fatal("my-tool missing")
	} else if u.Source != recipes.SourceUser {
		t.Errorf("my-tool Source = %q, want %q", u.Source, recipes.SourceUser)
	}
	if im, ok := gotByID["imported-tool"]; !ok {
		t.Fatal("imported-tool missing")
	} else if im.Source != recipes.SourceImported {
		t.Errorf("imported-tool Source = %q, want %q", im.Source, recipes.SourceImported)
	}
}

func TestUserStoreCopyOnWriteSnapshot(t *testing.T) {
	// Mutating a returned slice must NOT affect subsequent reads.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "user.yaml", validUserYAML)

	store := recipes.NewUserStore(dir, nil)
	if _, err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	first := store.Recipes()
	if len(first) != 1 {
		t.Fatalf("len(first) = %d, want 1", len(first))
	}
	first[0].ID = "tampered"

	second := store.Recipes()
	if second[0].ID == "tampered" {
		t.Fatal("Recipes() returned aliased slice; mutation leaked into store")
	}
	if second[0].ID != "my-tool" {
		t.Errorf("second[0].ID = %q, want my-tool", second[0].ID)
	}
}

func TestUserStoreReloadAfterFileWrite(t *testing.T) {
	// Reload should pick up a freshly-written file even without a
	// watcher.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "user.yaml", validUserYAML)

	store := recipes.NewUserStore(dir, nil)
	if _, err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(store.Recipes()); got != 1 {
		t.Fatalf("initial len = %d, want 1", got)
	}

	second := `
id: second-tool
display_name: Second
description: ""
category: other
command: [foo]
env_keys: []
capabilities: {tools: true, resources: false, prompts: false, sampling: false}
docs_url: ""
init_timeout_ms: 0
ping_period_ms: 0
sampling_policy: {allowed: false, default: false}
`
	writeUserRecipe(t, root, "second.yaml", second)
	store.Reload()
	if got := len(store.Recipes()); got != 2 {
		t.Errorf("after reload len = %d, want 2", got)
	}
}

func TestUserStoreRecipesBeforeLoad(t *testing.T) {
	// Calling Recipes() before Load must return an empty (non-nil)
	// slice — a brand-new store has no snapshot.
	store := recipes.NewUserStore(t.TempDir(), nil)
	got := store.Recipes()
	if got == nil {
		t.Fatal("Recipes() before Load = nil, want []Recipe{}")
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestUserStoreSkipsNonYAMLFiles(t *testing.T) {
	// A stray .json or .txt file alongside the recipes must be
	// ignored without error.
	dir := t.TempDir()
	root := filepath.Join(dir, "mcp", "recipes")
	writeUserRecipe(t, root, "user.yaml", validUserYAML)
	writeUserRecipe(t, root, "notes.txt", "this is not a recipe")
	writeUserRecipe(t, root, "preserved.json", `{"original":"clipboard paste"}`)

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len(got) = %d, want 1 (non-yaml ignored)", len(got))
	}
}
