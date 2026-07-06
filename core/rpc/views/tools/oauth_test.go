package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/oauth"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func oauthRecipe(clientID string) recipes.Recipe {
	return recipes.Recipe{
		ID:        "remote-oauth",
		Transport: recipes.TransportHTTP,
		URL:       "https://api.example.com/mcp/",
		Auth: &recipes.RecipeAuth{
			Kind:     recipes.AuthKindMCPOAuth,
			ClientID: clientID,
			Scopes:   []string{"repo"},
		},
	}
}

func TestSignInRecipe_NotOAuthRecipe(t *testing.T) {
	t.Parallel()
	api := New(Config{Catalog: testCatalog("plain")})
	_, err := api.SignInRecipe(context.Background(), "plain")
	if err == nil || !strings.Contains(err.Error(), "not an OAuth recipe") {
		t.Fatalf("want not-an-oauth-recipe error, got %v", err)
	}
}

func TestSignInRecipe_NoClientID(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{oauthRecipe("")}}
	api := New(Config{Catalog: cat})
	_, err := api.SignInRecipe(context.Background(), "remote-oauth")
	if err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("want client_id error, got %v", err)
	}
}

func TestInjectOAuthBearer_NonOAuthIsNoop(t *testing.T) {
	t.Parallel()
	api := New(Config{Secrets: secrets.NewMemoryBackend()})
	spec := &coremcp.ServerSpec{Name: "x"}
	if err := api.injectOAuthBearer(context.Background(), testRecipe("x"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("non-OAuth recipe should not get an Authorization header")
	}
}

func TestInjectOAuthBearer_DeferredWhenNotSignedIn(t *testing.T) {
	t.Parallel()
	api := New(Config{Secrets: secrets.NewMemoryBackend()})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	// OAuth recipe but no stored credential → deferred auth, no header, no error.
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer deferred: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("deferred-auth recipe should not get an Authorization header")
	}
}

func TestInjectOAuthBearer_InjectsStoredToken(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	cred := &oauth.StoredCredential{
		AccessToken: "stored-at",
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(time.Hour), // valid, no refresh needed
	}
	blob, err := cred.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	locator := recipes.OAuthCredentialLocator("remote-oauth")
	backend.SetEntries(map[string][]byte{
		secrets.RefKeychain.String() + "|" + locator: blob,
	})

	api := New(Config{Secrets: backend})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer stored-at" {
		t.Errorf("Authorization = %q, want Bearer stored-at", got)
	}
}
