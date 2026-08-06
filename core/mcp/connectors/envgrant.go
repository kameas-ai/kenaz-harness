package connectors

import (
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// Per-recipe credential de-namespacing (spec 091 D6 /
// ADR-connector-consent-and-credentials §2a — normative).
//
// The Kenaz host writes one env-grant line per connector credential:
//
//	KENAZ_ENVGRANT_MCP_<RECIPE_ID_FOLDED>__<ENV_KEY>=<value>
//
// where <RECIPE_ID_FOLDED> is the recipe id uppercased with '-' → '_' and
// the separator is a DOUBLE underscore. Recipe ids are slugs over
// [a-z0-9-] (recipes.ValidateRecipeID), which excludes '_', so the fold is
// injective and '__' cannot occur inside a folded id — the (recipe, key)
// split is unambiguous.
//
// The env-grant render step inside the VM strips the KENAZ_ENVGRANT_
// prefix before the harness unit reads its EnvironmentFile (the spec-078
// KENAZ_ENVGRANT_ANTHROPIC_API_KEY → ANTHROPIC_API_KEY precedent), so the
// served process env normally carries MCP_<ID>__<KEY> — the shape the ADR
// names for the harness side. Both spellings are accepted here (stripped
// first) so a channel that delivers the unstripped name still resolves.

// envGrantPrefixes are the accepted process-env spellings of a connector
// credential grant, in lookup order.
var envGrantPrefixes = []string{"MCP_", "KENAZ_ENVGRANT_MCP_"}

// foldRecipeID uppercases a recipe id and folds '-' to '_' for use in an
// env var name. Injective for valid recipe ids (no '_' allowed in ids).
func foldRecipeID(id string) string {
	return strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

// DenamespaceEnv resolves a whitelisted recipe's declared env keys from
// the served process environment. ONLY keys the recipe declares are
// consulted, and ONLY inside that recipe's own namespace — connector A's
// grant can never appear in connector B's map. Unset (or empty) grants
// produce no entry; callers decide whether a missing Required key makes
// the connector unavailable.
//
// The returned map is what Recipe.ToServerSpec receives as env; combined
// with ServerSpec.IsolateEnv it is the ONLY credential material a spawned
// connector process can see.
func DenamespaceEnv(getenv func(string) string, recipe recipes.Recipe) map[string]string {
	if len(recipe.EnvKeys) == 0 {
		return nil
	}
	folded := foldRecipeID(recipe.ID)
	out := make(map[string]string, len(recipe.EnvKeys))
	for _, key := range recipe.EnvKeys {
		for _, prefix := range envGrantPrefixes {
			if v := getenv(prefix + folded + "__" + key.Name); v != "" {
				out[key.Name] = v
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MissingRequiredKeys returns the names of Required env keys that env does
// not carry, in declaration order. Empty means the recipe is spawnable.
func MissingRequiredKeys(recipe recipes.Recipe, env map[string]string) []string {
	var missing []string
	for _, key := range recipe.EnvKeys {
		if key.Required && env[key.Name] == "" {
			missing = append(missing, key.Name)
		}
	}
	return missing
}
