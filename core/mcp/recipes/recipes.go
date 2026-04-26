// Package recipes is the shipped-recipes catalog for the MCP stdio
// pool. It exposes:
//
//   - Recipe / EnvKey / Capabilities / SamplingPolicy / Catalog —
//     the data-model types parsed from the embedded shipped.json.
//   - LoadShipped / Shipped — accessors for the package-level
//     singleton parsed at process start.
//   - EnabledRecipes — the persisted on-disk list of toggled-on
//     recipes (atomic save, corrupt-tolerant load).
//   - KeychainLocator / ResolveEnv / EnvAuditHash — the secrets-
//     backed env resolver used by the pool when spawning a server.
//
// The package is independent of `core/mcp/stdio/`: it only produces
// `core/mcp.ServerSpec` values via (*Recipe).ToServerSpec, which is
// the public type the existing pool surface already consumes.
//
// Spec mapping: FR-018 (catalog), FR-019 (enabled persistence),
// FR-020 (secrets resolve), NFR-006 (no plaintext on disk).
package recipes

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/sigil-tech/kaneaz-harness/core/mcp"
)

// Recipe is one entry in the shipped catalog. The fields mirror the
// data-model precisely; JSON tags match the on-disk shipped.json shape
// (snake_case).
type Recipe struct {
	// ID is the catalog primary key. It must match
	// `^[a-z][a-z0-9-]{0,63}$` because it doubles as the prefix of
	// every keychain locator and as `mcp.Tool.Server` for tools
	// produced by this server.
	ID string `json:"id"`
	// DisplayName is the user-visible recipe label.
	DisplayName string `json:"display_name"`
	// Description is the user-visible blurb in the install modal.
	Description string `json:"description"`
	// Category groups recipes in the modal ("search", "filesystem",
	// "memory", "fetch", ...).
	Category string `json:"category"`
	// Command is the argv used to spawn the stdio server.
	// Command[0] must be non-empty.
	Command []string `json:"command"`
	// EnvKeys are the credential-bearing env vars the server reads.
	// Slice order is render order in the install modal.
	EnvKeys []EnvKey `json:"env_keys"`
	// Capabilities is the recipe-author's declaration; the
	// negotiated set lives on ServerInstance.Negotiated.
	Capabilities Capabilities `json:"capabilities"`
	// DocsURL is the recipe-level documentation link.
	DocsURL string `json:"docs_url"`
	// InitTimeoutMs is the post-spawn initialize deadline. 0 -> 5000.
	InitTimeoutMs int `json:"init_timeout_ms"`
	// PingPeriodMs is the keepalive ping cadence. 0 -> 30000.
	PingPeriodMs int `json:"ping_period_ms"`
	// SamplingPolicy is the per-recipe sampling default.
	SamplingPolicy SamplingPolicy `json:"sampling_policy"`
}

// EnvKey is one credential-bearing env var the server reads.
type EnvKey struct {
	// Name is the exact env var the server looks up
	// (e.g. "BRAVE_API_KEY").
	Name string `json:"name"`
	// Display is the modal label.
	Display string `json:"display"`
	// DocsURL points at the provider's API-key issuance page.
	DocsURL string `json:"docs_url"`
	// Required marks env vars whose absence prevents the recipe from
	// running. ResolveEnv returns an error when a required key fails
	// to resolve.
	Required bool `json:"required"`
}

// Capabilities is the recipe-author's declaration of which MCP
// capabilities the server advertises. The harness still issues
// tools/list / resources/list / prompts/list against the negotiated
// capability set returned from initialize; this struct is a hint that
// drives modal copy and pre-flight expectations.
type Capabilities struct {
	Tools     bool `json:"tools"`
	Resources bool `json:"resources"`
	Prompts   bool `json:"prompts"`
	Sampling  bool `json:"sampling"`
}

// SamplingPolicy is the per-recipe sampling configuration.
//
// Allowed: whether the recipe permits sampling at all (recipe-author
// choice; some servers should never call back into the LLM).
// Default: the initial value of the per-server sampling toggle when
// the recipe is first enabled; user can flip it later via FR-015.
type SamplingPolicy struct {
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
}

// Catalog is the shipped-recipes container parsed from shipped.json.
// Iteration order in List() matches declaration order in the JSON
// (so the modal renders categories in catalog-author order).
type Catalog struct {
	Version int      `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

// List returns the recipes in catalog declaration order. Callers may
// retain the slice; the underlying memory is shared with the catalog
// (recipes are read-only after parse).
func (c *Catalog) List() []Recipe {
	if c == nil {
		return nil
	}
	out := make([]Recipe, len(c.Recipes))
	copy(out, c.Recipes)
	return out
}

// Get returns the recipe with id, or (zero, false) if no such recipe
// exists in the catalog.
func (c *Catalog) Get(id string) (Recipe, bool) {
	if c == nil {
		return Recipe{}, false
	}
	for i := range c.Recipes {
		if c.Recipes[i].ID == id {
			return c.Recipes[i], true
		}
	}
	return Recipe{}, false
}

// Sentinel errors. Callers branch on errors.Is.
var (
	// ErrRecipeNotFound is returned when a lookup misses the catalog.
	ErrRecipeNotFound = errors.New("recipes: recipe not found")
	// ErrInvalidRecipeID is returned when a recipe ID violates the
	// validation regex.
	ErrInvalidRecipeID = errors.New("recipes: invalid recipe id")
	// ErrInvalidRecipe is returned by validation for non-ID problems
	// (empty Command, etc.).
	ErrInvalidRecipe = errors.New("recipes: invalid recipe")
)

// recipeIDPattern is the canonical recipe ID validation regex.
var recipeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidateRecipeID reports whether id is a syntactically valid recipe
// ID. It returns nil on success or ErrInvalidRecipeID wrapped with the
// offending id.
func ValidateRecipeID(id string) error {
	if !recipeIDPattern.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidRecipeID, id)
	}
	return nil
}

// Validate runs the recipe-level invariants. It is called by
// LoadShipped on every parsed recipe so that a bad shipped.json fails
// the binary at init time.
func (r *Recipe) Validate() error {
	if err := ValidateRecipeID(r.ID); err != nil {
		return err
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		return fmt.Errorf("%w: recipe %q has empty Command", ErrInvalidRecipe, r.ID)
	}
	for i, k := range r.EnvKeys {
		if k.Name == "" {
			return fmt.Errorf("%w: recipe %q env_keys[%d] has empty Name", ErrInvalidRecipe, r.ID, i)
		}
	}
	return nil
}

// ToServerSpec produces the mcp.ServerSpec the pool consumes when
// spawning this recipe. env is the resolved env-var map (typically
// produced by ResolveEnv). The returned ServerSpec uses Transport
// "stdio" and copies Command verbatim.
func (r *Recipe) ToServerSpec(env map[string]string) mcp.ServerSpec {
	cmd := make([]string, len(r.Command))
	copy(cmd, r.Command)

	var envCopy map[string]string
	if len(env) > 0 {
		envCopy = make(map[string]string, len(env))
		for k, v := range env {
			envCopy[k] = v
		}
	}

	return mcp.ServerSpec{
		Name:      r.ID,
		Transport: "stdio",
		Command:   cmd,
		Env:       envCopy,
	}
}
