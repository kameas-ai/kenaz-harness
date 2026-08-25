package rpc

import (
	"context"
	"strings"
	"sync"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// ── fakes ────────────────────────────────────────────────────────────────

// registryFakePool is a minimal tools.PoolController: enough for
// InstallRecipe's spawn step to succeed without a real subprocess.
type registryFakePool struct {
	mu       sync.Mutex
	statuses map[string]stdio.RecipeStatus
	opens    int
}

func newRegistryFakePool() *registryFakePool {
	return &registryFakePool{statuses: map[string]stdio.RecipeStatus{}}
}

func (p *registryFakePool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opens++
	p.statuses[spec.Name] = stdio.RecipeStatus{ID: spec.Name, State: "running"}
	return nil
}

func (p *registryFakePool) CloseOne(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.statuses, id)
	return nil
}

func (p *registryFakePool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.statuses[id]
	return s, ok
}

func (p *registryFakePool) ServerTools(_ string) []coremcp.Tool { return nil }

func (p *registryFakePool) openCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opens
}

// registryFakeKeychain accepts every write; the installed-MCP sync path
// never sends secret values (see the redaction tests below), so nothing
// here needs to be inspected — it just has to not fail InstallRecipe.
type registryFakeKeychain struct{}

func (registryFakeKeychain) Write(_ context.Context, _ string, plaintext []byte) error {
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

// acmeRecipe is a synthetic recipe whose ConfigOption name deliberately
// collides with its EnvKey name ("ACME_TOKEN"). recipes.EnvKey has no
// Secret flag (core/mcp/recipes/recipes.go:78 — every EnvKey is
// credential-bearing by declaration), so a per-install config value that
// happens to share a name with a declared EnvKey is exactly the shape
// the redaction guard (core/fleet/sync_mcp.go:137) exists to catch: a
// non-secret config field is not supposed to leak secret-shaped data,
// but if it ever did, this is what it would look like.
func acmeRecipe() recipes.Recipe {
	return recipes.Recipe{
		ID:          "acme-mcp",
		DisplayName: "Acme MCP",
		Command:     []string{"/bin/echo"},
		EnvKeys: []recipes.EnvKey{
			{Name: "ACME_TOKEN", Display: "Acme Token", Required: false},
		},
		ConfigOptions: []recipes.ConfigOption{
			{Name: "ACME_TOKEN", Kind: recipes.ConfigKindString, Required: false},
			{Name: "base_url", Kind: recipes.ConfigKindString, Required: false},
		},
	}
}

// noSecretRecipe declares zero EnvKeys — the class ApplyInstalled treats
// as safe to install immediately (no credential to be missing).
func noSecretRecipe() recipes.Recipe {
	return recipes.Recipe{
		ID:          "public-mcp",
		DisplayName: "Public MCP",
		Command:     []string{"/bin/echo"},
	}
}

// secretRecipe declares a required EnvKey — the class ApplyInstalled must
// never auto-install, because the sync payload never carries secret
// values.
func secretRecipe() recipes.Recipe {
	return recipes.Recipe{
		ID:          "secret-mcp",
		DisplayName: "Secret MCP",
		Command:     []string{"/bin/echo"},
		EnvKeys: []recipes.EnvKey{
			{Name: "SECRET_MCP_TOKEN", Display: "Token", Required: true},
		},
	}
}

func newTestToolsAPI(t *testing.T, cat *recipes.Catalog, pool *registryFakePool) tools.ToolsAPI {
	t.Helper()
	return tools.New(tools.Config{
		Catalog:  cat,
		Enabled:  &recipes.EnabledRecipes{},
		Pool:     pool,
		Secrets:  secrets.NewMemoryBackend(),
		Keychain: registryFakeKeychain{},
		DataDir:  t.TempDir(),
	})
}

// ── AC-010: the real Reader redacts, and only redacts ──────────────────

// TestToolsMCPRegistry_ListInstalled_RedactsSecretKeyedOverride is AC-010.
// A real reader (toolsMCPRegistry over a live tools.API) enumerates an
// installed recipe whose per-install config carries a value keyed by the
// recipe's own EnvKey name. With a non-nil SecretKeys set covering that
// name, MCPSyncCategory.Collect's marshalled output must not contain the
// secret value's bytes anywhere — asserted on the raw JSON, not a struct
// field, per the spec's explicit instruction (a struct-field assertion
// would miss the value leaking through a different field).
func TestToolsMCPRegistry_ListInstalled_RedactsSecretKeyedOverride(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{acmeRecipe()}}
	pool := newRegistryFakePool()
	toolsAPI := newTestToolsAPI(t, cat, pool)

	const secretValue = "sk-acme-should-never-leave-the-device"
	const nonSecretValue = "https://acme.example.com/mcp"
	_, err := toolsAPI.InstallRecipe(context.Background(), "acme-mcp", nil, map[string]any{
		"ACME_TOKEN": secretValue,
		"base_url":   nonSecretValue,
	})
	if err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	registry := newToolsMCPRegistry(toolsAPI)
	secretKeys := mcpRecipeSecretKeys(func() []recipes.Recipe { return cat.Recipes })
	if !secretKeys["ACME_TOKEN"] {
		t.Fatalf("mcpRecipeSecretKeys() = %v, want ACME_TOKEN present (recipe declares it as an EnvKey)", secretKeys)
	}

	cattest := corefleet.NewMCPSyncCategory(registry, registry, func() map[string]bool { return secretKeys }, nil)
	raw, err := cattest.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("Collect payload leaked the secret-keyed value; raw = %s", raw)
	}
	if !strings.Contains(string(raw), nonSecretValue) {
		t.Fatalf("Collect payload dropped the non-secret value too — redaction should discriminate, not blank everything; raw = %s", raw)
	}
}

