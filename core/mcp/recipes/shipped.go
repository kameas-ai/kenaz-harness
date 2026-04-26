package recipes

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// shippedJSON is the embedded v1 catalog. The bytes ship with the
// binary; LoadShipped parses them into *Catalog.
//
//go:embed shipped.json
var shippedJSON []byte

// shipped is the package-level singleton. It is parsed once at init
// (via mustLoad). Callers obtain it through Shipped() — never reach
// for shipped directly because that would let a caller mutate the
// recipe slice underneath other readers.
var shipped = mustLoad()

// LoadShipped parses the embedded shipped.json into a fresh *Catalog.
// Callers that want a private copy (tests, hot-reload experiments)
// use this; production code uses Shipped() to share the singleton.
//
// LoadShipped re-reads the embedded bytes on every call so that test
// failures in one test don't poison the singleton in another.
func LoadShipped() (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(shippedJSON, &cat); err != nil {
		return nil, fmt.Errorf("recipes: parse shipped.json: %w", err)
	}
	if cat.Version != 1 {
		return nil, fmt.Errorf("recipes: unsupported shipped.json version %d (want 1)", cat.Version)
	}
	for i := range cat.Recipes {
		if err := cat.Recipes[i].Validate(); err != nil {
			return nil, fmt.Errorf("recipes: shipped.json recipe[%d]: %w", i, err)
		}
	}
	return &cat, nil
}

// Shipped returns the package-level catalog. The returned pointer is
// the singleton; callers MUST treat it as read-only. To get a mutable
// copy, call LoadShipped().
func Shipped() *Catalog {
	return shipped
}

// mustLoad is the init-time loader. It panics on failure because the
// shipped.json bytes are build-time data: a parse error means the
// binary is malformed and there is no recovery path.
func mustLoad() *Catalog {
	cat, err := LoadShipped()
	if err != nil {
		panic(fmt.Sprintf("recipes: shipped.json failed to load: %v", err))
	}
	return cat
}
