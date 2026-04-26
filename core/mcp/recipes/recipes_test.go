package recipes_test

import (
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
)

func TestShippedSingletonParses(t *testing.T) {
	cat := recipes.Shipped()
	if cat == nil {
		t.Fatal("Shipped() returned nil")
	}
	if got := len(cat.List()); got != 1 {
		t.Fatalf("want 1 recipe, got %d", got)
	}
}

func TestLoadShippedReturnsFreshCopy(t *testing.T) {
	a, err := recipes.LoadShipped()
	if err != nil {
		t.Fatalf("LoadShipped: %v", err)
	}
	b, err := recipes.LoadShipped()
	if err != nil {
		t.Fatalf("LoadShipped (second): %v", err)
	}
	if a == b {
		t.Fatal("LoadShipped should return a fresh *Catalog each call, got identical pointer")
	}
	if len(a.Recipes) != len(b.Recipes) {
		t.Fatalf("recipe count drift: %d vs %d", len(a.Recipes), len(b.Recipes))
	}
}

func TestBraveSearchEntry(t *testing.T) {
	cat := recipes.Shipped()
	r, ok := cat.Get("brave-search")
	if !ok {
		t.Fatal("brave-search not in shipped catalog")
	}
	if r.DisplayName != "Brave Search" {
		t.Errorf("DisplayName = %q", r.DisplayName)
	}
	if r.Category != "search" {
		t.Errorf("Category = %q", r.Category)
	}
	wantCmd := []string{"npx", "-y", "@modelcontextprotocol/server-brave-search"}
	if len(r.Command) != len(wantCmd) {
		t.Fatalf("Command length = %d, want %d", len(r.Command), len(wantCmd))
	}
	for i := range wantCmd {
		if r.Command[i] != wantCmd[i] {
			t.Errorf("Command[%d] = %q, want %q", i, r.Command[i], wantCmd[i])
		}
	}
	if len(r.EnvKeys) != 1 {
		t.Fatalf("EnvKeys length = %d, want 1", len(r.EnvKeys))
	}
	if r.EnvKeys[0].Name != "BRAVE_API_KEY" {
		t.Errorf("EnvKeys[0].Name = %q, want BRAVE_API_KEY", r.EnvKeys[0].Name)
	}
	if !r.EnvKeys[0].Required {
		t.Error("EnvKeys[0].Required should be true")
	}
	if !r.Capabilities.Tools {
		t.Error("Capabilities.Tools should be true")
	}
	if r.Capabilities.Resources || r.Capabilities.Prompts || r.Capabilities.Sampling {
		t.Errorf("unexpected capabilities: %+v", r.Capabilities)
	}
	if r.InitTimeoutMs != 5000 {
		t.Errorf("InitTimeoutMs = %d, want 5000", r.InitTimeoutMs)
	}
	if r.PingPeriodMs != 30000 {
		t.Errorf("PingPeriodMs = %d, want 30000", r.PingPeriodMs)
	}
	if r.SamplingPolicy.Allowed || r.SamplingPolicy.Default {
		t.Errorf("sampling policy = %+v, want both false", r.SamplingPolicy)
	}
}

func TestCatalogGetMiss(t *testing.T) {
	cat := recipes.Shipped()
	if _, ok := cat.Get("nonexistent-recipe"); ok {
		t.Fatal("Get returned ok for nonexistent id")
	}
}

func TestCatalogListIsCopy(t *testing.T) {
	cat := recipes.Shipped()
	first := cat.List()
	if len(first) == 0 {
		t.Fatal("empty catalog")
	}
	first[0].ID = "tampered"
	second := cat.List()
	if second[0].ID == "tampered" {
		t.Fatal("List() returned aliased slice; mutation leaked")
	}
}

