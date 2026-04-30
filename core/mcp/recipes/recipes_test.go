package recipes_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
)

func TestShippedSingletonParses(t *testing.T) {
	cat := recipes.Shipped()
	if cat == nil {
		t.Fatal("Shipped() returned nil")
	}
	if got := len(cat.List()); got != 3 {
		t.Fatalf("want 3 recipes, got %d", got)
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

func TestBraveSearchRemoved(t *testing.T) {
	// Local-only stance (charter): the Brave Search recipe required an
	// external API key and is no longer shipped. Web search ships as a
	// built-in keyless tool surfaced via core/tools/websearch (toggle
	// in KaneazToolsPanel under "Local tools").
	cat := recipes.Shipped()
	if _, ok := cat.Get("brave-search"); ok {
		t.Fatal("brave-search MUST NOT be in shipped catalog (local-only stance)")
	}
}

func TestFilesystemEntry(t *testing.T) {
	cat := recipes.Shipped()
	r, ok := cat.Get("filesystem")
	if !ok {
		t.Fatal("filesystem not in shipped catalog")
	}
	if r.DisplayName != "Filesystem (sandboxed)" {
		t.Errorf("DisplayName = %q", r.DisplayName)
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q", r.Category)
	}
	wantCmd := []string{"npx", "-y", "@modelcontextprotocol/server-filesystem"}
	if len(r.Command) != len(wantCmd) {
		t.Fatalf("Command length = %d, want %d", len(r.Command), len(wantCmd))
	}
	for i := range wantCmd {
		if r.Command[i] != wantCmd[i] {
			t.Errorf("Command[%d] = %q, want %q", i, r.Command[i], wantCmd[i])
		}
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("EnvKeys len = %d, want 0", len(r.EnvKeys))
	}
	if len(r.ArgsTemplate) != 1 || r.ArgsTemplate[0] != "${ALLOWED_DIRS}" {
		t.Errorf("ArgsTemplate = %v, want [${ALLOWED_DIRS}]", r.ArgsTemplate)
	}
	if len(r.ConfigOptions) != 1 {
		t.Fatalf("ConfigOptions len = %d, want 1", len(r.ConfigOptions))
	}
	opt := r.ConfigOptions[0]
	if opt.Name != "allowed_directories" {
		t.Errorf("ConfigOptions[0].Name = %q", opt.Name)
	}
	if opt.Kind != recipes.ConfigKindDirectoryList {
		t.Errorf("ConfigOptions[0].Kind = %q, want %q", opt.Kind, recipes.ConfigKindDirectoryList)
	}
	if !opt.Required {
		t.Error("ConfigOptions[0].Required = false, want true")
	}
	if !r.Capabilities.Tools {
		t.Error("Capabilities.Tools = false, want true")
	}
}

func TestCatalogGetMiss(t *testing.T) {
	cat := recipes.Shipped()
	if _, ok := cat.Get("nonexistent-recipe"); ok {
		t.Fatal("Get returned ok for nonexistent id")
	}
}

func TestFilesystemProjectEntry(t *testing.T) {
	cat := recipes.Shipped()
	r, ok := cat.Get("filesystem-project")
	if !ok {
		t.Fatal("filesystem-project not in shipped catalog")
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
	}
	if len(r.ConfigOptions) != 1 {
		t.Fatalf("ConfigOptions len = %d, want 1", len(r.ConfigOptions))
	}
	opt := r.ConfigOptions[0]
	if !opt.Required {
		t.Error("project root should be required")
	}
	// Default is empty so user must pick a project.
	if defaults, ok := opt.Default.([]any); !ok || len(defaults) != 0 {
		t.Errorf("default = %#v, want empty slice", opt.Default)
	}
	if r.Warning != "" {
		t.Errorf("project recipe should not carry a warning, got %q", r.Warning)
	}
}

func TestFilesystemFullEntry(t *testing.T) {
	cat := recipes.Shipped()
	r, ok := cat.Get("filesystem-full")
	if !ok {
		t.Fatal("filesystem-full not in shipped catalog")
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
	}
	// Hardcoded args, no user config knobs.
	if len(r.ArgsTemplate) != 1 || r.ArgsTemplate[0] != "/" {
		t.Errorf("ArgsTemplate = %v, want [/]", r.ArgsTemplate)
	}
	if len(r.ConfigOptions) != 0 {
		t.Errorf("ConfigOptions = %v, want none (path is hardcoded to /)", r.ConfigOptions)
	}
	if r.Warning == "" {
		t.Error("filesystem-full MUST carry a Warning string")
	}
	if r.RecommendedPolicyTemplate != "filesystem-full-recommended.cedar" {
		t.Errorf("RecommendedPolicyTemplate = %q, want filesystem-full-recommended.cedar", r.RecommendedPolicyTemplate)
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
	spec := r.ToServerSpec(env, nil)

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
	spec := r.ToServerSpec(nil, nil)
	if spec.Env != nil {
		t.Errorf("Env = %v, want nil for empty env", spec.Env)
	}
}

func TestRecipeNewFieldsZeroWhenJSONOmits(t *testing.T) {
	// A recipe parsed from JSON without args_template / config_options
	// should land with nil slices — not surprise allocations. Inline a
	// minimal recipe blob to exercise this without depending on a
	// specific shipped recipe's shape.
	const blob = `{
		"id": "minimal",
		"display_name": "Minimal",
		"description": "",
		"category": "other",
		"command": ["npx", "-y", "stub"],
		"env_keys": [],
		"capabilities": {"tools": true, "resources": false, "prompts": false, "sampling": false},
		"docs_url": "",
		"init_timeout_ms": 5000,
		"ping_period_ms": 30000,
		"sampling_policy": {"allowed": false, "default": false}
	}`
	var r recipes.Recipe
	if err := json.Unmarshal([]byte(blob), &r); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.ArgsTemplate != nil {
		t.Errorf("ArgsTemplate = %v, want nil", r.ArgsTemplate)
	}
	if r.ConfigOptions != nil {
		t.Errorf("ConfigOptions = %v, want nil", r.ConfigOptions)
	}
	if r.Warning != "" {
		t.Errorf("Warning = %q, want empty", r.Warning)
	}
	if r.RecommendedPolicyTemplate != "" {
		t.Errorf("RecommendedPolicyTemplate = %q, want empty", r.RecommendedPolicyTemplate)
	}
}

func TestRecipeNewFieldsParseFromJSON(t *testing.T) {
	// Parse an inline JSON blob that exercises both new fields so a
	// regression in tag names or struct shape fails here, not at
	// shipped.json bump time.
	const blob = `{
		"id": "fs",
		"display_name": "Filesystem",
		"description": "",
		"category": "filesystem",
		"command": ["npx", "-y", "server-fs"],
		"args_template": ["${ALLOWED_DIRS}"],
		"config_options": [
			{"name":"allowed_directories","display":"Roots","kind":"directory_list","required":true,"description":"d"},
			{"name":"read_only","display":"Read-only","kind":"boolean","default":false,"required":false,"description":"d2"}
		],
		"env_keys": [],
		"capabilities": {"tools":true,"resources":false,"prompts":false,"sampling":false},
		"docs_url": "",
		"init_timeout_ms": 5000,
		"ping_period_ms": 30000,
		"sampling_policy": {"allowed":false,"default":false}
	}`
	var r recipes.Recipe
	if err := json.Unmarshal([]byte(blob), &r); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(r.ArgsTemplate) != 1 || r.ArgsTemplate[0] != "${ALLOWED_DIRS}" {
		t.Errorf("ArgsTemplate = %v", r.ArgsTemplate)
	}
	if len(r.ConfigOptions) != 2 {
		t.Fatalf("ConfigOptions len = %d, want 2", len(r.ConfigOptions))
	}
	if r.ConfigOptions[0].Kind != recipes.ConfigKindDirectoryList {
		t.Errorf("ConfigOptions[0].Kind = %q", r.ConfigOptions[0].Kind)
	}
	if !r.ConfigOptions[0].Required {
		t.Error("ConfigOptions[0].Required = false, want true")
	}
	if r.ConfigOptions[1].Kind != recipes.ConfigKindBoolean {
		t.Errorf("ConfigOptions[1].Kind = %q", r.ConfigOptions[1].Kind)
	}
}

func TestToServerSpec_NilConfigBackwardCompat(t *testing.T) {
	// A recipe with no ArgsTemplate and a nil config must produce the
	// exact same spec it did pre-WP01: Command verbatim, no extras.
	r := recipes.Recipe{
		ID:      "brave-search",
		Command: []string{"npx", "-y", "@modelcontextprotocol/server-brave-search"},
	}
	spec := r.ToServerSpec(map[string]string{"BRAVE_API_KEY": "x"}, nil)
	if len(spec.Command) != 3 {
		t.Fatalf("Command len = %d, want 3 (no template expansion expected)", len(spec.Command))
	}
}

func TestToServerSpec_ConfigSubstitutesArgsTemplate(t *testing.T) {
	r := recipes.Recipe{
		ID:           "fs",
		Command:      []string{"npx", "-y", "@modelcontextprotocol/server-filesystem"},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
	}
	cfg := map[string]any{
		"allowed_directories": []any{"/tmp/one", "/tmp/two", "/tmp/three"},
	}
	spec := r.ToServerSpec(nil, cfg)
	// Expect Command + 3 expanded args.
	if len(spec.Command) != 3+3 {
		t.Fatalf("Command = %v (len=%d), want 6", spec.Command, len(spec.Command))
	}
	if spec.Command[3] != "/tmp/one" || spec.Command[4] != "/tmp/two" || spec.Command[5] != "/tmp/three" {
		t.Errorf("expanded args = %v", spec.Command[3:])
	}
}

func TestToServerSpec_ConfigEmptyListAppendsNothing(t *testing.T) {
	r := recipes.Recipe{
		ID:           "fs",
		Command:      []string{"npx"},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
	}
	spec := r.ToServerSpec(nil, map[string]any{"allowed_directories": []any{}})
	if len(spec.Command) != 1 {
		t.Fatalf("Command = %v, want [npx]", spec.Command)
	}
}

func TestToServerSpec_ConfigDataDirScalar(t *testing.T) {
	r := recipes.Recipe{
		ID:           "fs",
		Command:      []string{"npx"},
		ArgsTemplate: []string{"--root=${DATA_DIR}/workspace"},
	}
	spec := r.ToServerSpec(nil, map[string]any{"data_dir": "/var/harness"})
	want := "--root=/var/harness/workspace"
	if len(spec.Command) != 2 || spec.Command[1] != want {
		t.Errorf("Command = %v, want [npx %q]", spec.Command, want)
	}
}

// TestPromptOnFirstUseRoundTrip verifies that the PromptOnFirstUse slice
// survives JSON marshal → unmarshal exactly (WP06 requirement).
func TestPromptOnFirstUseRoundTrip(t *testing.T) {
	// When the JSON blob includes prompt_on_first_use, it must decode.
	const blobWith = `{
		"id": "fs",
		"display_name": "FS",
		"description": "",
		"category": "filesystem",
		"command": ["npx", "-y", "server-fs"],
		"env_keys": [],
		"capabilities": {"tools": true, "resources": false, "prompts": false, "sampling": false},
		"docs_url": "",
		"init_timeout_ms": 5000,
		"ping_period_ms": 30000,
		"sampling_policy": {"allowed": false, "default": false},
		"prompt_on_first_use": ["write_file", "delete_file"]
	}`
	var r recipes.Recipe
	if err := json.Unmarshal([]byte(blobWith), &r); err != nil {
		t.Fatalf("Unmarshal with prompt_on_first_use: %v", err)
	}
	if len(r.PromptOnFirstUse) != 2 {
		t.Fatalf("PromptOnFirstUse len = %d, want 2", len(r.PromptOnFirstUse))
	}
	if r.PromptOnFirstUse[0] != "write_file" || r.PromptOnFirstUse[1] != "delete_file" {
		t.Errorf("PromptOnFirstUse = %v, want [write_file delete_file]", r.PromptOnFirstUse)
	}

	// Re-marshal and confirm the key appears.
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var r2 recipes.Recipe
	if err := json.Unmarshal(out, &r2); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if len(r2.PromptOnFirstUse) != 2 {
		t.Fatalf("round-trip PromptOnFirstUse len = %d, want 2", len(r2.PromptOnFirstUse))
	}

	// When the JSON blob omits prompt_on_first_use, the field is nil (not []).
	const blobWithout = `{
		"id": "fs",
		"display_name": "FS",
		"description": "",
		"category": "filesystem",
		"command": ["npx", "-y", "server-fs"],
		"env_keys": [],
		"capabilities": {"tools": false, "resources": false, "prompts": false, "sampling": false},
		"docs_url": "",
		"init_timeout_ms": 5000,
		"ping_period_ms": 30000,
		"sampling_policy": {"allowed": false, "default": false}
	}`
	var r3 recipes.Recipe
	if err := json.Unmarshal([]byte(blobWithout), &r3); err != nil {
		t.Fatalf("Unmarshal without prompt_on_first_use: %v", err)
	}
	if r3.PromptOnFirstUse != nil {
		t.Errorf("PromptOnFirstUse = %v, want nil when JSON omits field", r3.PromptOnFirstUse)
	}

	// A recipe with no PromptOnFirstUse must NOT emit the key in JSON
	// (omitempty). Confirmed by checking the marshalled form contains no
	// "prompt_on_first_use" substring.
	outWithout, err := json.Marshal(r3)
	if err != nil {
		t.Fatalf("Marshal without: %v", err)
	}
	if bytes := string(outWithout); containsSubstring(bytes, "prompt_on_first_use") {
		t.Errorf("empty PromptOnFirstUse should be omitted from JSON, got: %s", bytes)
	}
}

// containsSubstring is a pure helper that avoids importing strings in
// this test file.
func containsSubstring(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
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
