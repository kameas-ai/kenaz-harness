package recipes_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// expectedRegistryIDs is the WP06 mandate: these ids — and only
// these — make up the curated registry catalog. Drift from the list
// fails the test so a future PR that adds an entry without updating
// the spec gets caught.
// slack-tokens is the advanced stdio fallback; slack is the primary remote MCP recipe.
var expectedRegistryIDs = []string{
	"github",
	"fetch",
	"brave-search",
	"sqlite",
	"postgres",
	"slack",
	"slack-tokens",
	"gmail",
	"outlook",
	"time",
	"git",
	"puppeteer",
	"playwright",
	"notion",
	"linear",
	"sentry",
	"cloudflare",
	"asana",
	"atlassian",
	"zapier",
	"make",
	"pipedream",
	"composio",
	"n8n",
	"workato",
	"onedrive",
	"teams",
	"sharepoint",
	"excel",
	"supabase",
	"gitlab",
	"google-maps",
	"mysql",
	"mongodb",
	"redis",
	"ringcentral",
	"webex",
	"front",
	"discord",
	"zoom",
	"twilio",
	"intercom",
	"freshdesk",
	"crowdstrike-aidr",
	"help-scout",
	"okta",
	"zendesk",
	"servicenow",
	"jamf-docs",
	"mercury",
	"ramp",
	"deel",
	"greenhouse",
	"ashby",
	"xero",
	"quickbooks",
	"brex",
	"rippling",
	"netsuite",
	"workday",
	"grafana",
	"datadog",
	"new-relic",
	"dynatrace",
	"splunk",
	"circleci",
	"netlify",
	"vercel",
	"bitbucket",
	"pagerduty",
	"snyk",
	"sonar",
	"jenkins",
	"google-calendar",
	"google-drive",
	"google-docs",
	"google-sheets",
	"clickup",
	"monday",
	"shortcut",
	"smartsheet",
	"wrike",
	"trello",
	"coda",
	"mixpanel",
	"amplitude",
	"klaviyo",
	"dbt-cloud",
	"tableau",
	"fivetran",
	"redshift",
	"snowflake",
	"bigquery",
	"metabase",
	"looker",
	"ga4",
	"mailchimp",
	"braze",
	"marketo",
	"databricks",
	"elasticsearch",
	"airtable",
	"canva",
	"figma",
	"miro",
	"dropbox",
	"paypal",
	"square",
	"hubspot",
	"box",
	"salesforce",
	"bigcommerce-docs",
	"shopify",
	"pipedrive",
	"stripe",
}

func TestRegistrySingletonParses(t *testing.T) {
	cat := recipes.Registry()
	if cat == nil {
		t.Fatal("Registry() returned nil")
	}
	if got, want := len(cat.List()), len(expectedRegistryIDs); got != want {
		t.Fatalf("registry recipe count = %d, want %d", got, want)
	}
}

func TestLoadRegistryReturnsFreshCopy(t *testing.T) {
	a, err := recipes.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	b, err := recipes.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry (second): %v", err)
	}
	if a == b {
		t.Fatal("LoadRegistry should return a fresh *Catalog each call, got identical pointer")
	}
	if len(a.Recipes) != len(b.Recipes) {
		t.Fatalf("recipe count drift: %d vs %d", len(a.Recipes), len(b.Recipes))
	}
}

func TestRegistryAllExpectedIDsPresent(t *testing.T) {
	cat := recipes.Registry()
	for _, id := range expectedRegistryIDs {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("registry missing expected id %q", id)
		}
	}
}

func TestRegistryEverySourceIsRegistry(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if r.Source != recipes.SourceRegistry {
			t.Errorf("recipe %q Source = %q, want %q", r.ID, r.Source, recipes.SourceRegistry)
		}
	}
}

func TestRegistryEveryRecipeValidates(t *testing.T) {
	// LoadRegistry already calls Validate; this test guards against a
	// future refactor that quietly drops the validation step.
	cat := recipes.Registry()
	for _, r := range cat.List() {
		r := r
		if err := r.Validate(); err != nil {
			t.Errorf("recipe %q failed Validate: %v", r.ID, err)
		}
	}
}

func TestRegistryDoesNotShadowShipped(t *testing.T) {
	// C-003: shipped.json is frozen. The registry must not collide on
	// id with any of the 3 shipped filesystem recipes — collisions are
	// permitted by MergedCatalog (registry would shadow shipped) but a
	// silent shadow on the boot path defeats C-003's intent.
	shipped := recipes.Shipped()
	registry := recipes.Registry()
	shippedIDs := map[string]struct{}{}
	for _, r := range shipped.List() {
		shippedIDs[r.ID] = struct{}{}
	}
	for _, r := range registry.List() {
		if _, clash := shippedIDs[r.ID]; clash {
			t.Errorf("registry recipe %q collides with shipped recipe id (C-003)", r.ID)
		}
	}
}

func TestRegistryRoundTripsJSON(t *testing.T) {
	// Round-trip every entry: marshal → unmarshal → re-validate. If
	// any entry has a JSON tag that doesn't survive a round trip
	// (unknown field, type mismatch), this fails.
	cat := recipes.Registry()
	for _, r := range cat.List() {
		r := r
		t.Run(r.ID, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(&r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got recipes.Recipe
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("post-roundtrip Validate: %v", err)
			}
			if got.ID != r.ID {
				t.Errorf("ID drift: %q vs %q", got.ID, r.ID)
			}
			if got.DisplayName != r.DisplayName {
				t.Errorf("DisplayName drift: %q vs %q", got.DisplayName, r.DisplayName)
			}
			if len(got.Command) != len(r.Command) {
				t.Errorf("Command length drift: %d vs %d", len(got.Command), len(r.Command))
			}
			if len(got.EnvKeys) != len(r.EnvKeys) {
				t.Errorf("EnvKeys length drift: %d vs %d", len(got.EnvKeys), len(r.EnvKeys))
			}
		})
	}
}

func TestRegistryGitHubRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("github")
	if !ok {
		t.Fatal("github not in registry")
	}
	if r.Category != "web" {
		t.Errorf("Category = %q, want web", r.Category)
	}
	// GitHub now points at the official remote MCP server over HTTP and signs
	// in via OAuth (no token to paste); the PAT env key is an optional fallback.
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("Auth.Scopes should list the requested OAuth scopes")
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "GITHUB_PERSONAL_ACCESS_TOKEN" {
		t.Fatalf("EnvKeys = %+v, want one optional GITHUB_PERSONAL_ACCESS_TOKEN", r.EnvKeys)
	}
	if r.EnvKeys[0].Required {
		t.Error("PAT should be optional (OAuth is the primary path)")
	}
}

func TestRegistryFetchRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("fetch")
	if !ok {
		t.Fatal("fetch not in registry")
	}
	if r.Category != "web" {
		t.Errorf("Category = %q, want web", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("fetch should have no env keys, got %d", len(r.EnvKeys))
	}
}

func TestRegistryBraveSearchRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("brave-search")
	if !ok {
		t.Fatal("brave-search not in registry")
	}
	if r.Category != "web" {
		t.Errorf("Category = %q, want web", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "BRAVE_API_KEY" {
		t.Errorf("EnvKeys = %+v, want one entry named BRAVE_API_KEY", r.EnvKeys)
	}
}

func TestRegistrySQLiteRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("sqlite")
	if !ok {
		t.Fatal("sqlite not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if len(r.ConfigOptions) != 1 || r.ConfigOptions[0].Name != "db_path" {
		t.Errorf("ConfigOptions = %+v, want one entry named db_path", r.ConfigOptions)
	}
	if !r.ConfigOptions[0].Required {
		t.Error("db_path should be required")
	}
}

func TestRegistryPostgresRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("postgres")
	if !ok {
		t.Fatal("postgres not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "POSTGRES_CONNECTION_STRING" {
		t.Errorf("EnvKeys = %+v, want one entry named POSTGRES_CONNECTION_STRING", r.EnvKeys)
	}
}

func TestRegistrySlackRecipe(t *testing.T) {
	cat := recipes.Registry()

	// Primary recipe: remote MCP over HTTP with OAuth PKCE.
	r, ok := cat.Get("slack")
	if !ok {
		t.Fatal("slack not in registry")
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.slack.com" {
		t.Errorf("URL = %q, want https://mcp.slack.com", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthOAuth {
		t.Errorf("PrimaryAuth = %q, want oauth", r.PrimaryAuth)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Errorf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// client_id is intentionally empty (placeholder); must resolve at runtime via
	// KAMEAS_SLACK_OAUTH_CLIENT_ID env / build override.
	if r.Auth.ClientID != "" {
		t.Errorf("slack auth.client_id = %q, want empty placeholder (TODO: bake registered Kameas Slack app client_id)", r.Auth.ClientID)
	}
	if hdr, ok := r.HeadersTemplate["Authorization"]; !ok || hdr == "" {
		t.Errorf("HeadersTemplate[Authorization] missing or empty; want Bearer ${SLACK_TOKEN}")
	}
	// No env keys required for primary path — token is injected via OAuth.
	if len(r.EnvKeys) != 0 {
		t.Errorf("slack remote recipe should have no required env keys, got %d", len(r.EnvKeys))
	}

	// Advanced fallback: stdio recipe with bot + app tokens.
	rt, ok := cat.Get("slack-tokens")
	if !ok {
		t.Fatal("slack-tokens not in registry")
	}
	if rt.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("slack-tokens PrimaryAuth = %q, want keys", rt.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range rt.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SLACK_BOT_TOKEN"] || !envNames["SLACK_APP_TOKEN"] {
		t.Errorf("slack-tokens EnvKeys = %+v, want both SLACK_BOT_TOKEN and SLACK_APP_TOKEN", rt.EnvKeys)
	}
}

func TestRegistryTimeRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("time")
	if !ok {
		t.Fatal("time not in registry")
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("time should have no env keys, got %d", len(r.EnvKeys))
	}
}

func TestRegistryGitRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("git")
	if !ok {
		t.Fatal("git not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if len(r.ConfigOptions) != 1 || r.ConfigOptions[0].Name != "repo_path" {
		t.Errorf("ConfigOptions = %+v, want one entry named repo_path", r.ConfigOptions)
	}
}

func TestRegistryPuppeteerRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("puppeteer")
	if !ok {
		t.Fatal("puppeteer not in registry")
	}
	if r.Warning == "" {
		t.Error("puppeteer should carry a Warning (npm fetch / Chromium download)")
	}
}

func TestRegistryPlaywrightRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("playwright")
	if !ok {
		t.Fatal("playwright not in registry")
	}
	if r.Warning == "" {
		t.Error("playwright should carry a Warning (npm fetch / browser binaries)")
	}
}

func TestRegistryEveryRecipeHasCommand(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		// Remote recipes (http/sse) carry a URL instead of a stdio command.
		if r.Transport == recipes.TransportHTTP || r.Transport == recipes.TransportSSE {
			if r.URL == "" {
				t.Errorf("remote recipe %q has empty URL", r.ID)
			}
			continue
		}
		if len(r.Command) == 0 || r.Command[0] == "" {
			t.Errorf("stdio recipe %q has empty Command", r.ID)
		}
	}
}