func TestValidateRecipeID(t *testing.T) {
	good := []string{"a", "ab", "brave-search", "fs", "memory", "z9-z9"}
	for _, id := range good {
		if err := recipes.ValidateRecipeID(id); err != nil {
			t.Errorf("ValidateRecipeID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{
		"",                // empty
		"A",               // uppercase
		"1abc",            // leading digit
		"-abc",            // leading dash
		"foo_bar",         // underscore
		"foo bar",         // space
		"foo/bar",         // slash
		"héllo",           // non-ascii
		repeat("a", 65),   // > 64 chars (1 + 64)
	}
	for _, id := range bad {
		err := recipes.ValidateRecipeID(id)
		if err == nil {
			t.Errorf("ValidateRecipeID(%q) = nil, want error", id)
			continue
		}
		if !errors.Is(err, recipes.ErrInvalidRecipeID) {
			t.Errorf("ValidateRecipeID(%q) error = %v, want ErrInvalidRecipeID", id, err)
		}
	}
	// Exactly 64 chars is allowed (regex is {0,63} after the leading
	// char, so total length 1+63=64).
	max := "a" + repeat("b", 63)
	if err := recipes.ValidateRecipeID(max); err != nil {
		t.Errorf("ValidateRecipeID(64-char) = %v, want nil", err)
	}
	// 65 chars rejected.
	tooLong := "a" + repeat("b", 64)
	if err := recipes.ValidateRecipeID(tooLong); err == nil {
		t.Errorf("ValidateRecipeID(65-char) = nil, want error")
	}
}

func TestRecipeValidate(t *testing.T) {
	good := recipes.Recipe{
		ID:      "fs",
		Command: []string{"npx", "-y", "fs"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good.Validate() = %v", err)
	}

	cases := map[string]recipes.Recipe{
		"empty id":      {ID: "", Command: []string{"x"}},
		"bad id":        {ID: "BAD", Command: []string{"x"}},
		"no command":    {ID: "fs", Command: nil},
		"empty argv0":   {ID: "fs", Command: []string{""}},
		"empty env name": {ID: "fs", Command: []string{"x"}, EnvKeys: []recipes.EnvKey{{Name: "", Required: true}}},
	}
	for name, r := range cases {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want error", name)
		}
	}
}

func TestToServerSpec(t *testing.T) {
	r := recipes.Recipe{
		ID:      "brave-search",
		Command: []string{"npx", "-y", "@modelcontextprotocol/server-brave-search"},
	}
	env := map[string]string{"BRAVE_API_KEY": "sentinel-value"}
	spec := r.ToServerSpec(env)

	if spec.Name != "brave-search" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Transport != "stdio" {
		t.Errorf("Transport = %q", spec.Transport)
	}
	if got := len(spec.Command); got != 3 {
		t.Fatalf("Command len = %d", got)
	}
	if spec.Env["BRAVE_API_KEY"] != "sentinel-value" {
		t.Errorf("Env BRAVE_API_KEY = %q", spec.Env["BRAVE_API_KEY"])
	}

	// Defensive copy: mutating the caller's command shouldn't affect
	// the spec, and vice versa.
	r.Command[0] = "tampered"
	if spec.Command[0] == "tampered" {
		t.Error("ToServerSpec aliased Command slice")
	}
	env["BRAVE_API_KEY"] = "tampered"
	if spec.Env["BRAVE_API_KEY"] == "tampered" {
		t.Error("ToServerSpec aliased Env map")
	}
}

func TestToServerSpecNilEnv(t *testing.T) {
	r := recipes.Recipe{ID: "fs", Command: []string{"npx"}}
	spec := r.ToServerSpec(nil)
	if spec.Env != nil {
		t.Errorf("Env = %v, want nil for empty env", spec.Env)
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	// Defensive: a refactor that collapses the sentinels into one
	// would silently flip behaviour for downstream errors.Is checks.
	if errors.Is(recipes.ErrRecipeNotFound, recipes.ErrInvalidRecipeID) {
		t.Error("ErrRecipeNotFound should not be ErrInvalidRecipeID")
	}
	if errors.Is(recipes.ErrInvalidRecipe, recipes.ErrInvalidRecipeID) {
		t.Error("ErrInvalidRecipe should not be ErrInvalidRecipeID")
	}
}

// repeat is a tiny helper to avoid pulling strings.Repeat just for
// test data in two places.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
