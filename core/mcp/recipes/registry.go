package recipes

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// registryJSON is the embedded curated-registry catalog. The bytes
// ship with the binary; LoadRegistry parses them into *Catalog.
// Distinct from shipped.json (the 3 frozen filesystem recipes — C-003)
// — registry.json carries the larger curated set (github, fetch,
// brave-search, sqlite, postgres, slack, time, git, puppeteer,
// playwright) the install modal's "From registry" tab renders.
//
//go:embed registry.json
var registryJSON []byte

// registry is the package-level singleton, parsed once at init via
// mustLoadRegistry. Same singleton discipline as `shipped`: callers
// reach for it through Registry() so the slice can't be mutated
// underneath other readers.
var registry = mustLoadRegistry()

// LoadRegistry parses the embedded registry.json into a fresh
// *Catalog. Mirrors LoadShipped exactly: each call re-parses the
// embedded bytes (so a test that mutates the returned catalog can't
// poison the singleton), every entry is run through Recipe.Validate,
// and the loader stamps SourceRegistry on every parsed recipe.
//
// Parse, version, and per-entry validation failures all surface as
// errors. The registry is build-time data: a malformed file means
// the binary is broken, so mustLoadRegistry panics at init.
func LoadRegistry() (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(registryJSON, &cat); err != nil {
		return nil, fmt.Errorf("recipes: parse registry.json: %w", err)
	}
	if cat.Version != 1 {
		return nil, fmt.Errorf("recipes: unsupported registry.json version %d (want 1)", cat.Version)
	}
	for i := range cat.Recipes {
		if err := cat.Recipes[i].Validate(); err != nil {
			return nil, fmt.Errorf("recipes: registry.json recipe[%d]: %w", i, err)
		}
		cat.Recipes[i].Source = SourceRegistry
	}
	return &cat, nil
}

// Registry returns the package-level curated-registry catalog. The
// returned pointer is the singleton; callers MUST treat it as
// read-only. Use LoadRegistry() to obtain a private mutable copy.
func Registry() *Catalog {
	return registry
}

// mustLoadRegistry is the init-time loader. It panics on failure for
// the same reason mustLoad does for shipped.json: registry.json is
// build-time data; a parse error means the binary is malformed and
// there is no recovery path.
func mustLoadRegistry() *Catalog {
	cat, err := LoadRegistry()
	if err != nil {
		panic(fmt.Sprintf("recipes: registry.json failed to load: %v", err))
	}
	return cat
}