func TestRegistryWiresIntoMergedCatalog(t *testing.T) {
	// Acceptance: tools view's ListRecipes returns shipped + registry
	// merged in source-tagged order. Exercise the same wiring shape
	// the rpc layer uses.
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return recipes.Shipped().List() },
		func() []recipes.Recipe { return recipes.Registry().List() },
		nil,
	)
	merged := mc.Recipes()
	want := len(recipes.Shipped().List()) + len(recipes.Registry().List())
	if len(merged) != want {
		t.Fatalf("merged recipe count = %d, want %d (no id collisions expected)", len(merged), want)
	}
	// Source-tagged: every entry carries its origin source.
	shippedSeen := 0
	registrySeen := 0
	for _, r := range merged {
		switch r.Source {
		case recipes.SourceShipped:
			shippedSeen++
		case recipes.SourceRegistry:
			registrySeen++
		default:
			t.Errorf("recipe %q has unexpected Source %q", r.ID, r.Source)
		}
	}
	if shippedSeen != len(recipes.Shipped().List()) {
		t.Errorf("shipped count in merged = %d, want %d", shippedSeen, len(recipes.Shipped().List()))
	}
	if registrySeen != len(recipes.Registry().List()) {
		t.Errorf("registry count in merged = %d, want %d", registrySeen, len(recipes.Registry().List()))
	}
}

