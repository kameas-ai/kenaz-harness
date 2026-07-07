package contextbootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	_ "embed"
)

// recipe.go implements WP10: LocalRecipeSource backed by the embedded
// bootstrap_recipe.json fixture.
//
// DEFERRED (fleet WP01): The authoritative recipe registry lives in
// kenaz-fleet. When fleet WP01 ("recipe schema + registry + push") and
// WP06 ("contract doc") land, replace LocalRecipeSource with a
// FleetRecipeSource that reads from the fleet config-bundle/catalog pipe.
//
// The fixture recipe (fixtures/bootstrap_recipe.json) proves the
// connector fan-out loop end-to-end with all defined connectors.
// New connectors are recipe additions — the engine does not change.

//go:embed fixtures/bootstrap_recipe.json
var embeddedRecipeJSON []byte

// LocalRecipeSource is a RecipeSource backed by the embedded fixture JSON.
// It satisfies the seam for harness-only operation (no fleet connectivity
// required). Safe for concurrent use; the recipe is parsed once at first call.
//
// DEFERRED: replaced by FleetRecipeSource when fleet WP01/WP06 merge.
type LocalRecipeSource struct {
	// override allows tests to inject an alternate recipe JSON.
	// nil = use the embedded fixture.
	override []byte
}

// NewLocalRecipeSource constructs a LocalRecipeSource using the embedded
// fixture recipe. Call NewLocalRecipeSourceFromJSON to inject an alternate
// recipe (for tests or fleet integration).
func NewLocalRecipeSource() *LocalRecipeSource {
	return &LocalRecipeSource{}
}

// NewLocalRecipeSourceFromJSON constructs a LocalRecipeSource using the
// provided JSON bytes instead of the embedded fixture. Used in tests and
// for the fleet integration shim.
func NewLocalRecipeSourceFromJSON(recipeJSON []byte) *LocalRecipeSource {
	return &LocalRecipeSource{override: recipeJSON}
}

// LoadRecipe implements RecipeSource. Parses the embedded (or overridden)
// JSON each call so that tests can inject different fixtures without
// restarting. In production the parse is cheap (< 1 ms).
func (s *LocalRecipeSource) LoadRecipe(_ context.Context) (*BootstrapRecipe, error) {
	src := embeddedRecipeJSON
	if len(s.override) > 0 {
		src = s.override
	}
	var r BootstrapRecipe
	if err := json.Unmarshal(src, &r); err != nil {
		return nil, fmt.Errorf("contextbootstrap: parse recipe: %w", err)
	}
	return &r, nil
}
