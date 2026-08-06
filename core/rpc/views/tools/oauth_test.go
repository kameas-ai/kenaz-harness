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

// fakeTokenSource satisfies ConnectorTokenSource for the served-mode
// broker fallback tests (spec 091 D8).
type fakeTokenSource struct {
	token string
	err   error
	calls int
}

func (f *fakeTokenSource) ConnectorToken(context.Context, string) (string, error) {
	f.calls++
	return f.token, f.err
}

func TestInjectOAuthBearer_BrokerFallbackWhenNoLocalCredential(t *testing.T) {
	t.Parallel()
	src := &fakeTokenSource{token: "broker-at"}
	api := New(Config{Secrets: secrets.NewMemoryBackend(), ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer broker-at" {
		t.Errorf("Authorization = %q, want broker bearer", got)
	}
	if src.calls != 1 {
		t.Errorf("broker calls = %d, want 1", src.calls)
	}
}

func TestInjectOAuthBearer_BrokerFallbackFailureIsDeferred(t *testing.T) {
	t.Parallel()
	src := &fakeTokenSource{err: context.DeadlineExceeded}
	api := New(Config{Secrets: secrets.NewMemoryBackend(), ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	// A broker failure is deferred auth — no header, no error, no spawn
	// failure.
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if _, ok := spec.HeadersTemplate["Authorization"]; ok {
		t.Error("failed broker fallback must leave the spec unauthenticated")
	}
}

func TestInjectOAuthBearer_LocalCredentialWinsOverBroker(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	cred := &oauth.StoredCredential{
		AccessToken: "stored-at",
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	blob, err := cred.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	backend.SetEntries(map[string][]byte{
		secrets.RefKeychain.String() + "|" + recipes.OAuthCredentialLocator("remote-oauth"): blob,
	})
	src := &fakeTokenSource{token: "broker-at"}
	api := New(Config{Secrets: backend, ConnectorTokens: src})
	spec := &coremcp.ServerSpec{Name: "remote-oauth"}
	if err := api.injectOAuthBearer(context.Background(), oauthRecipe("cid"), spec); err != nil {
		t.Fatalf("injectOAuthBearer: %v", err)
	}
	if got := spec.HeadersTemplate["Authorization"]; got != "Bearer stored-at" {
		t.Errorf("Authorization = %q, want stored token", got)
	}
	if src.calls != 0 {
		t.Errorf("broker consulted despite a valid local credential (%d calls)", src.calls)
	}
}
