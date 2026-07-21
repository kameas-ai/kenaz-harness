package recipes_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestPrimaryAuthValidation verifies that Recipe.Validate() accepts all
// recognised PrimaryAuth values and rejects unknown ones.
func TestPrimaryAuthValidation(t *testing.T) {
	t.Parallel()

	base := func() recipes.Recipe {
		return recipes.Recipe{
			ID:          "test-recipe",
			DisplayName: "Test",
			Description: "desc",
			Category:    "other",
			Command:     []string{"echo"},
		}
	}

	valid := []struct {
		name        string
		primaryAuth string
	}{
		{"empty (legacy default)", ""},
		{"oauth", recipes.PrimaryAuthOAuth},
		{"device_code", recipes.PrimaryAuthDeviceCode},
		{"keys", recipes.PrimaryAuthKeys},
		{"none", recipes.PrimaryAuthNone},
		{"browser_oauth_dcr", recipes.PrimaryAuthBrowserOAuthDCR},
		{"browser_oauth_pkce", recipes.PrimaryAuthBrowserOAuthPKCE},
	}
	for _, tc := range valid {
		tc := tc
		t.Run("valid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			r := base()
			r.PrimaryAuth = tc.primaryAuth
			if err := r.Validate(); err != nil {
				t.Errorf("Validate() returned error for primary_auth=%q: %v", tc.primaryAuth, err)
			}
		})
	}

	invalid := []string{"browser", "sso", "basic", "bearer", "KEYS", "OAUTH"}
	for _, v := range invalid {
		v := v
		t.Run("invalid/"+v, func(t *testing.T) {
			t.Parallel()
			r := base()
			r.PrimaryAuth = v
			if err := r.Validate(); err == nil {
				t.Errorf("Validate() did not return error for invalid primary_auth=%q", v)
			}
		})
	}
}

// TestShippedRecipesPrimaryAuth verifies that every shipped catalog entry
// declares a known (or empty) primary_auth value and that the shipped
// catalog round-trips cleanly through Validate.
func TestShippedRecipesPrimaryAuth(t *testing.T) {
	t.Parallel()
	cat := recipes.Shipped()
	for _, r := range cat.List() {
		r := r
		t.Run(r.ID, func(t *testing.T) {
			t.Parallel()
			if err := r.Validate(); err != nil {
				t.Errorf("shipped recipe %q Validate() error: %v", r.ID, err)
			}
			// Ensure primary_auth is non-empty for shipped recipes (we want
			// explicit intent rather than silent fallback).
			if r.PrimaryAuth == "" {
				t.Errorf("shipped recipe %q has empty primary_auth (set explicitly)", r.ID)
			}
		})
	}
}

// TestRegistryRecipesPrimaryAuth applies the same check to the registry catalog.
func TestRegistryRecipesPrimaryAuth(t *testing.T) {
	t.Parallel()
	cat, err := recipes.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	for _, r := range cat.List() {
		r := r
		t.Run(r.ID, func(t *testing.T) {
			t.Parallel()
			if err := r.Validate(); err != nil {
				t.Errorf("registry recipe %q Validate() error: %v", r.ID, err)
			}
		})
	}
}