// TestToolsMCPRegistry_ListInstalled_UndeterminableSecretSet_ShipsNothing
// pins the fail-closed half of AC-010. A nil SecretKeys provider (or one
// returning a nil map) means "the secret set could not be determined" —
// mergedRecipeCatalog returned nil, say. The old code treated that as
// "no key is secret" and shipped every override in plaintext; it now
// drops every override instead.
//
// Flip the `if !known` arm in MCPSyncCategory.Collect back to the old
// `len(m.SecretKeys) > 0` guard and this goes red with the secret in raw.
func TestToolsMCPRegistry_ListInstalled_UndeterminableSecretSet_ShipsNothing(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{acmeRecipe()}}
	pool := newRegistryFakePool()
	toolsAPI := newTestToolsAPI(t, cat, pool)

	const secretValue = "sk-acme-should-never-leave-the-device"
	_, err := toolsAPI.InstallRecipe(context.Background(), "acme-mcp", nil, map[string]any{
		"ACME_TOKEN": secretValue,
	})
	if err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	registry := newToolsMCPRegistry(toolsAPI)
	for _, tc := range []struct {
		name     string
		provider func() map[string]bool
	}{
		{"nil provider", nil},
		{"provider returns nil", func() map[string]bool { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cattest := corefleet.NewMCPSyncCategory(registry, registry, tc.provider, nil)
			raw, err := cattest.Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if strings.Contains(string(raw), secretValue) {
				t.Fatalf("an undeterminable secret set must drop EVERY env override rather than "+
					"guessing that none are secret; the token shipped instead; raw = %s", raw)
			}
		})
	}
}

// TestToolsMCPRegistry_ListInstalled_RedactsRecipeAddedAfterConstruction
// pins that the secret set is resolved per Collect rather than snapshotted
// at construction. WP07 originally passed mcpRecipeSecretKeys(...) — an
// already-evaluated map — and a.mcpUserStore accepts recipe imports at
// runtime, so a recipe imported after boot was absent from the redaction
// set and its token shipped in plaintext on the ordinary "import an MCP
// recipe, paste the token, sync" path.
//
// The category is constructed while the catalog does NOT yet contain the
// recipe; the recipe (and therefore its ACME_TOKEN env key) only appears
// afterwards. A provider evaluated at construction cannot know about it.
func TestToolsMCPRegistry_ListInstalled_RedactsRecipeAddedAfterConstruction(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{}}
	pool := newRegistryFakePool()
	toolsAPI := newTestToolsAPI(t, cat, pool)
	registry := newToolsMCPRegistry(toolsAPI)

	cattest := corefleet.NewMCPSyncCategory(registry, registry, func() map[string]bool {
		out := map[string]bool{}
		for _, rec := range cat.Recipes {
			for _, k := range rec.EnvKeys {
				if k.Name != "" {
					out[k.Name] = true
				}
			}
		}
		return out
	}, nil)

	// The recipe arrives AFTER the category exists — the post-boot import.
	cat.Recipes = append(cat.Recipes, acmeRecipe())

	const secretValue = "sk-imported-after-the-category-was-built"
	if _, err := toolsAPI.InstallRecipe(context.Background(), "acme-mcp", nil, map[string]any{
		"ACME_TOKEN": secretValue,
	}); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	raw, err := cattest.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("a recipe imported after the sync category was constructed must still be "+
			"redacted — a boot-time snapshot of the secret set ships its token; raw = %s", raw)
	}
}

