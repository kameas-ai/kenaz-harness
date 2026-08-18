package mcp

// custom_recipe.go — SaveCustomRecipe, mcp-connector-lifecycle-01PMMC01
// WP06.
//
// CustomRecipeTab.vue's save() used to unconditionally throw — there was
// no backend to call. WP02 gated the whole Custom tab + the row Edit
// button behind CUSTOM_RECIPE_AUTHORING_ENABLED (a dated interim flag,
// see frontend/src/lib/customRecipeAuthoring.ts) until this landed.
// SaveCustomRecipe persists through recipes.UserStore.Save (already
// implemented and tested — user.go:487 pre-WP04 line numbers), which
// WP03's mcpUserRecipeSource reloads on every merged-catalog build, so a
// saved recipe is visible to Tools_ListRecipes in the same process,
// without a restart, the instant this call returns — same freshness
// contract as a paste-config import.

import (
	"context"
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// RecipeSaver is the persistence seam SaveCustomRecipe writes through.
// *recipes.UserStore satisfies it.
type RecipeSaver interface {
	Save(r recipes.Recipe) error
}

// WithRecipeSaver injects the user-recipe persistence backend used by
// SaveCustomRecipe. Without this option SaveCustomRecipe returns
// ErrRecipeSaverNotConfigured.
func WithRecipeSaver(s RecipeSaver) Option {
	return func(a *API) { a.recipeSaver = s }
}

// SaveCustomRecipeRequest is the wire shape
// frontend/src/views/tools/CustomRecipeTab.vue's save() submits. It is a
// narrower, purpose-built shape rather than a reuse of the frontend
// `Recipe` listing type — that type has no `command`/`url`/`transport`
// fields (the Tools list never needed to round-trip them), and forcing
// this request through it would either widen the listing type for one
// caller or silently drop fields.
type SaveCustomRecipeRequest struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// Transport is "stdio" | "http" | "sse". Empty defaults to "stdio".
	Transport string `json:"transport,omitempty"`
	// Command is the full argv for a stdio recipe (binary + args).
	// Required when Transport is "stdio" (or empty).
	Command []string `json:"command,omitempty"`
	// URL is required for "http"/"sse" transports.
	URL             string            `json:"url,omitempty"`
	HeadersTemplate map[string]string `json:"headers_template,omitempty"`
	// PostURL is required for "sse".
	PostURL string `json:"post_url,omitempty"`
}

// ErrRecipeSaverNotConfigured is returned when the boot wiring left
// RecipeSaver nil (the rpc.New(nil) test-harness path, or a DataDir-less
// boot where UserStore has nothing to write to).
var ErrRecipeSaverNotConfigured = errors.New("mcp: recipe saver not configured")

// SaveCustomRecipe validates req against the same rules every shipped
// recipe must satisfy (recipes.Recipe.Validate — a stdio recipe needs a
// non-empty Command, http/sse need a well-formed URL, sse additionally
// needs PostURL) and persists it as a user-owned recipe. The saved
// Recipe (Source stamped "user" by UserStore.Save) is returned so the
// caller can render it without a second round trip.
func (a *API) SaveCustomRecipe(_ context.Context, req SaveCustomRecipeRequest) (recipes.Recipe, error) {
	a.mu.RLock()
	saver := a.recipeSaver
	a.mu.RUnlock()
	if saver == nil {
		return recipes.Recipe{}, ErrRecipeSaverNotConfigured
	}

	transport := req.Transport
	if transport == "" {
		transport = recipes.TransportStdio
	}

	r := recipes.Recipe{
		ID:              req.ID,
		DisplayName:     req.DisplayName,
		Description:     req.Description,
		Category:        "other",
		Transport:       transport,
		Command:         req.Command,
		URL:             req.URL,
		HeadersTemplate: req.HeadersTemplate,
		PostURL:         req.PostURL,
		Capabilities:    recipes.Capabilities{Tools: true},
	}
	if err := r.Validate(); err != nil {
		return recipes.Recipe{}, fmt.Errorf("mcp: SaveCustomRecipe: %w", err)
	}
	if err := saver.Save(r); err != nil {
		return recipes.Recipe{}, fmt.Errorf("mcp: SaveCustomRecipe: %w", err)
	}
	r.Source = recipes.SourceUser
	return r, nil
}
