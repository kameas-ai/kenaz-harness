package tools

// device_auth_test.go — connector-lifecycle-truth-01PMZ303 UNIT-5 (MO-13).
//
// BeginDeviceAuth's doc has always asserted two preconditions —
// PrimaryAuth == "device_code" AND Auth.Kind == "mcp_oauth" — but the body
// only ever checked Auth.Kind, then unconditionally built an
// oauth.GitHubDeviceConfig from whatever client_id it resolved. Latent
// today because among the 7 shipped device_code recipes only github
// carries an Auth block (spec.md §11 R-1), but the hole opens the moment a
// second one does: that recipe's client_id would be POSTed to GitHub's
// device-authorization and token endpoints instead of its own provider's.
import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

func deviceCodeRecipe(id, clientID string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		Transport:   recipes.TransportStdio,
		Command:     []string{"echo", "test"},
		PrimaryAuth: recipes.PrimaryAuthDeviceCode,
		Auth: &recipes.RecipeAuth{
			Kind:     recipes.AuthKindMCPOAuth,
			ClientID: clientID,
		},
	}
}

// TestBeginDeviceAuth_WrongPrimaryAuth_FailsClosed pins MO-13's first half:
// a recipe whose Auth.Kind is mcp_oauth but whose PrimaryAuth is NOT
// device_code must be rejected by BeginDeviceAuth's own stated
// precondition, not silently routed into the GitHub device flow.
//
// Mutation: remove the `recipe.PrimaryAuth != recipes.PrimaryAuthDeviceCode`
// guard. This test must fail — the call would instead proceed to the
// client-id resolution step for an arm BeginDeviceAuth was never meant to
// serve.
func TestBeginDeviceAuth_WrongPrimaryAuth_FailsClosed(t *testing.T) {
	t.Parallel()
	r := deviceCodeRecipe("not-device-code", "some-client-id")
	r.PrimaryAuth = recipes.PrimaryAuthBrowserOAuthDCR // wrong arm, same Auth.Kind
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}
	api := New(Config{Catalog: cat})

	_, err := api.BeginDeviceAuth(context.Background(), "not-device-code")
	if err == nil {
		t.Fatal("want an error for a non-device_code recipe, got nil")
	}
	if !strings.Contains(err.Error(), "not its sign-in entry point") {
		t.Fatalf("want the PrimaryAuth-mismatch message, got: %v", err)
	}
}

// TestBeginDeviceAuth_NonGitHubDeviceCodeRecipe_FailsClosed pins MO-13's
// second half: a device_code recipe that is NOT github must be refused
// rather than having its client_id sent to GitHub's hardcoded endpoints
// (oauth.GitHubDeviceConfig, device.go:313-320).
//
// Mutation: remove the `id != "github"` guard. This test must fail — the
// call would instead build a GitHubDeviceConfig carrying a client_id that
// belongs to a different provider entirely.
func TestBeginDeviceAuth_NonGitHubDeviceCodeRecipe_FailsClosed(t *testing.T) {
	t.Parallel()
	r := deviceCodeRecipe("some-future-provider", "some-future-client-id")
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}
	api := New(Config{Catalog: cat})

	_, err := api.BeginDeviceAuth(context.Background(), "some-future-provider")
	if err == nil {
		t.Fatal("want an error for a non-github device_code recipe, got nil")
	}
	if !strings.Contains(err.Error(), "only github's device-authorization endpoints are wired") {
		t.Fatalf("want the github-only message, got: %v", err)
	}
}
