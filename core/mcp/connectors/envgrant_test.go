package connectors

import (
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

func mapGetenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TestDenamespaceEnv_Isolation is the spec 091 D6 isolation assertion: two
// connectors' grants coexist in the served process env, and each recipe
// resolves ONLY its own declared keys — never a sibling's secret, never an
// undeclared key from its own namespace.
func TestDenamespaceEnv_Isolation(t *testing.T) {
	env := map[string]string{
		// google-drive's grants (stripped, the normal in-VM shape).
		"MCP_GOOGLE_DRIVE__GDRIVE_TOKEN": "gdrive-secret",
		// datadog's grants.
		"MCP_DATADOG__DD_API_KEY": "dd-secret",
		"MCP_DATADOG__DD_APP_KEY": "dd-app-secret",
		// An undeclared key inside datadog's namespace must NOT resolve.
		"MCP_DATADOG__SMUGGLED": "nope",
		// Ambient process env must never resolve as a grant.
		"DD_API_KEY": "ambient-nope",
	}
	gdrive := recipes.Recipe{
		ID:      "google-drive",
		EnvKeys: []recipes.EnvKey{{Name: "GDRIVE_TOKEN", Required: true}},
	}
	datadog := recipes.Recipe{
		ID: "datadog",
		EnvKeys: []recipes.EnvKey{
			{Name: "DD_API_KEY", Required: true},
			{Name: "DD_APP_KEY"},
		},
	}

	gotG := DenamespaceEnv(mapGetenv(env), gdrive)
	if want := map[string]string{"GDRIVE_TOKEN": "gdrive-secret"}; !reflect.DeepEqual(gotG, want) {
		t.Errorf("google-drive env = %v, want %v", gotG, want)
	}
	gotD := DenamespaceEnv(mapGetenv(env), datadog)
	want := map[string]string{"DD_API_KEY": "dd-secret", "DD_APP_KEY": "dd-app-secret"}
	if !reflect.DeepEqual(gotD, want) {
		t.Errorf("datadog env = %v, want %v", gotD, want)
	}
	for _, m := range []map[string]string{gotG, gotD} {
		for k, v := range m {
			if v == "nope" || v == "ambient-nope" || k == "SMUGGLED" {
				t.Errorf("leaked entry %q=%q", k, v)
			}
		}
	}
	if gotG["DD_API_KEY"] != "" {
		t.Error("google-drive resolved datadog's secret")
	}
}

// TestDenamespaceEnv_UnstrippedFallback verifies the full
// KENAZ_ENVGRANT_MCP_* spelling also resolves (channel that does not
// strip the prefix), with the stripped spelling taking precedence.
func TestDenamespaceEnv_UnstrippedFallback(t *testing.T) {
	recipe := recipes.Recipe{
		ID:      "slack",
		EnvKeys: []recipes.EnvKey{{Name: "SLACK_TOKEN", Required: true}},
	}
	got := DenamespaceEnv(mapGetenv(map[string]string{
		"KENAZ_ENVGRANT_MCP_SLACK__SLACK_TOKEN": "unstripped",
	}), recipe)
	if got["SLACK_TOKEN"] != "unstripped" {
		t.Errorf("unstripped spelling did not resolve: %v", got)
	}

	got = DenamespaceEnv(mapGetenv(map[string]string{
		"MCP_SLACK__SLACK_TOKEN":                "stripped",
		"KENAZ_ENVGRANT_MCP_SLACK__SLACK_TOKEN": "unstripped",
	}), recipe)
	if got["SLACK_TOKEN"] != "stripped" {
		t.Errorf("stripped spelling should win: %v", got)
	}
}

// TestDenamespaceEnv_DoubleUnderscoreSeparator pins the injectivity the
// ADR designed for: google-drive+X and google+DRIVE__X are distinct names
// only because of the double-underscore separator.
func TestDenamespaceEnv_DoubleUnderscoreSeparator(t *testing.T) {
	env := map[string]string{
		"MCP_GOOGLE_DRIVE__X": "for-google-drive",
		"MCP_GOOGLE__DRIVE_X": "for-google",
	}
	gd := recipes.Recipe{ID: "google-drive", EnvKeys: []recipes.EnvKey{{Name: "X"}}}
	g := recipes.Recipe{ID: "google", EnvKeys: []recipes.EnvKey{{Name: "DRIVE_X"}}}

	if got := DenamespaceEnv(mapGetenv(env), gd); got["X"] != "for-google-drive" {
		t.Errorf("google-drive/X = %v", got)
	}
	if got := DenamespaceEnv(mapGetenv(env), g); got["DRIVE_X"] != "for-google" {
		t.Errorf("google/DRIVE_X = %v", got)
	}
}

func TestMissingRequiredKeys(t *testing.T) {
	recipe := recipes.Recipe{
		ID: "datadog",
		EnvKeys: []recipes.EnvKey{
			{Name: "DD_API_KEY", Required: true},
			{Name: "DD_APP_KEY"},
			{Name: "DD_SITE", Required: true},
		},
	}
	got := MissingRequiredKeys(recipe, map[string]string{"DD_API_KEY": "x"})
	if want := []string{"DD_SITE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingRequiredKeys = %v, want %v", got, want)
	}
	if got := MissingRequiredKeys(recipe, map[string]string{"DD_API_KEY": "x", "DD_SITE": "y"}); got != nil {
		t.Errorf("MissingRequiredKeys = %v, want nil", got)
	}
}