// ── ApplyInstalled: secrets never auto-install, non-secrets do ─────────

func TestToolsMCPRegistry_ApplyInstalled_SkipsItemsRequiringSecrets(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{secretRecipe()}}
	pool := newRegistryFakePool()
	toolsAPI := newTestToolsAPI(t, cat, pool)
	registry := newToolsMCPRegistry(toolsAPI)

	err := registry.ApplyInstalled([]corefleet.InstalledMCP{
		{ID: "secret-mcp", RecipeID: "secret-mcp", RequiresSecretKeys: []string{"SECRET_MCP_TOKEN"}},
	})
	if err != nil {
		t.Fatalf("ApplyInstalled: %v", err)
	}
	if pool.openCount() != 0 {
		t.Fatalf("pool.opens = %d, want 0 — an item requiring secrets must never be auto-installed "+
			"(the payload never carries the secret value)", pool.openCount())
	}
	listings, err := toolsAPI.ListRecipes(context.Background())
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	for _, l := range listings {
		if l.Recipe.ID == "secret-mcp" && l.Enabled {
			t.Fatalf("secret-mcp was marked enabled despite requiring secrets this device does not have")
		}
	}
}

func TestToolsMCPRegistry_ApplyInstalled_InstallsSecretFreeItemsIdempotently(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{noSecretRecipe()}}
	pool := newRegistryFakePool()
	toolsAPI := newTestToolsAPI(t, cat, pool)
	registry := newToolsMCPRegistry(toolsAPI)

	item := corefleet.InstalledMCP{ID: "public-mcp", RecipeID: "public-mcp"}

	if err := registry.ApplyInstalled([]corefleet.InstalledMCP{item}); err != nil {
		t.Fatalf("ApplyInstalled (first): %v", err)
	}
	if pool.openCount() != 1 {
		t.Fatalf("pool.opens after first apply = %d, want 1", pool.openCount())
	}

	// Idempotency by RecipeID: applying the identical payload again must
	// not re-open the pool entry.
	if err := registry.ApplyInstalled([]corefleet.InstalledMCP{item}); err != nil {
		t.Fatalf("ApplyInstalled (second): %v", err)
	}
	if pool.openCount() != 1 {
		t.Fatalf("pool.opens after second apply = %d, want 1 (must be idempotent by RecipeID)", pool.openCount())
	}
}

// ── mcpRecipeSecretKeys ─────────────────────────────────────────────────

// TestMcpRecipeSecretKeys asserts a subset, not exact equality: production
// mcpRecipeSecretKeys deliberately runs over mergedRecipeCatalog (shipped +
// registry + userSource — spec D-4, "every EnvKey.Name across loaded
// recipes"), so the real shipped catalog's own EnvKeys are legitimately
// present in the result alongside the synthetic ones this test contributes
// via userSource.
func TestMcpRecipeSecretKeys(t *testing.T) {
	t.Parallel()
	source := func() []recipes.Recipe {
		return []recipes.Recipe{acmeRecipe(), noSecretRecipe(), secretRecipe()}
	}
	got := mcpRecipeSecretKeys(source)
	for _, want := range []string{"ACME_TOKEN", "SECRET_MCP_TOKEN"} {
		if !got[want] {
			t.Errorf("mcpRecipeSecretKeys() missing %q; got %v", want, got)
		}
	}
	if got["NOT_A_DECLARED_ENV_KEY"] {
		t.Errorf("mcpRecipeSecretKeys() contains a name no recipe declared — it must be the declared-name set, not a catch-all")
	}
}
