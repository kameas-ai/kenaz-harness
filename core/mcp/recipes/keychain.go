package recipes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// KeychainLocator returns the canonical keychain locator for a
// recipe env-key pair: "mcp/<recipeID>/<envName>".
//
// The locator is what we store in CredentialReference.Locator and
// what gets sha256'd into EnvAuditHash. It NEVER appears alongside
// the resolved value: write-side (FR-019 InstallRecipe) takes the
// user-supplied value and stuffs it into the OS keychain under this
// locator; read-side (this package's ResolveEnv) only ever sees the
// locator string.
func KeychainLocator(recipeID, envName string) string {
	return "mcp/" + recipeID + "/" + envName
}

// ResolveEnv resolves every EnvKey in recipe through backend and
// returns the env map ready to attach to exec.Cmd.Env.
//
// Resolution rules:
//   - Each EnvKey produces a CredentialReference{Kind: RefKeychain,
//     Locator: KeychainLocator(recipe.ID, key.Name)}.
//   - On resolution success the resolved bytes are copied into the
//     env map at key key.Name, then the Secret is destroyed before
//     the function returns. If the caller cancels mid-resolve, any
//     already-acquired Secrets are still destroyed in the deferred
//     cleanup loop.
//   - If a Required key fails to resolve, ResolveEnv returns the
//     wrapped error.
//   - If an optional key fails to resolve, the env map carries no
//     entry for it (the server will see the absent var and decide).
//   - The returned map never contains the locator string as a value
//     and never logs the resolved bytes — only the redaction-safe
//     CredentialReference.ID() / Locator are eligible for logs, and
//     this function emits none.
//
// On any error the function returns nil for the map; callers should
// not use a partially-built env to spawn a server.
func ResolveEnv(ctx context.Context, backend secrets.Backend, recipe Recipe) (map[string]string, error) {
	if backend == nil {
		return nil, errors.New("recipes: ResolveEnv: nil backend")
	}
	out := make(map[string]string, len(recipe.EnvKeys))

	// destroy collects every Secret we acquired so we can guarantee a
	// Destroy() pass even on the error paths.
	var acquired []secrets.Secret
	defer func() {
		for _, s := range acquired {
			s.Destroy()
		}
	}()

	for _, key := range recipe.EnvKeys {
		ref := secrets.CredentialReference{
			Kind:     secrets.RefKeychain,
			Locator:  KeychainLocator(recipe.ID, key.Name),
			Optional: !key.Required,
		}
		s, err := backend.Resolve(ctx, ref)
		if err != nil {
			if key.Required {
				return nil, fmt.Errorf("recipes: resolve required key %q for recipe %q: %w",
					key.Name, recipe.ID, err)
			}
			// Optional + unresolvable: skip the env var entirely.
			continue
		}
		acquired = append(acquired, s)
		// Use exposes the borrowed []byte for the duration of the
		// closure. We copy it into a string and immediately drop the
		// borrow — the Destroy in the defer will then zeroise the
		// underlying buffer.
		var value string
		useErr := s.Use(func(v []byte) error {
			value = string(v)
			return nil
		})
		if useErr != nil {
			if key.Required {
				return nil, fmt.Errorf("recipes: read required key %q for recipe %q: %w",
					key.Name, recipe.ID, useErr)
			}
			continue
		}
		out[key.Name] = value
	}
	return out, nil
}

// EnvAuditHash returns the sha256 (hex-encoded) of the sorted
// keychain locator strings for recipe.EnvKeys. NOT the values — only
// the locators, which are not credential material.
//
// Use case: data-model.md's EnvAuditHash field on EnabledRecipe.
// When the catalog rev changes which env vars a recipe needs, the
// hash changes too — surfaces a "configuration changed" hint to the
// user without revealing any credential. A recipe with no env keys
// hashes to the empty-string sha256.
func EnvAuditHash(recipe Recipe) string {
	locators := make([]string, 0, len(recipe.EnvKeys))
	for _, k := range recipe.EnvKeys {
		locators = append(locators, KeychainLocator(recipe.ID, k.Name))
	}
	sort.Strings(locators)
	h := sha256.New()
	// Newline separator so concatenation is unambiguous: a locator
	// can't legitimately contain a newline (env var names don't, and
	// recipe IDs match a strict regex).
	h.Write([]byte(strings.Join(locators, "\n")))
	return hex.EncodeToString(h.Sum(nil))
}
