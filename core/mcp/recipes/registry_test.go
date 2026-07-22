package recipes_test

import (
	"encoding/json"
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
	// WP01–WP18 — marketing analytics + data/BI pack (01NCONN09)
	"mixpanel",
	"amplitude",
	"klaviyo",
	"dbt-cloud",
	"tableau",
	"fivetran",
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
	if r.Category != "search" {
		t.Errorf("Category = %q, want search", r.Category)
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
	if r.Category != "fetch" {
		t.Errorf("Category = %q, want fetch", r.Category)
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
	if r.Category != "search" {
		t.Errorf("Category = %q, want search", r.Category)
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
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
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
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
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
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
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

// WP01 — Marketing analytics + data/BI pack (01NCONN09)

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
	if r.Category != "analytics" {
		t.Errorf("Category = %q, want analytics", r.Category)
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
	if r.Category != "analytics" {
		t.Errorf("Category = %q, want analytics", r.Category)
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
	if r.Category != "bi" {
		t.Errorf("Category = %q, want bi", r.Category)
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