func TestRecipe_Notion(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("notion")
	if !ok {
		t.Fatal("notion not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.notion.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.notion.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("notion auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if r.Warning == "" {
		t.Error("notion should carry a Warning (beta label)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Linear(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("linear")
	if !ok {
		t.Fatal("linear not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("URL = %q, want https://mcp.linear.app/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("linear auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("linear Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Sentry(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("sentry")
	if !ok {
		t.Fatal("sentry not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("URL = %q, want https://mcp.sentry.dev/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("sentry auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("sentry Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Cloudflare(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("cloudflare")
	if !ok {
		t.Fatal("cloudflare not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.cloudflare.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.cloudflare.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("cloudflare auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Asana(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("asana")
	if !ok {
		t.Fatal("asana not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.asana.com/v2/mcp" {
		t.Errorf("URL = %q, want https://mcp.asana.com/v2/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("asana auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("asana Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Atlassian(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("atlassian")
	if !ok {
		t.Fatal("atlassian not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.atlassian.com/v1/mcp" {
		t.Errorf("URL = %q, want https://mcp.atlassian.com/v1/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Atlassian uses a placeholder client_id (no DCR — must register Kameas OAuth app).
	if r.Auth.ClientID != "${KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID}" {
		t.Errorf("atlassian auth.client_id = %q, want ${KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("atlassian Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	// Must have the KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID env key so install modal can surface it.
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID should be required (no DCR — needs registered Kameas app)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestZapierRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("zapier")
	if !ok {
		t.Fatal("zapier not in registry")
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL == "" {
		t.Error("URL must be non-empty")
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("Auth.ClientID = %q, want empty (DCR zero-app — no baked client_id)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthOAuth {
		t.Errorf("PrimaryAuth = %q, want oauth", r.PrimaryAuth)
	}
}

func TestMakeRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("make")
	if !ok {
		t.Fatal("make not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.make.com/" {
		t.Errorf("URL = %q, want https://mcp.make.com/", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("Auth.ClientID = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
}

func TestPipedreamRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("pipedream")
	if !ok {
		t.Fatal("pipedream not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("Auth.ClientID = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	// "mcp" scope required for Pipedream Connect MCP.
	hasMCPScope := false
	for _, s := range r.Auth.Scopes {
		if s == "mcp" {
			hasMCPScope = true
		}
	}
	if !hasMCPScope {
		t.Errorf("Auth.Scopes = %v, must include \"mcp\"", r.Auth.Scopes)
	}
	// env_keys must include PIPEDREAM_PROJECT_ID.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["PIPEDREAM_PROJECT_ID"] {
		t.Errorf("EnvKeys = %+v, want PIPEDREAM_PROJECT_ID", r.EnvKeys)
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
}

func TestComposioRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("composio")
	if !ok {
		t.Fatal("composio not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// Auth block absent — no OAuth flow for this recipe.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Composio (API-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["COMPOSIO_SERVER_ID"] {
		t.Errorf("EnvKeys = %+v, want COMPOSIO_SERVER_ID", r.EnvKeys)
	}
	if !envNames["COMPOSIO_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want COMPOSIO_API_KEY", r.EnvKeys)
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
}

func TestN8nRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("n8n")
	if !ok {
		t.Fatal("n8n not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// No OAuth flow — bearer token via headers_template.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for n8n (bearer token auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["N8N_INSTANCE_HOST"] {
		t.Errorf("EnvKeys = %+v, want N8N_INSTANCE_HOST", r.EnvKeys)
	}
	if !envNames["N8N_MCP_ACCESS_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want N8N_MCP_ACCESS_TOKEN", r.EnvKeys)
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
}

func TestWorkatoRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("workato")
	if !ok {
		t.Fatal("workato not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// No OAuth flow — bearer token via headers_template.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Workato (bearer token auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["WORKATO_MCP_PATH"] {
		t.Errorf("EnvKeys = %+v, want WORKATO_MCP_PATH", r.EnvKeys)
	}
	if !envNames["WORKATO_MCP_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want WORKATO_MCP_TOKEN", r.EnvKeys)
	}
	if r.Warning == "" {
		t.Error("Workato recipe should carry a Warning (enterprise-only, admin setup required)")
	}
	if r.Category != "automation" {
		t.Errorf("Category = %q, want automation", r.Category)
	}
}

func TestAutomationCategoryGroup(t *testing.T) {
	cat := recipes.Registry()
	iPaaSIDs := []string{"zapier", "make", "pipedream", "composio", "n8n", "workato"}
	for _, id := range iPaaSIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("iPaaS recipe %q not found in registry", id)
			continue
		}
		if r.Category != "automation" {
			t.Errorf("recipe %q: Category = %q, want automation", id, r.Category)
		}
	}
}

func TestRegistrySharePointRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("sharepoint")
	if !ok {
		t.Fatal("sharepoint not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthDeviceCode {
		t.Errorf("PrimaryAuth = %q, want device_code", r.PrimaryAuth)
	}
	// Command must contain both --org-mode and --preset work.
	hasOrgMode := false
	hasPresetWork := false
	for i, arg := range r.Command {
		if arg == "--org-mode" {
			hasOrgMode = true
		}
		if arg == "--preset" && i+1 < len(r.Command) && r.Command[i+1] == "work" {
			hasPresetWork = true
		}
	}
	if !hasOrgMode {
		t.Errorf("Command = %v, must contain --org-mode", r.Command)
	}
	if !hasPresetWork {
		t.Errorf("Command = %v, must contain --preset work", r.Command)
	}
	// Server-bundled app: no required env keys.
	for _, e := range r.EnvKeys {
		if e.Required {
			t.Errorf("env key %q should be optional (server-bundled app)", e.Name)
		}
	}
	if r.Warning == "" {
		t.Error("sharepoint should carry a Warning (work/school account + tenant ID required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRegistryTeamsRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("teams")
	if !ok {
		t.Fatal("teams not in registry")
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthDeviceCode {
		t.Errorf("PrimaryAuth = %q, want device_code", r.PrimaryAuth)
	}
	// Command must contain both --org-mode and --preset teams.
	hasOrgMode := false
	hasPresetTeams := false
	for i, arg := range r.Command {
		if arg == "--org-mode" {
			hasOrgMode = true
		}
		if arg == "--preset" && i+1 < len(r.Command) && r.Command[i+1] == "teams" {
			hasPresetTeams = true
		}
	}
	if !hasOrgMode {
		t.Errorf("Command = %v, must contain --org-mode", r.Command)
	}
	if !hasPresetTeams {
		t.Errorf("Command = %v, must contain --preset teams", r.Command)
	}
	// Server-bundled app: no required env keys.
	for _, e := range r.EnvKeys {
		if e.Required {
			t.Errorf("env key %q should be optional (server-bundled app)", e.Name)
		}
	}
	if r.Warning == "" {
		t.Error("teams should carry a Warning (work/school account required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRegistryOneDriveRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("onedrive")
	if !ok {
		t.Fatal("onedrive not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthDeviceCode {
		t.Errorf("PrimaryAuth = %q, want device_code", r.PrimaryAuth)
	}
	if len(r.Command) < 5 || r.Command[3] != "--preset" || r.Command[4] != "onedrive" {
		t.Errorf("Command = %v, want --preset onedrive at positions [3][4]", r.Command)
	}
	// Server-bundled app: no required env keys.
	for _, e := range r.EnvKeys {
		if e.Required {
			t.Errorf("env key %q should be optional (server-bundled app, no Kameas app needed)", e.Name)
		}
	}
	if r.Warning == "" {
		t.Error("onedrive should carry a Warning (personal account refresh token caveat)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Redis(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("redis")
	if !ok {
		t.Fatal("redis not in registry")
	}
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (empty)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command = %v, want uvx-based command", r.Command)
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "REDIS_URL" {
		t.Fatalf("EnvKeys = %+v, want one entry named REDIS_URL", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("REDIS_URL should be required")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("redis should carry a Warning (uvx/uv required, TLS note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_MongoDB(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("mongodb")
	if !ok {
		t.Fatal("mongodb not in registry")
	}
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (empty)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] != "npx" {
		t.Errorf("Command = %v, want npx-based command", r.Command)
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	envNames := map[string]bool{}
	requiredCount := 0
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
		if e.Required {
			requiredCount++
		}
	}
	if !envNames["MDB_MCP_CONNECTION_STRING"] {
		t.Error("EnvKeys missing required MDB_MCP_CONNECTION_STRING")
	}
	// Connection string must be required.
	for _, e := range r.EnvKeys {
		if e.Name == "MDB_MCP_CONNECTION_STRING" && !e.Required {
			t.Error("MDB_MCP_CONNECTION_STRING should be required")
		}
	}
	if !envNames["MDB_MCP_READ_ONLY"] {
		t.Error("EnvKeys missing optional MDB_MCP_READ_ONLY")
	}
	if !envNames["MDB_MCP_API_CLIENT_ID"] || !envNames["MDB_MCP_API_CLIENT_SECRET"] {
		t.Error("EnvKeys missing optional Atlas credentials (MDB_MCP_API_CLIENT_ID / MDB_MCP_API_CLIENT_SECRET)")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("mongodb should carry a Warning (Atlas management cost implications)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_MySQL(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("mysql")
	if !ok {
		t.Fatal("mysql not in registry")
	}
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (empty)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] != "npx" {
		t.Errorf("Command = %v, want npx-based command", r.Command)
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	for _, required := range []string{"MYSQL_HOST", "MYSQL_USER", "MYSQL_PASS", "MYSQL_DB"} {
		if !envNames[required] {
			t.Errorf("EnvKeys missing required key %q", required)
		}
	}
	if !envNames["MYSQL_PORT"] {
		t.Error("EnvKeys missing optional MYSQL_PORT")
	}
	if !envNames["ALLOW_INSERT_OPERATION"] || !envNames["ALLOW_UPDATE_OPERATION"] || !envNames["ALLOW_DELETE_OPERATION"] {
		t.Error("EnvKeys missing write-enable optional flags (ALLOW_INSERT/UPDATE/DELETE_OPERATION)")
	}
	// Write flags must be optional.
	for _, e := range r.EnvKeys {
		if (e.Name == "ALLOW_INSERT_OPERATION" || e.Name == "ALLOW_UPDATE_OPERATION" || e.Name == "ALLOW_DELETE_OPERATION") && e.Required {
			t.Errorf("env key %q should be optional (write flag)", e.Name)
		}
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("mysql should carry a Warning (community server, write ops disabled by default)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_GoogleMaps(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("google-maps")
	if !ok {
		t.Fatal("google-maps not in registry")
	}
	// stdio recipe — no transport field, command must be set.
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (empty)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] != "npx" {
		t.Errorf("Command = %v, want npx-based command", r.Command)
	}
	if r.Category != "web" {
		t.Errorf("Category = %q, want web", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "GOOGLE_MAPS_API_KEY" {
		t.Fatalf("EnvKeys = %+v, want one entry named GOOGLE_MAPS_API_KEY", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("GOOGLE_MAPS_API_KEY should be required")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("google-maps should carry a Warning (npm fetch + API enablement)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_GitLab(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("gitlab")
	if !ok {
		t.Fatal("gitlab not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://gitlab.com/api/v4/mcp" {
		t.Errorf("URL = %q, want https://gitlab.com/api/v4/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("gitlab auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("gitlab Auth.Scopes should list requested scopes")
	}
	hasMCP := false
	for _, s := range r.Auth.Scopes {
		if s == "mcp" {
			hasMCP = true
		}
	}
	if !hasMCP {
		t.Errorf("Auth.Scopes = %v, must include \"mcp\"", r.Auth.Scopes)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	// PAT fallback env key present but optional.
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "GITLAB_PERSONAL_ACCESS_TOKEN" {
		t.Fatalf("EnvKeys = %+v, want one entry named GITLAB_PERSONAL_ACCESS_TOKEN", r.EnvKeys)
	}
	if r.EnvKeys[0].Required {
		t.Error("GITLAB_PERSONAL_ACCESS_TOKEN should be optional (DCR OAuth is the primary path)")
	}
	if r.Warning == "" {
		t.Error("gitlab should carry a Warning (gitlab.com only, self-managed caveat)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Supabase(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("supabase")
	if !ok {
		t.Fatal("supabase not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.supabase.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.supabase.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("supabase auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	// PAT fallback env key present but optional.
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "SUPABASE_ACCESS_TOKEN" {
		t.Fatalf("EnvKeys = %+v, want one entry named SUPABASE_ACCESS_TOKEN", r.EnvKeys)
	}
	if r.EnvKeys[0].Required {
		t.Error("SUPABASE_ACCESS_TOKEN should be optional (DCR OAuth is the primary path)")
	}
	if r.Warning == "" {
		t.Error("supabase should carry a Warning (dev/test only, all-or-nothing scopes)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestCommunicationCategoryGroup(t *testing.T) {
	// WP09: all communication connectors in this pack must use
	// category="communication" so the catalog UI groups them together.
	commIDs := []string{
		"slack", "slack-tokens", "gmail", "outlook",
		"intercom", "twilio", "zoom", "discord", "front", "webex", "ringcentral",
	}
	cat := recipes.Registry()
	for _, id := range commIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("communication recipe %q not found in registry", id)
			continue
		}
		if r.Category != "communication" {
			t.Errorf("recipe %q: Category = %q, want communication", id, r.Category)
		}
	}
}

func TestRecipe_RingCentral(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("ringcentral")
	if !ok {
		t.Fatal("ringcentral not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_RINGCENTRAL_OAUTH_CLIENT_ID}" {
		t.Errorf("auth.client_id = %q, want ${KAMEAS_RINGCENTRAL_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.Warning == "" {
		t.Error("ringcentral should carry a Warning (URL unverified at plan time)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Webex(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("webex")
	if !ok {
		t.Fatal("webex not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_WEBEX_OAUTH_CLIENT_ID}" {
		t.Errorf("auth.client_id = %q, want ${KAMEAS_WEBEX_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	// Must have read-only Webex spark scopes.
	scopeMap := map[string]bool{}
	for _, s := range r.Auth.Scopes {
		scopeMap[s] = true
	}
	if !scopeMap["spark:messages_read"] {
		t.Error("webex auth.scopes should include spark:messages_read")
	}
	if !scopeMap["spark:rooms_read"] {
		t.Error("webex auth.scopes should include spark:rooms_read")
	}
	if r.Warning == "" {
		t.Error("webex should carry a Warning (URL unverified at plan time)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Front(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("front")
	if !ok {
		t.Fatal("front not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.frontapp.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.frontapp.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_FRONT_OAUTH_CLIENT_ID}" {
		t.Errorf("auth.client_id = %q, want ${KAMEAS_FRONT_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.Auth.ClientSecret != "${KAMEAS_FRONT_OAUTH_CLIENT_SECRET}" {
		t.Errorf("auth.client_secret = %q, want ${KAMEAS_FRONT_OAUTH_CLIENT_SECRET}", r.Auth.ClientSecret)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	// Must have read scope (read-only default).
	hasRead := false
	for _, s := range r.Auth.Scopes {
		if s == "read" {
			hasRead = true
		}
	}
	if !hasRead {
		t.Error("front auth.scopes should include 'read' (read-only default)")
	}
	if r.Warning == "" {
		t.Error("front should carry a Warning (open beta + MCP-only feature flag)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Discord(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("discord")
	if !ok {
		t.Fatal("discord not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_DISCORD_OAUTH_CLIENT_ID}" {
		t.Errorf("auth.client_id = %q, want ${KAMEAS_DISCORD_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.Warning == "" {
		t.Error("discord should carry a Warning (URL unverified at plan time)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Zoom(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("zoom")
	if !ok {
		t.Fatal("zoom not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_ZOOM_OAUTH_CLIENT_ID}" {
		t.Errorf("auth.client_id = %q, want ${KAMEAS_ZOOM_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.Warning == "" {
		t.Error("zoom should carry a Warning (URL unverified at plan time)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Twilio(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("twilio")
	if !ok {
		t.Fatal("twilio not in registry")
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// stdio recipe: no transport/URL
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio or empty", r.Transport)
	}
	// Must have three required env keys.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
		if !e.Required {
			t.Errorf("env key %q should be required", e.Name)
		}
	}
	if !envNames["TWILIO_ACCOUNT_SID"] {
		t.Error("missing env key TWILIO_ACCOUNT_SID")
	}
	if !envNames["TWILIO_API_KEY"] {
		t.Error("missing env key TWILIO_API_KEY")
	}
	if !envNames["TWILIO_API_SECRET"] {
		t.Error("missing env key TWILIO_API_SECRET")
	}
	// Credentials must be delivered via the environment (env_keys), never on the
	// command line where they would be visible in process listings / crash logs.
	for _, arg := range r.Command {
		for _, secret := range []string{"TWILIO_ACCOUNT_SID", "TWILIO_API_KEY", "TWILIO_API_SECRET"} {
			if strings.Contains(arg, secret) {
				t.Errorf("command must not interpolate %s into argv (leaks via ps); rely on env_keys", secret)
			}
		}
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Intercom(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("intercom")
	if !ok {
		t.Fatal("intercom not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.intercom.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.intercom.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("intercom auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "communication" {
		t.Errorf("Category = %q, want communication", r.Category)
	}
	if r.Warning == "" {
		t.Error("intercom should carry a Warning (US-hosted workspaces only)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_1PasswordDeferred(t *testing.T) {
	cat := recipes.Registry()
	if _, ok := cat.Get("1password"); ok {
		t.Error("1password recipe should not be in registry (deferred — macOS beta, Environments-only, no vault access)")
	}
	if _, ok := cat.Get("onepassword"); ok {
		t.Error("onepassword recipe should not be in registry (deferred)")
	}
}

func TestRecipe_JamfDocs(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("jamf-docs")
	if !ok {
		t.Fatal("jamf-docs not in registry")
	}
	// Remote HTTP — docs search, no auth.
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://developer.jamf.com/mcp" {
		t.Errorf("URL = %q, want https://developer.jamf.com/mcp", r.URL)
	}
	// No auth object — docs MCP is public.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for no-auth HTTP MCP, got %+v", r.Auth)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthNone {
		t.Errorf("PrimaryAuth = %q, want none", r.PrimaryAuth)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("EnvKeys should be empty for no-auth recipe, got %+v", r.EnvKeys)
	}
	if r.Category != "support" {
		t.Errorf("Category = %q, want support", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_ServiceNow(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("servicenow")
	if !ok {
		t.Fatal("servicenow not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("ServiceNow recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("ServiceNow is stdio — URL should be empty, got %q", r.URL)
	}
	if r.Category != "support" {
		t.Errorf("Category = %q, want support", r.Category)
	}
	// SERVICENOW_INSTANCE_URL must be present (per-tenant required config).
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SERVICENOW_INSTANCE_URL"] {
		t.Errorf("EnvKeys = %+v, want SERVICENOW_INSTANCE_URL", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Zendesk(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("zendesk")
	if !ok {
		t.Fatal("zendesk not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Zendesk recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("Zendesk is stdio — URL should be empty, got %q", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Category != "support" {
		t.Errorf("Category = %q, want support", r.Category)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["ZENDESK_SUBDOMAIN"] {
		t.Errorf("EnvKeys = %+v, want ZENDESK_SUBDOMAIN", r.EnvKeys)
	}
	if !envNames["ZENDESK_EMAIL"] {
		t.Errorf("EnvKeys = %+v, want ZENDESK_EMAIL", r.EnvKeys)
	}
	if !envNames["ZENDESK_API_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want ZENDESK_API_TOKEN", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Okta(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("okta")
	if !ok {
		t.Fatal("okta not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Okta recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("Okta is stdio — URL should be empty, got %q", r.URL)
	}
	// Security connector: device_code primary auth, read-only scopes by default.
	if r.PrimaryAuth != recipes.PrimaryAuthDeviceCode {
		t.Errorf("PrimaryAuth = %q, want device_code", r.PrimaryAuth)
	}
	if r.Category != "security" {
		t.Errorf("Category = %q, want security", r.Category)
	}
	if r.SamplingPolicy.Allowed {
		t.Error("security recipe sampling_policy.allowed must be false")
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["OKTA_ORG_URL"] {
		t.Errorf("EnvKeys = %+v, want OKTA_ORG_URL", r.EnvKeys)
	}
	if !envNames["OKTA_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want OKTA_CLIENT_ID", r.EnvKeys)
	}
	if !envNames["OKTA_SCOPES"] {
		t.Errorf("EnvKeys = %+v, want OKTA_SCOPES (read-only scope default)", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_HelpScout(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("help-scout")
	if !ok {
		t.Fatal("help-scout not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Help Scout recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("Help Scout is stdio — URL should be empty, got %q", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Category != "support" {
		t.Errorf("Category = %q, want support", r.Category)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["HELPSCOUT_APP_ID"] {
		t.Errorf("EnvKeys = %+v, want HELPSCOUT_APP_ID", r.EnvKeys)
	}
	if !envNames["HELPSCOUT_APP_SECRET"] {
		t.Errorf("EnvKeys = %+v, want HELPSCOUT_APP_SECRET", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_CrowdStrikeAIDR(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("crowdstrike-aidr")
	if !ok {
		t.Fatal("crowdstrike-aidr not in registry")
	}
	// stdio recipe — command present, no URL.
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("CrowdStrike AIDR recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("CrowdStrike AIDR is stdio — URL should be empty, got %q", r.URL)
	}
	// Security connector: read/query-only, no sampling.
	if r.Category != "security" {
		t.Errorf("Category = %q, want security", r.Category)
	}
	if r.SamplingPolicy.Allowed {
		t.Error("security recipe sampling_policy.allowed must be false")
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["CS_AIDR_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want CS_AIDR_TOKEN", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Freshdesk(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("freshdesk")
	if !ok {
		t.Fatal("freshdesk not in registry")
	}
	// stdio recipe — command present, no URL.
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Freshdesk recipe must have a non-empty Command")
	}
	if r.URL != "" {
		t.Errorf("Freshdesk is stdio — URL should be empty, got %q", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Category != "support" {
		t.Errorf("Category = %q, want support", r.Category)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["FRESHDESK_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want FRESHDESK_API_KEY", r.EnvKeys)
	}
	if !envNames["FRESHDESK_DOMAIN"] {
		t.Errorf("EnvKeys = %+v, want FRESHDESK_DOMAIN", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Mercury(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("mercury")
	if !ok {
		t.Fatal("mercury not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.mercury.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.mercury.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no pre-registered client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("mercury auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	wantScopes := map[string]bool{"read": true, "offline_access": true}
	for _, s := range r.Auth.Scopes {
		if !wantScopes[s] {
			t.Errorf("mercury Auth.Scopes contains unexpected scope %q", s)
		}
	}
	if len(r.Auth.Scopes) != 2 {
		t.Errorf("mercury Auth.Scopes = %v, want [read offline_access]", r.Auth.Scopes)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	// No owner app registration needed (zero-app DCR).
	for _, k := range r.EnvKeys {
		if k.Name == "KAMEAS_MERCURY_OAUTH_CLIENT_ID" {
			t.Error("mercury should not have KAMEAS_MERCURY_OAUTH_CLIENT_ID env key (DCR zero-app)")
		}
	}
	// Finance safety copy.
	if !strings.Contains(r.Description, "read-only") {
		t.Error("mercury description must include read-only safety copy")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Ramp(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("ramp")
	if !ok {
		t.Fatal("ramp not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.ramp.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.ramp.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no pre-registered client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("ramp auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	// FR-003: CRITICAL — enumerate every forbidden write/money-movement scope.
	forbiddenRampScopes := []string{
		"bank_accounts:write",
		"banking_drawdown_requests:write",
		"transactions:write",
		"limits:write",
		"x402:write",
		"trips:write",
	}
	presentScopes := map[string]bool{}
	for _, s := range r.Auth.Scopes {
		presentScopes[s] = true
	}
	for _, forbidden := range forbiddenRampScopes {
		if presentScopes[forbidden] {
			t.Errorf("ramp Auth.Scopes must not include write/money-movement scope %q", forbidden)
		}
	}
	// Finance safety copy.
	if !strings.Contains(r.Description, "read-only") {
		t.Error("ramp description must include read-only safety copy")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Deel(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("deel")
	if !ok {
		t.Fatal("deel not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://api.letsdeel.com/mcp" {
		t.Errorf("URL = %q, want https://api.letsdeel.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no pre-registered client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("deel auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "hr_people" {
		t.Errorf("Category = %q, want hr_people", r.Category)
	}
	// FR-003: no write or money-movement scopes.
	forbiddenDeelScopes := []string{
		"contracts:write",
		"timesheets:write",
		"invoice-adjustments:write",
		"adjustments:write",
		"time-off:write",
		"invoice:create",
		"treasury-vendorbill:write",
		"auth:write",
		"ai:write",
		"profile:write",
	}
	presentScopes := map[string]bool{}
	for _, s := range r.Auth.Scopes {
		presentScopes[s] = true
	}
	for _, forbidden := range forbiddenDeelScopes {
		if presentScopes[forbidden] {
			t.Errorf("deel Auth.Scopes must not include write scope %q", forbidden)
		}
	}
	// No KAMEAS_DEEL_OAUTH_CLIENT_ID (zero-app).
	for _, k := range r.EnvKeys {
		if k.Name == "KAMEAS_DEEL_OAUTH_CLIENT_ID" {
			t.Error("deel should not have KAMEAS_DEEL_OAUTH_CLIENT_ID env key (DCR zero-app)")
		}
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Greenhouse(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("greenhouse")
	if !ok {
		t.Fatal("greenhouse not in registry")
	}
	if r.Category != "hr_people" {
		t.Errorf("Category = %q, want hr_people", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// Must have GREENHOUSE_API_KEY env key.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["GREENHOUSE_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want GREENHOUSE_API_KEY", r.EnvKeys)
	}
	// Must be required.
	for _, e := range r.EnvKeys {
		if e.Name == "GREENHOUSE_API_KEY" && !e.Required {
			t.Error("GREENHOUSE_API_KEY should be required")
		}
	}
	// stdio — no OAuth auth block.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Greenhouse (API key auth), got %+v", r.Auth)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Ashby(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("ashby")
	if !ok {
		t.Fatal("ashby not in registry")
	}
	if r.Category != "hr_people" {
		t.Errorf("Category = %q, want hr_people", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["ASHBY_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want ASHBY_API_KEY", r.EnvKeys)
	}
	for _, e := range r.EnvKeys {
		if e.Name == "ASHBY_API_KEY" && !e.Required {
			t.Error("ASHBY_API_KEY should be required")
		}
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Ashby (API key auth), got %+v", r.Auth)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Xero(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("xero")
	if !ok {
		t.Fatal("xero not in registry")
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["XERO_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want XERO_CLIENT_ID", r.EnvKeys)
	}
	if !envNames["XERO_CLIENT_SECRET"] {
		t.Errorf("EnvKeys = %+v, want XERO_CLIENT_SECRET", r.EnvKeys)
	}
	for _, e := range r.EnvKeys {
		switch e.Name {
		case "XERO_CLIENT_ID", "XERO_CLIENT_SECRET":
			if !e.Required {
				t.Errorf("%q should be required", e.Name)
			}
		}
	}
	// Finance safety copy.
	if !strings.Contains(r.Description, "read-only") {
		t.Error("xero description must include read-only safety copy")
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Xero (client_id/secret-based OAuth, not mcp_oauth flow), got %+v", r.Auth)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_QuickBooks(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("quickbooks")
	if !ok {
		t.Fatal("quickbooks not in registry")
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["QUICKBOOKS_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want QUICKBOOKS_CLIENT_ID (Kameas OAuth seam)", r.EnvKeys)
	}
	if !envNames["QUICKBOOKS_CLIENT_SECRET"] {
		t.Errorf("EnvKeys = %+v, want QUICKBOOKS_CLIENT_SECRET", r.EnvKeys)
	}
	// Finance safety copy.
	if !strings.Contains(r.Description, "read-only") {
		t.Error("quickbooks description must include read-only safety copy")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Brex(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("brex")
	if !ok {
		t.Fatal("brex not in registry")
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["BREX_API_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want BREX_API_TOKEN", r.EnvKeys)
	}
	// No write/money-movement scopes (this is an API-token recipe, no scopes in auth block).
	if r.Auth != nil {
		for _, s := range r.Auth.Scopes {
			if strings.Contains(s, "write") || strings.Contains(s, "transfer") || strings.Contains(s, "payment") {
				t.Errorf("brex Auth.Scopes must not include write/money-movement scope %q", s)
			}
		}
	}
	// Finance safety copy.
	if !strings.Contains(r.Description, "read-only") {
		t.Error("brex description must include read-only safety copy")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Rippling(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("rippling")
	if !ok {
		t.Fatal("rippling not in registry")
	}
	if r.Category != "hr_people" {
		t.Errorf("Category = %q, want hr_people", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["RIPPLING_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want RIPPLING_CLIENT_ID (Kameas OAuth seam)", r.EnvKeys)
	}
	if !envNames["RIPPLING_CLIENT_SECRET"] {
		t.Errorf("EnvKeys = %+v, want RIPPLING_CLIENT_SECRET", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_NetSuite(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("netsuite")
	if !ok {
		t.Fatal("netsuite not in registry")
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	// Must have netsuite_account_id config option (per-account URL config required).
	hasAccountID := false
	for _, opt := range r.ConfigOptions {
		if opt.Name == "netsuite_account_id" {
			hasAccountID = true
			if !opt.Required {
				t.Error("netsuite_account_id config option should be required")
			}
		}
	}
	if !hasAccountID {
		t.Error("netsuite should have netsuite_account_id config option")
	}
	// Description should reference enterprise/iPaaS alternative.
	if !strings.Contains(strings.ToLower(r.Description), "enterprise") {
		t.Error("netsuite description should note enterprise setup requirement")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Workday(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("workday")
	if !ok {
		t.Fatal("workday not in registry")
	}
	if r.Category != "hr_people" {
		t.Errorf("Category = %q, want hr_people", r.Category)
	}
	// Must have workday_tenant_url config option (per-tenant URL required).
	hasTenantURL := false
	for _, opt := range r.ConfigOptions {
		if opt.Name == "workday_tenant_url" {
			hasTenantURL = true
			if !opt.Required {
				t.Error("workday_tenant_url config option should be required")
			}
		}
	}
	if !hasTenantURL {
		t.Error("workday should have workday_tenant_url config option")
	}
	// Description should reference enterprise/iPaaS alternative.
	if !strings.Contains(strings.ToLower(r.Description), "enterprise") {
		t.Error("workday description should note enterprise setup requirement")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestHRFinanceCategoryGroups(t *testing.T) {
	cat := recipes.Registry()

	hrPeopleIDs := []string{"deel", "greenhouse", "ashby", "rippling", "workday"}
	for _, id := range hrPeopleIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("hr_people connector %q not found in registry", id)
			continue
		}
		if r.Category != "hr_people" {
			t.Errorf("recipe %q: Category = %q, want hr_people", id, r.Category)
		}
	}

	financeIDs := []string{"mercury", "ramp", "brex", "quickbooks", "xero", "netsuite"}
	for _, id := range financeIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("finance_accounting connector %q not found in registry", id)
			continue
		}
		if r.Category != "finance" {
			t.Errorf("recipe %q: Category = %q, want finance", id, r.Category)
		}
		// FR-003: finance connector descriptions must include read-only safety copy.
		if !strings.Contains(r.Description, "read-only") {
			t.Errorf("finance recipe %q description must include read-only safety copy, got: %q", id, r.Description)
		}
	}
}

func TestObservabilityCategoryGroup(t *testing.T) {
	cat := recipes.Registry()
	observabilityIDs := []string{"grafana", "datadog", "new-relic", "pagerduty"}
	for _, id := range observabilityIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("observability recipe %q not found in registry", id)
			continue
		}
		if r.Category != "observability" {
			t.Errorf("recipe %q: Category = %q, want observability", id, r.Category)
		}
	}
}

func TestSecurityCategoryGroup(t *testing.T) {
	cat := recipes.Registry()
	securityIDs := []string{"snyk", "sonar"}
	for _, id := range securityIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("security recipe %q not found in registry", id)
			continue
		}
		if r.Category != "security" {
			t.Errorf("recipe %q: Category = %q, want security", id, r.Category)
		}
	}
}

func TestRecipe_Jenkins(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("jenkins")
	if !ok {
		t.Fatal("jenkins not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	// URL uses a host token — per-instance host supplied by user via env var.
	if r.URL != "https://${JENKINS_INSTANCE_HOST}/mcp-server/mcp" {
		t.Errorf("URL = %q, want https://${JENKINS_INSTANCE_HOST}/mcp-server/mcp", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["JENKINS_INSTANCE_HOST"] {
		t.Errorf("EnvKeys = %+v, want JENKINS_INSTANCE_HOST", r.EnvKeys)
	}
	if !envNames["JENKINS_BASIC_AUTH"] {
		t.Errorf("EnvKeys = %+v, want JENKINS_BASIC_AUTH", r.EnvKeys)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if r.Warning == "" {
		t.Error("jenkins should carry a Warning (plugin required / self-hosted)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Sonar(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("sonar")
	if !ok {
		t.Fatal("sonar not in registry")
	}
	// Stdio transport (Docker).
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (or empty)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] != "docker" {
		t.Errorf("Command = %v, want docker as first element", r.Command)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SONARQUBE_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want SONARQUBE_TOKEN", r.EnvKeys)
	}
	// Must have sonar_url config_option.
	optNames := map[string]bool{}
	for _, o := range r.ConfigOptions {
		optNames[o.Name] = true
	}
	if !optNames["sonar_url"] {
		t.Errorf("ConfigOptions = %+v, want sonar_url", r.ConfigOptions)
	}
	if r.Category != "security" {
		t.Errorf("Category = %q, want security", r.Category)
	}
	if r.Warning == "" {
		t.Error("sonar should carry a Warning (Docker required / token type note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Snyk(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("snyk")
	if !ok {
		t.Fatal("snyk not in registry")
	}
	// Stdio transport (npx).
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (or empty)", r.Transport)
	}
	if len(r.Command) < 3 || r.Command[0] != "npx" {
		t.Errorf("Command = %v, want [npx -y snyk mcp ...]", r.Command)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SNYK_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want SNYK_TOKEN", r.EnvKeys)
	}
	for _, e := range r.EnvKeys {
		if e.Name == "SNYK_TOKEN" && !e.Required {
			t.Error("SNYK_TOKEN should be required")
		}
	}
	if r.Category != "security" {
		t.Errorf("Category = %q, want security", r.Category)
	}
	if r.Warning == "" {
		t.Error("snyk should carry a Warning (npm download / Node.js required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_PagerDuty(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("pagerduty")
	if !ok {
		t.Fatal("pagerduty not in registry")
	}
	// Stdio transport (uvx).
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio (or empty)", r.Transport)
	}
	if len(r.Command) < 2 || r.Command[0] != "uvx" || r.Command[1] != "pagerduty-mcp" {
		t.Errorf("Command = %v, want [uvx pagerduty-mcp]", r.Command)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["PAGERDUTY_USER_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want PAGERDUTY_USER_API_KEY", r.EnvKeys)
	}
	for _, e := range r.EnvKeys {
		if e.Name == "PAGERDUTY_USER_API_KEY" && !e.Required {
			t.Error("PAGERDUTY_USER_API_KEY should be required")
		}
	}
	if r.Category != "observability" {
		t.Errorf("Category = %q, want observability", r.Category)
	}
	if r.Warning == "" {
		t.Error("pagerduty should carry a Warning (uvx download / read-only note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Bitbucket(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("bitbucket")
	if !ok {
		t.Fatal("bitbucket not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.atlassian.com/v1/mcp" {
		t.Errorf("URL = %q, want https://mcp.atlassian.com/v1/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Bitbucket uses the Atlassian OAuth app (no DCR on auth.atlassian.com).
	if r.Auth.ClientID != "${KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID}" {
		t.Errorf("bitbucket auth.client_id = %q, want ${KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("bitbucket Auth.Scopes should list Bitbucket-specific scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_ATLASSIAN_OAUTH_CLIENT_ID should be required")
	}
	if r.Warning == "" {
		t.Error("bitbucket should carry a Warning (Atlassian app required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Vercel(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("vercel")
	if !ok {
		t.Fatal("vercel not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.vercel.com" {
		t.Errorf("URL = %q, want https://mcp.vercel.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Vercel gates DCR: uses PKCE with a Kameas-registered app (placeholder seam).
	if r.Auth.ClientID != "${KAMEAS_VERCEL_OAUTH_CLIENT_ID}" {
		t.Errorf("vercel auth.client_id = %q, want ${KAMEAS_VERCEL_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_VERCEL_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_VERCEL_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_VERCEL_OAUTH_CLIENT_ID should be required")
	}
	if r.Warning == "" {
		t.Error("vercel should carry a Warning (DCR gated / Kameas app required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Netlify(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("netlify")
	if !ok {
		t.Fatal("netlify not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://netlify-mcp.netlify.app/mcp" {
		t.Errorf("URL = %q, want https://netlify-mcp.netlify.app/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("netlify auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("netlify Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if r.Warning == "" {
		t.Error("netlify should carry a Warning (hosting note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_CircleCI(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("circleci")
	if !ok {
		t.Fatal("circleci not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.circleci.com" {
		t.Errorf("URL = %q, want https://mcp.circleci.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("circleci auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "developer" {
		t.Errorf("Category = %q, want developer", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_NewRelic(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("new-relic")
	if !ok {
		t.Fatal("new-relic not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.newrelic.com/mcp/" {
		t.Errorf("URL = %q, want https://mcp.newrelic.com/mcp/", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("new-relic auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("new-relic Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "observability" {
		t.Errorf("Category = %q, want observability", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Datadog(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("datadog")
	if !ok {
		t.Fatal("datadog not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.datadoghq.com/v1/mcp" {
		t.Errorf("URL = %q, want https://mcp.datadoghq.com/v1/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("datadog auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "observability" {
		t.Errorf("Category = %q, want observability", r.Category)
	}
	// DD_API_KEY and DD_APPLICATION_KEY are optional fallback keys.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["DD_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want DD_API_KEY", r.EnvKeys)
	}
	if r.Warning == "" {
		t.Error("datadog should carry a Warning (rate limits / GovCloud not supported)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Grafana(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("grafana")
	if !ok {
		t.Fatal("grafana not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.grafana.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.grafana.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("grafana auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("grafana Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "observability" {
		t.Errorf("Category = %q, want observability", r.Category)
	}
	if r.Warning == "" {
		t.Error("grafana should carry a Warning (Grafana Cloud only)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRegistryGoogleCalendarRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("google-calendar")
	if !ok {
		t.Fatal("google-calendar not in registry")
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	// stdio recipe — no transport field (empty string = stdio)
	if r.Transport != "" {
		t.Errorf("Transport = %q, want empty (stdio)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Command must be non-empty")
	}
	if r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthOAuth {
		t.Errorf("PrimaryAuth = %q, want oauth", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("google-calendar should carry a Warning (one-time OAuth setup, token expiry in test-mode)")
	}
	// Must carry KAMEAS_GOOGLE_OAUTH_CLIENT_ID env key as placeholder seam (not required).
	foundPlaceholder := false
	for _, k := range r.EnvKeys {
		if k.Name == "KAMEAS_GOOGLE_OAUTH_CLIENT_ID" {
			foundPlaceholder = true
			if k.Required {
				t.Error("KAMEAS_GOOGLE_OAUTH_CLIENT_ID should not be required (Kameas-provided; guided-setup path)")
			}
		}
	}
	if !foundPlaceholder {
		t.Error("google-calendar missing KAMEAS_GOOGLE_OAUTH_CLIENT_ID env_key placeholder seam")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRegistryGoogleDriveRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("google-drive")
	if !ok {
		t.Fatal("google-drive not in registry")
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	// stdio recipe — no transport field (empty string = stdio)
	if r.Transport != "" {
		t.Errorf("Transport = %q, want empty (stdio)", r.Transport)
	}
	if len(r.Command) == 0 || r.Command[0] == "" {
		t.Error("Command must be non-empty")
	}
	if r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthOAuth {
		t.Errorf("PrimaryAuth = %q, want oauth", r.PrimaryAuth)
	}
	if r.Warning == "" {
		t.Error("google-drive should carry a Warning (OAuth setup, scope limitation, CASA upgrade path)")
	}
	// Must carry KAMEAS_GOOGLE_OAUTH_CLIENT_ID env key as placeholder seam (not required).
	foundPlaceholder := false
	for _, k := range r.EnvKeys {
		if k.Name == "KAMEAS_GOOGLE_OAUTH_CLIENT_ID" {
			foundPlaceholder = true
			if k.Required {
				t.Error("KAMEAS_GOOGLE_OAUTH_CLIENT_ID should not be required (Kameas-provided; guided-setup path)")
			}
		}
	}
	if !foundPlaceholder {
		t.Error("google-drive missing KAMEAS_GOOGLE_OAUTH_CLIENT_ID env_key placeholder seam")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Coda(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("coda")
	if !ok {
		t.Fatal("coda not in registry")
	}
	// stdio recipe: no transport/url, has command.
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio or empty", r.Transport)
	}
	if len(r.Command) == 0 {
		t.Fatal("coda Command must be non-empty")
	}
	if r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// Auth is nil — bearer API token, no OAuth 2.0 server.
	if r.Auth != nil {
		t.Errorf("coda Auth should be nil (bearer API token, not OAuth 2.0), got %+v", r.Auth)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "CODA_API_TOKEN" {
		t.Errorf("EnvKeys = %+v, want one entry CODA_API_TOKEN", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("CODA_API_TOKEN should be required")
	}
	if r.Warning == "" {
		t.Error("coda should carry a Warning (rebrand note + community package)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Trello(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("trello")
	if !ok {
		t.Fatal("trello not in registry")
	}
	// stdio recipe: no transport/url, has command.
	if r.Transport != "" && r.Transport != recipes.TransportStdio {
		t.Errorf("Transport = %q, want stdio or empty", r.Transport)
	}
	if len(r.Command) == 0 {
		t.Fatal("trello Command must be non-empty")
	}
	if r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	// Auth is nil — no OAuth 2.0 server.
	if r.Auth != nil {
		t.Errorf("trello Auth should be nil (API key + OAuth 1.0 token, not OAuth 2.0), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = e.Required
	}
	if !envNames["TRELLO_API_KEY"] {
		t.Error("TRELLO_API_KEY should be present and required")
	}
	if _, ok := envNames["TRELLO_TOKEN"]; !ok {
		t.Error("TRELLO_TOKEN should be present")
	}
	if !envNames["TRELLO_TOKEN"] {
		t.Error("TRELLO_TOKEN should be required")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Wrike(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("wrike")
	if !ok {
		t.Fatal("wrike not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.wrike.com/app/mcp/stream" {
		t.Errorf("URL = %q, want https://mcp.wrike.com/app/mcp/stream", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Kameas app: placeholder client_id seam required.
	if r.Auth.ClientID != "${KAMEAS_WRIKE_OAUTH_CLIENT_ID}" {
		t.Errorf("wrike auth.client_id = %q, want ${KAMEAS_WRIKE_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	// Must expose KAMEAS_WRIKE_OAUTH_CLIENT_ID as required + WRIKE_PERMANENT_TOKEN as optional.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = e.Required
	}
	if _, ok := envNames["KAMEAS_WRIKE_OAUTH_CLIENT_ID"]; !ok {
		t.Error("wrike should have KAMEAS_WRIKE_OAUTH_CLIENT_ID env_key")
	}
	if !envNames["KAMEAS_WRIKE_OAUTH_CLIENT_ID"] {
		t.Error("KAMEAS_WRIKE_OAUTH_CLIENT_ID should be required")
	}
	if _, ok := envNames["WRIKE_PERMANENT_TOKEN"]; !ok {
		t.Error("wrike should have WRIKE_PERMANENT_TOKEN env_key (optional PAT fallback)")
	}
	if envNames["WRIKE_PERMANENT_TOKEN"] {
		t.Error("WRIKE_PERMANENT_TOKEN should be optional (fallback only)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Smartsheet(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("smartsheet")
	if !ok {
		t.Fatal("smartsheet not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.smartsheet.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.smartsheet.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Kameas app: placeholder client_id seam required.
	if r.Auth.ClientID != "${KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID}" {
		t.Errorf("smartsheet auth.client_id = %q, want ${KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID}", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("smartsheet Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	// Must expose KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID as required env_key.
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_SMARTSHEET_OAUTH_CLIENT_ID should be required (no DCR — needs registered Kameas app)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Shortcut(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("shortcut")
	if !ok {
		t.Fatal("shortcut not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.shortcut.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.shortcut.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("shortcut auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("shortcut Auth.Scopes should list requested scopes")
	}
	hasRead := false
	for _, s := range r.Auth.Scopes {
		if s == "read" {
			hasRead = true
		}
	}
	if !hasRead {
		t.Errorf("shortcut Auth.Scopes = %v, must include \"read\"", r.Auth.Scopes)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("shortcut should have no env_keys (DCR zero-app), got %d", len(r.EnvKeys))
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Monday(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("monday")
	if !ok {
		t.Fatal("monday not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.monday.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.monday.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("monday auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	// Optional API token fallback env key must be present and not required.
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "MONDAY_API_TOKEN" {
		t.Errorf("EnvKeys = %+v, want one entry MONDAY_API_TOKEN", r.EnvKeys)
	}
	if r.EnvKeys[0].Required {
		t.Error("MONDAY_API_TOKEN should be optional (fallback only)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_ClickUp(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("clickup")
	if !ok {
		t.Fatal("clickup not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.clickup.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.clickup.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("clickup auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("clickup Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("clickup should have no env_keys (DCR zero-app), got %d", len(r.EnvKeys))
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestProductivityCategoryGroup_Pack06b(t *testing.T) {
	pack06bIDs := []string{
		"clickup",
		"monday",
		"shortcut",
		"smartsheet",
		"wrike",
		"trello",
		"coda",
	}
	cat := recipes.Registry()
	for _, id := range pack06bIDs {
		r, ok := cat.Get(id)
		if !ok {
			t.Errorf("pack-06b recipe %q not found in registry", id)
			continue
		}
		if r.Category != "productivity" {
			t.Errorf("recipe %q: Category = %q, want productivity", id, r.Category)
		}
	}
}

func TestRecipe_Mixpanel(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("mixpanel")
	if !ok {
		t.Fatal("mixpanel not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.mixpanel.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.mixpanel.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("mixpanel auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("mixpanel Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("mixpanel should have no env keys (DCR zero-app), got %d", len(r.EnvKeys))
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Amplitude(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("amplitude")
	if !ok {
		t.Fatal("amplitude not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.amplitude.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.amplitude.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("amplitude auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	hasMCPRead := false
	for _, s := range r.Auth.Scopes {
		if s == "mcp:read" {
			hasMCPRead = true
		}
	}
	if !hasMCPRead {
		t.Errorf("Auth.Scopes = %v, must include mcp:read", r.Auth.Scopes)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("amplitude should have no env keys (DCR zero-app), got %d", len(r.EnvKeys))
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Klaviyo(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("klaviyo")
	if !ok {
		t.Fatal("klaviyo not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.klaviyo.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.klaviyo.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("klaviyo auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	// Klaviyo intentionally omits scopes — server assigns based on private key permissions.
	if len(r.Auth.Scopes) != 0 {
		t.Errorf("klaviyo Auth.Scopes = %v, want empty (scopes assigned by server based on key permissions)", r.Auth.Scopes)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("klaviyo should have no env keys (DCR zero-app), got %d", len(r.EnvKeys))
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_DbtCloud(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("dbt-cloud")
	if !ok {
		t.Fatal("dbt-cloud not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL == "" {
		t.Error("dbt-cloud URL must be non-empty")
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("dbt-cloud auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	// Must have token fallback env key.
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["DBT_ACCESS_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want DBT_ACCESS_TOKEN (token fallback)", r.EnvKeys)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Tableau(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("tableau")
	if !ok {
		t.Fatal("tableau not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.tableau.com" {
		t.Errorf("URL = %q, want https://mcp.tableau.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Tableau requires Kameas owner-app — placeholder client_id.
	if r.Auth.ClientID != "${KAMEAS_TABLEAU_OAUTH_CLIENT_ID}" {
		t.Errorf("tableau auth.client_id = %q, want ${KAMEAS_TABLEAU_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("tableau Auth.Scopes should list read-only scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_TABLEAU_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_TABLEAU_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_TABLEAU_OAUTH_CLIENT_ID should be required (no DCR — needs registered Kameas Connected App)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Fivetran(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("fivetran")
	if !ok {
		t.Fatal("fivetran not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Fivetran (API-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["FIVETRAN_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want FIVETRAN_API_KEY", r.EnvKeys)
	}
	if !envNames["FIVETRAN_API_SECRET"] {
		t.Errorf("EnvKeys = %+v, want FIVETRAN_API_SECRET", r.EnvKeys)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if r.Warning == "" {
		t.Error("fivetran should carry a Warning (git install, read-only note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Redshift(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("redshift")
	if !ok {
		t.Fatal("redshift not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Redshift (IAM auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["AWS_REGION"] {
		t.Errorf("EnvKeys = %+v, want AWS_REGION", r.EnvKeys)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Snowflake(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("snowflake")
	if !ok {
		t.Fatal("snowflake not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	// Command must contain --account flag.
	hasAccount := false
	hasAllowWrite := false
	for _, arg := range r.Command {
		if arg == "--account" {
			hasAccount = true
		}
		if arg == "--allow_write" {
			hasAllowWrite = true
		}
	}
	if !hasAccount {
		t.Error("snowflake Command should include --account flag")
	}
	if hasAllowWrite {
		t.Error("snowflake Command must NOT include --allow_write (read-only enforcement)")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Snowflake (CLI-flag auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	for _, name := range []string{"SNOWFLAKE_ACCOUNT", "SNOWFLAKE_USER", "SNOWFLAKE_PASSWORD", "SNOWFLAKE_WAREHOUSE", "SNOWFLAKE_DATABASE"} {
		if !envNames[name] {
			t.Errorf("EnvKeys missing %s", name)
		}
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if r.Warning == "" {
		t.Error("snowflake should carry a Warning (read-only note, community package)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_BigQuery(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("bigquery")
	if !ok {
		t.Fatal("bigquery not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	// Must include --prebuilt=bigquery flag.
	hasPrebuilt := false
	for _, arg := range r.Command {
		if arg == "--prebuilt=bigquery" {
			hasPrebuilt = true
		}
	}
	if !hasPrebuilt {
		t.Error("bigquery Command should include --prebuilt=bigquery flag")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for BigQuery (ADC auth), got %+v", r.Auth)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	// Must have project_id config option.
	hasProjectID := false
	for _, c := range r.ConfigOptions {
		if c.Name == "project_id" {
			hasProjectID = true
		}
	}
	if !hasProjectID {
		t.Error("bigquery ConfigOptions should include project_id")
	}
	if r.Warning == "" {
		t.Error("bigquery should carry a Warning (verify at build note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Metabase(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("metabase")
	if !ok {
		t.Fatal("metabase not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Metabase (API-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["METABASE_URL"] {
		t.Errorf("EnvKeys = %+v, want METABASE_URL", r.EnvKeys)
	}
	if !envNames["METABASE_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want METABASE_API_KEY", r.EnvKeys)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Looker(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("looker")
	if !ok {
		t.Fatal("looker not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Looker (API3-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["LOOKER_BASE_URL"] {
		t.Errorf("EnvKeys = %+v, want LOOKER_BASE_URL", r.EnvKeys)
	}
	if !envNames["LOOKER_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want LOOKER_CLIENT_ID", r.EnvKeys)
	}
	if !envNames["LOOKER_CLIENT_SECRET"] {
		t.Errorf("EnvKeys = %+v, want LOOKER_CLIENT_SECRET", r.EnvKeys)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_GA4(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("ga4")
	if !ok {
		t.Fatal("ga4 not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for GA4 (service account auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["GOOGLE_APPLICATION_CREDENTIALS"] {
		t.Errorf("EnvKeys = %+v, want GOOGLE_APPLICATION_CREDENTIALS", r.EnvKeys)
	}
	if !envNames["GA4_PROPERTY_ID"] {
		t.Errorf("EnvKeys = %+v, want GA4_PROPERTY_ID", r.EnvKeys)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Mailchimp(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("mailchimp")
	if !ok {
		t.Fatal("mailchimp not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Mailchimp (API-key auth), got %+v", r.Auth)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "MAILCHIMP_API_KEY" {
		t.Errorf("EnvKeys = %+v, want one entry named MAILCHIMP_API_KEY", r.EnvKeys)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Braze(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("braze")
	if !ok {
		t.Fatal("braze not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	// Must NOT include ALLOW_WRITES in command (read-only enforcement).
	for _, arg := range r.Command {
		if arg == "BRAZE_ALLOW_WRITES" || arg == "--allow_writes" {
			t.Error("braze Command must NOT include BRAZE_ALLOW_WRITES (read-only enforcement)")
		}
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Braze (API-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["BRAZE_API_KEY"] {
		t.Errorf("EnvKeys = %+v, want BRAZE_API_KEY", r.EnvKeys)
	}
	if !envNames["BRAZE_BASE_URL"] {
		t.Errorf("EnvKeys = %+v, want BRAZE_BASE_URL", r.EnvKeys)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if r.Warning == "" {
		t.Error("braze should carry a Warning (read-only note, cluster URL note)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Marketo(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("marketo")
	if !ok {
		t.Fatal("marketo not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "npx" {
		t.Errorf("Command[0] = %q, want npx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Marketo (client-credentials auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["MARKETO_CLIENT_ID"] {
		t.Errorf("EnvKeys = %+v, want MARKETO_CLIENT_ID", r.EnvKeys)
	}
	if !envNames["MARKETO_CLIENT_SECRET"] {
		t.Errorf("EnvKeys = %+v, want MARKETO_CLIENT_SECRET", r.EnvKeys)
	}
	if !envNames["MARKETO_MUNCHKIN_ID"] {
		t.Errorf("EnvKeys = %+v, want MARKETO_MUNCHKIN_ID", r.EnvKeys)
	}
	if r.Category != "marketing" {
		t.Errorf("Category = %q, want marketing", r.Category)
	}
	if r.Warning == "" {
		t.Error("marketo should carry a Warning (stretch: package unconfirmed)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Databricks(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("databricks")
	if !ok {
		t.Fatal("databricks not in registry")
	}
	if len(r.Command) == 0 || r.Command[0] != "uvx" {
		t.Errorf("Command[0] = %q, want uvx", r.Command[0])
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Databricks (PAT auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["DATABRICKS_HOST"] {
		t.Errorf("EnvKeys = %+v, want DATABRICKS_HOST", r.EnvKeys)
	}
	if !envNames["DATABRICKS_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want DATABRICKS_TOKEN", r.EnvKeys)
	}
	if r.Category != "data" {
		t.Errorf("Category = %q, want data", r.Category)
	}
	// Must have stretch warning about package verification.
	if r.Warning == "" {
		t.Error("databricks should carry a Warning (stretch: verify runnable entry point)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Stripe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("stripe")
	if !ok {
		t.Fatal("stripe not in registry")
	}
	// Stdio recipe — restricted-key path, not mcp.stripe.com (which cannot constrain tools).
	if r.Transport != "" && r.Transport != "stdio" {
		t.Errorf("Transport = %q, want stdio (restricted-key server path)", r.Transport)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys (read-only restricted key)", r.PrimaryAuth)
	}
	// No OAuth block — auth is purely the restricted env key.
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Stripe (restricted-key auth, no OAuth flow), got %+v", r.Auth)
	}
	// Must have STRIPE_RESTRICTED_KEY env key (not STRIPE_SECRET_KEY — never a full secret key).
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["STRIPE_RESTRICTED_KEY"] {
		t.Errorf("EnvKeys = %+v, want STRIPE_RESTRICTED_KEY (read-only Restricted Key)", r.EnvKeys)
	}
	if envNames["STRIPE_SECRET_KEY"] {
		t.Error("STRIPE_SECRET_KEY must not appear — use STRIPE_RESTRICTED_KEY with read-only permissions only")
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	// FR-003: Warning must document read-only constraint and restricted-key requirement.
	if r.Warning == "" {
		t.Error("stripe should carry a Warning (read-only + restricted key required)")
	}
	// Command must include a read-only flag to prevent write tool exposure.
	hasReadOnlyFlag := false
	for _, arg := range r.Command {
		if arg == "--tools=read-only" || strings.Contains(arg, "read-only") {
			hasReadOnlyFlag = true
		}
	}
	if !hasReadOnlyFlag {
		t.Errorf("stripe Command = %v, want --tools=read-only flag to prevent write tool exposure", r.Command)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

// canonicalCategories is the FR-005 taxonomy every registry recipe's
// Category must fall into. 16 buckets, kept in sync with the mapping
// documented on Recipe.Category (core/mcp/recipes/recipes.go).
var canonicalCategories = map[string]bool{
	"automation": true, "communication": true, "crm": true, "data": true,
	"design": true, "developer": true, "ecommerce": true, "files": true,
	"finance": true, "hr_people": true, "marketing": true, "observability": true,
	"productivity": true, "security": true, "support": true, "web": true,
}

// TestRecipe_CatalogCategories enforces the canonical category taxonomy
// (FR-005) across every recipe in the registry, not just a spot-checked
// subset — this is what stops a future connector pack from reintroducing
// a drifted category name (e.g. "filesystem" or "itsm") that the Tools
// catalog UI wouldn't know how to bucket.
func TestRecipe_CatalogCategories(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if r.Category == "" {
			t.Errorf("recipe %q has empty category", r.ID)
			continue
		}
		if !canonicalCategories[r.Category] {
			t.Errorf("recipe %q has non-canonical category %q; want one of the 16-bucket taxonomy", r.ID, r.Category)
		}
	}
}

// TestRecipe_CatalogAliasesPopulated is the FR-005 search-expansion
// guarantee: every registry recipe must carry at least one search alias
// so the Tools catalog's search box can surface a connector by an
// alternate name, abbreviation, or generic task word (e.g. "email" ->
// gmail/outlook) even when the query doesn't match the id or display
// name. Also guards against accidental keyword stuffing / duplication.
func TestRecipe_CatalogAliasesPopulated(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if len(r.Aliases) == 0 {
			t.Errorf("recipe %q has no aliases", r.ID)
			continue
		}
		if len(r.Aliases) > 6 {
			t.Errorf("recipe %q has %d aliases, want at most 6", r.ID, len(r.Aliases))
		}
		seen := map[string]bool{}
		for _, a := range r.Aliases {
			if a == "" {
				t.Errorf("recipe %q has an empty alias entry", r.ID)
				continue
			}
			if strings.ToLower(a) != a {
				t.Errorf("recipe %q alias %q is not lowercase", r.ID, a)
			}
			if seen[a] {
				t.Errorf("recipe %q has duplicate alias %q", r.ID, a)
			}
			seen[a] = true
		}
	}
}

// TestRecipe_NoFloatingPackageSpecs forbids the two package-spec anti-patterns
// that the v0.45.0 catalog exhibited and that are never defensible:
//
//   - an explicit "@latest" tag (floating — resolves to whatever is published at
//     launch, so a hijacked publish runs with the user's credentials), and
//   - a git install with no pinned ref (git+https://…/repo with no @<sha|tag>),
//     which tracks the default branch HEAD.
//
// Bare package names (the catalog's long-standing convention for both official
// reference servers and vetted community packages) are allowed here — moving the
// whole catalog to exact-version pins is a separate hygiene effort. This test
// exists so a new connector can never reintroduce @latest or an unpinned git URL,
// the specific drift that shipped broken/squattable recipes in v0.45.0.
func TestRecipe_NoFloatingPackageSpecs(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if len(r.Command) == 0 {
			continue
		}
		runner := r.Command[0]
		if runner != "npx" && runner != "uvx" {
			continue
		}
		// The package spec is the token after --from, else the first non-flag arg.
		var spec string
		args := r.Command[1:]
		for i, a := range args {
			if a == "--from" && i+1 < len(args) {
				spec = args[i+1]
				break
			}
		}
		if spec == "" {
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					spec = a
					break
				}
			}
		}
		if spec == "" {
			t.Errorf("recipe %q: could not find package spec in command %v", r.ID, r.Command)
			continue
		}
		if strings.HasSuffix(spec, "@latest") {
			t.Errorf("recipe %q: package %q uses floating @latest — pin an exact version", r.ID, spec)
		}
		if strings.Contains(spec, "git+") {
			// Require a ref beyond the scheme's "://": git+https://host/repo@<ref>.
			if strings.LastIndex(spec, "@") <= strings.Index(spec, "://")+2 {
				t.Errorf("recipe %q: git install %q has no pinned ref — append @<commit-or-tag>", r.ID, spec)
			}
		}
	}
}

func TestRecipe_Pipedrive(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("pipedrive")
	if !ok {
		t.Fatal("pipedrive not in registry")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Pipedrive (API-key auth), got %+v", r.Auth)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "PIPEDRIVE_API_TOKEN" {
		t.Errorf("EnvKeys = %+v, want one entry named PIPEDRIVE_API_TOKEN", r.EnvKeys)
	}
	if r.Category != "crm" {
		t.Errorf("Category = %q, want crm", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Shopify(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("shopify")
	if !ok {
		t.Fatal("shopify not in registry")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthKeys {
		t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
	}
	if r.Auth != nil {
		t.Errorf("Auth should be nil for Shopify (API-key auth), got %+v", r.Auth)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SHOPIFY_STORE_URL"] {
		t.Errorf("EnvKeys = %+v, want SHOPIFY_STORE_URL", r.EnvKeys)
	}
	if !envNames["SHOPIFY_ACCESS_TOKEN"] {
		t.Errorf("EnvKeys = %+v, want SHOPIFY_ACCESS_TOKEN", r.EnvKeys)
	}
	if r.Category != "ecommerce" {
		t.Errorf("Category = %q, want ecommerce", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_BigCommerceDocs(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("bigcommerce-docs")
	if !ok {
		t.Fatal("bigcommerce-docs not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://docs.bigcommerce.com/_mcp/server" {
		t.Errorf("URL = %q, want https://docs.bigcommerce.com/_mcp/server", r.URL)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthNone {
		t.Errorf("PrimaryAuth = %q, want none", r.PrimaryAuth)
	}
	// No auth block needed since auth.kind is "none".
	if r.Auth == nil || r.Auth.Kind != "none" {
		t.Errorf("Auth = %+v, want kind=none", r.Auth)
	}
	if r.Category != "ecommerce" {
		t.Errorf("Category = %q, want ecommerce", r.Category)
	}
	if r.Warning == "" {
		t.Error("bigcommerce-docs should carry a Warning (docs only, not store data)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Salesforce(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("salesforce")
	if !ok {
		t.Fatal("salesforce not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	// URL uses per-org substitution token (verify URL templating at build).
	if !strings.Contains(r.URL, "${SALESFORCE_ORG_DOMAIN}") {
		t.Errorf("URL = %q, want ${SALESFORCE_ORG_DOMAIN} substitution token", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "${KAMEAS_SALESFORCE_OAUTH_CLIENT_ID}" {
		t.Errorf("salesforce auth.client_id = %q, want ${KAMEAS_SALESFORCE_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "crm" {
		t.Errorf("Category = %q, want crm", r.Category)
	}
	// Must have org_domain config option.
	if len(r.ConfigOptions) == 0 || r.ConfigOptions[0].Name != "org_domain" {
		t.Errorf("ConfigOptions = %+v, want one entry named org_domain", r.ConfigOptions)
	}
	if !r.ConfigOptions[0].Required {
		t.Error("org_domain should be required")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Box(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("box")
	if !ok {
		t.Fatal("box not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.box.com" {
		t.Errorf("URL = %q, want https://mcp.box.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Owner-app: placeholder seam — no DCR.
	if r.Auth.ClientID != "${KAMEAS_BOX_OAUTH_CLIENT_ID}" {
		t.Errorf("box auth.client_id = %q, want ${KAMEAS_BOX_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_BOX_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_BOX_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_BOX_OAUTH_CLIENT_ID should be required (no DCR — needs Box Admin Console app)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_HubSpot(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("hubspot")
	if !ok {
		t.Fatal("hubspot not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.hubspot.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.hubspot.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// Owner-app: placeholder seam — no DCR.
	if r.Auth.ClientID != "${KAMEAS_HUBSPOT_OAUTH_CLIENT_ID}" {
		t.Errorf("hubspot auth.client_id = %q, want ${KAMEAS_HUBSPOT_OAUTH_CLIENT_ID} placeholder", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
	}
	if r.Category != "crm" {
		t.Errorf("Category = %q, want crm", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "KAMEAS_HUBSPOT_OAUTH_CLIENT_ID" {
		t.Errorf("EnvKeys = %+v, want one entry named KAMEAS_HUBSPOT_OAUTH_CLIENT_ID", r.EnvKeys)
	}
	if !r.EnvKeys[0].Required {
		t.Error("KAMEAS_HUBSPOT_OAUTH_CLIENT_ID should be required (no DCR — needs registered Kameas app)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Square(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("square")
	if !ok {
		t.Fatal("square not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.squareup.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.squareup.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("square auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	// FR-003: all scopes must be read-only variants (no WRITE/payment).
	for _, scope := range r.Auth.Scopes {
		if !strings.HasSuffix(scope, "_READ") {
			t.Errorf("square scope %q is not a read-only scope (must end with _READ)", scope)
		}
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	if r.Warning == "" {
		t.Error("square should carry a Warning (Beta + read-only constraint)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_PayPal(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("paypal")
	if !ok {
		t.Fatal("paypal not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.paypal.com" {
		t.Errorf("URL = %q, want https://mcp.paypal.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("paypal auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "finance" {
		t.Errorf("Category = %q, want finance", r.Category)
	}
	// FR-003: read/query-only enforcement documented in Warning field.
	if r.Warning == "" {
		t.Error("paypal should carry a Warning (read/query only constraint)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Dropbox(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("dropbox")
	if !ok {
		t.Fatal("dropbox not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.dropbox.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.dropbox.com/mcp (verify at build)", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("dropbox auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("dropbox Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "files" {
		t.Errorf("Category = %q, want files", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Miro(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("miro")
	if !ok {
		t.Fatal("miro not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.miro.com" {
		t.Errorf("URL = %q, want https://mcp.miro.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("miro auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "design" {
		t.Errorf("Category = %q, want design", r.Category)
	}
	if r.Warning == "" {
		t.Error("miro should carry a Warning (Enterprise plan required)")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Figma(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("figma")
	if !ok {
		t.Fatal("figma not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.figma.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.figma.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("figma auth.client_id = %q, want empty (DCR zero-app, verify at build)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "design" {
		t.Errorf("Category = %q, want design", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Canva(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("canva")
	if !ok {
		t.Fatal("canva not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.canva.com" {
		t.Errorf("URL = %q, want https://mcp.canva.com", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	if r.Auth.ClientID != "" {
		t.Errorf("canva auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "design" {
		t.Errorf("Category = %q, want design", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

func TestRecipe_Airtable(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("airtable")
	if !ok {
		t.Fatal("airtable not in registry")
	}
	if r.Transport != recipes.TransportHTTP {
		t.Errorf("Transport = %q, want http", r.Transport)
	}
	if r.URL != "https://mcp.airtable.com/mcp" {
		t.Errorf("URL = %q, want https://mcp.airtable.com/mcp", r.URL)
	}
	if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
		t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
	}
	// DCR zero-app: no baked client_id.
	if r.Auth.ClientID != "" {
		t.Errorf("airtable auth.client_id = %q, want empty (DCR zero-app)", r.Auth.ClientID)
	}
	if len(r.Auth.Scopes) == 0 {
		t.Error("airtable Auth.Scopes should list requested scopes")
	}
	if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthDCR {
		t.Errorf("PrimaryAuth = %q, want browser_oauth_dcr", r.PrimaryAuth)
	}
	if r.Category != "productivity" {
		t.Errorf("Category = %q, want productivity", r.Category)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("Validate() error: %v", err)
	}
}

// TestRecipe_FR013Additions covers the spec-091 FR-013 catalog additions:
// dynatrace, splunk, elasticsearch (remote HTTP with key-based auth),
// google-docs + google-sheets (Google's official Workspace remote MCP
// servers, OAuth), and the ms-365 excel preset (stdio, US4-gated).
// Word/PowerPoint presets do not exist upstream (no Microsoft Graph
// content-editing API), so no recipes are added for them.
func TestRecipe_FR013Additions(t *testing.T) {
	cat := recipes.Registry()

	t.Run("dynatrace", func(t *testing.T) {
		r, ok := cat.Get("dynatrace")
		if !ok {
			t.Fatal("dynatrace not in registry")
		}
		if r.Transport != recipes.TransportHTTP {
			t.Errorf("Transport = %q, want http", r.Transport)
		}
		if want := "https://${DYNATRACE_ENVIRONMENT_NAME}.apps.dynatrace.com/platform-reserved/mcp-gateway/v0.1/servers/dynatrace-mcp/mcp"; r.URL != want {
			t.Errorf("URL = %q, want %q", r.URL, want)
		}
		if r.PrimaryAuth != recipes.PrimaryAuthKeys {
			t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
		}
		if r.HeadersTemplate["Authorization"] != "Bearer ${DYNATRACE_PLATFORM_TOKEN}" {
			t.Errorf("Authorization header template = %q", r.HeadersTemplate["Authorization"])
		}
		if r.Category != "observability" {
			t.Errorf("Category = %q, want observability", r.Category)
		}
		if len(r.EnvKeys) != 2 || r.EnvKeys[0].Name != "DYNATRACE_ENVIRONMENT_NAME" || r.EnvKeys[1].Name != "DYNATRACE_PLATFORM_TOKEN" {
			t.Errorf("EnvKeys = %+v, want DYNATRACE_ENVIRONMENT_NAME + DYNATRACE_PLATFORM_TOKEN", r.EnvKeys)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("Validate() error: %v", err)
		}
	})

	t.Run("splunk", func(t *testing.T) {
		r, ok := cat.Get("splunk")
		if !ok {
			t.Fatal("splunk not in registry")
		}
		if r.Transport != recipes.TransportHTTP {
			t.Errorf("Transport = %q, want http", r.Transport)
		}
		if want := "https://${SPLUNK_MCP_ENDPOINT}"; r.URL != want {
			t.Errorf("URL = %q, want %q", r.URL, want)
		}
		if r.PrimaryAuth != recipes.PrimaryAuthKeys {
			t.Errorf("PrimaryAuth = %q, want keys", r.PrimaryAuth)
		}
		if r.HeadersTemplate["Authorization"] != "Bearer ${SPLUNK_MCP_TOKEN}" {
			t.Errorf("Authorization header template = %q", r.HeadersTemplate["Authorization"])
		}
		if r.Category != "observability" {
			t.Errorf("Category = %q, want observability", r.Category)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("Validate() error: %v", err)
		}
	})

	t.Run("elasticsearch", func(t *testing.T) {
		r, ok := cat.Get("elasticsearch")
		if !ok {
			t.Fatal("elasticsearch not in registry")
		}
		if r.Transport != recipes.TransportHTTP {
			t.Errorf("Transport = %q, want http", r.Transport)
		}
		if want := "https://${KIBANA_HOST}/api/agent_builder/mcp"; r.URL != want {
			t.Errorf("URL = %q, want %q", r.URL, want)
		}
		if r.HeadersTemplate["Authorization"] != "ApiKey ${ELASTIC_API_KEY}" {
			t.Errorf("Authorization header template = %q", r.HeadersTemplate["Authorization"])
		}
		if r.Category != "data" {
			t.Errorf("Category = %q, want data", r.Category)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("Validate() error: %v", err)
		}
	})

	for _, id := range []string{"google-docs", "google-sheets"} {
		id := id
		t.Run(id, func(t *testing.T) {
			r, ok := cat.Get(id)
			if !ok {
				t.Fatalf("%s not in registry", id)
			}
			if r.Transport != recipes.TransportHTTP {
				t.Errorf("Transport = %q, want http", r.Transport)
			}
			if !strings.HasSuffix(r.URL, ".googleapis.com/mcp/v1") {
				t.Errorf("URL = %q, want an official *.googleapis.com/mcp/v1 endpoint", r.URL)
			}
			if r.Auth == nil || r.Auth.Kind != recipes.AuthKindMCPOAuth {
				t.Fatalf("Auth = %+v, want mcp_oauth", r.Auth)
			}
			// Google does not support DCR: PKCE with a registered app.
			if r.PrimaryAuth != recipes.PrimaryAuthBrowserOAuthPKCE {
				t.Errorf("PrimaryAuth = %q, want browser_oauth_pkce", r.PrimaryAuth)
			}
			if r.Category != "productivity" {
				t.Errorf("Category = %q, want productivity", r.Category)
			}
			if err := r.Validate(); err != nil {
				t.Errorf("Validate() error: %v", err)
			}
		})
	}

	t.Run("excel", func(t *testing.T) {
		r, ok := cat.Get("excel")
		if !ok {
			t.Fatal("excel not in registry")
		}
		wantCmd := []string{"npx", "-y", "@softeria/ms-365-mcp-server", "--preset", "excel"}
		if len(r.Command) != len(wantCmd) {
			t.Fatalf("Command = %v, want %v", r.Command, wantCmd)
		}
		for i := range wantCmd {
			if r.Command[i] != wantCmd[i] {
				t.Fatalf("Command = %v, want %v", r.Command, wantCmd)
			}
		}
		if r.PrimaryAuth != recipes.PrimaryAuthDeviceCode {
			t.Errorf("PrimaryAuth = %q, want device_code", r.PrimaryAuth)
		}
		if r.Category != "productivity" {
			t.Errorf("Category = %q, want productivity", r.Category)
		}
		if len(r.EnvKeys) != 2 || r.EnvKeys[0].Name != "MS365_MCP_CLIENT_ID" || r.EnvKeys[1].Name != "MS365_MCP_TENANT_ID" {
			t.Errorf("EnvKeys = %+v, want optional MS365_MCP_CLIENT_ID + MS365_MCP_TENANT_ID", r.EnvKeys)
		}
		if !strings.Contains(r.Warning, "US4") {
			t.Errorf("excel warning should note the vendored runtime lane (US4); got %q", r.Warning)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("Validate() error: %v", err)
		}
	})
}
