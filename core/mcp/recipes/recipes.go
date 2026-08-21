// Package recipes is the shipped-recipes catalog for the MCP stdio
// pool. It exposes:
//
//   - Recipe / EnvKey / Capabilities / SamplingPolicy / Catalog —
//     the data-model types parsed from the embedded shipped.json.
//   - LoadShipped / Shipped — accessors for the package-level
//     singleton parsed at process start.
//   - EnabledRecipes — the persisted on-disk list of toggled-on
//     recipes (atomic save, corrupt-tolerant load).
//   - KeychainLocator / ResolveEnv / EnvAuditHash — the secrets-
//     backed env resolver used by the pool when spawning a server.
//
// The package is independent of `core/mcp/stdio/`: it only produces
// `core/mcp.ServerSpec` values via (*Recipe).ToServerSpec, which is
// the public type the existing pool surface already consumes.
//
// Spec mapping: FR-018 (catalog), FR-019 (enabled persistence),
// FR-020 (secrets resolve), NFR-006 (no plaintext on disk).
package recipes

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/mcp"
)

// Recognised transport identifiers. Empty string is treated as
// TransportStdio for backwards compatibility with the shipped
// catalog (which predates the transport field).
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// Recipe is one entry in the shipped catalog. The fields mirror the
// data-model precisely; JSON tags match the on-disk shipped.json shape
// (snake_case).
type Recipe struct {
	// ID is the catalog primary key. It must match
	// `^[a-z][a-z0-9-]{0,63}$` because it doubles as the prefix of
	// every keychain locator and as `mcp.Tool.Server` for tools
	// produced by this server.
	ID string `json:"id"`
	// DisplayName is the user-visible recipe label.
	DisplayName string `json:"display_name"`
	// Description is the user-visible blurb in the install modal.
	Description string `json:"description"`
	// Category groups recipes in the modal. Canonical taxonomy (16
	// buckets): automation, communication, crm, data, design, developer,
	// ecommerce, files, finance, hr_people, marketing, observability,
	// productivity, security, support, web.
	Category string `json:"category"`
	// Command is the argv used to spawn the stdio server.
	// Command[0] must be non-empty for stdio recipes.
	Command []string `json:"command"`
	// Transport names the wire protocol the recipe speaks. Empty (or
	// "stdio") preserves backwards compatibility with the shipped
	// stdio catalog. Recognised values: "stdio", "http", "sse".
	Transport string `json:"transport,omitempty" yaml:"transport,omitempty"`
	// URL is the endpoint for HTTP/SSE recipes. Required when
	// Transport is non-stdio. Subject to ${VAR} env-var substitution
	// at connection-open time.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// HeadersTemplate is the per-request HTTP header set applied on
	// every outbound request. Values are subject to ${VAR}
	// substitution at Open time.
	HeadersTemplate map[string]string `json:"headers_template,omitempty" yaml:"headers_template,omitempty"`
	// PostURL is the SSE client→server endpoint. Required when
	// Transport=="sse". Subject to ${VAR} substitution at Open time.
	PostURL string `json:"post_url,omitempty" yaml:"post_url,omitempty"`
	// EnvKeys are the credential-bearing env vars the server reads.
	// Slice order is render order in the install modal.
	EnvKeys []EnvKey `json:"env_keys"`
	// Capabilities is the recipe-author's declaration; the
	// negotiated set lives on ServerInstance.Negotiated.
	Capabilities Capabilities `json:"capabilities"`
	// DocsURL is the recipe-level documentation link.
	DocsURL string `json:"docs_url"`
	// InitTimeoutMs is the post-spawn initialize deadline. 0 -> 5000.
	InitTimeoutMs int `json:"init_timeout_ms"`
	// PingPeriodMs is the keepalive ping cadence. 0 -> 30000.
	PingPeriodMs int `json:"ping_period_ms"`
	// SamplingPolicy is the per-recipe sampling default.
	SamplingPolicy SamplingPolicy `json:"sampling_policy"`
	// ArgsTemplate is appended verbatim to Command after ${VAR} token
	// substitution. Recipes that take a fixed argv (e.g. Brave Search)
	// leave this nil; recipes that need per-install variables (e.g. the
	// filesystem server's allowed-directory positional args) expand
	// tokens via Substitute at ToServerSpec time.
	ArgsTemplate []string `json:"args_template,omitempty"`
	// ConfigOptions declares the per-install config fields the modal
	// renders. Empty for recipes that need only env keys.
	ConfigOptions []ConfigOption `json:"config_options,omitempty"`
	// Warning is an optional user-facing message rendered by the install
	// modal alongside the install controls. Its severity — how alarming the
	// rendering is — is governed by WarningSeverity, NOT by mere presence:
	// an empty WarningSeverity (the default) renders a calm, neutral notice;
	// only WarningSeverityDanger escalates to the stark red banner + explicit
	// confirmation checkbox. Most recipes that set Warning are conveying a
	// benign setup/latency/maturity note (e.g. "requires uv installed",
	// "first run downloads a browser binary") and should stay at the default
	// (info) severity. Reserve WarningSeverityDanger for recipes that grant
	// genuinely elevated trust — e.g. an unrestricted filesystem MCP.
	Warning string `json:"warning,omitempty"`
	// WarningSeverity controls how Warning is rendered. "" / WarningSeverityInfo
	// → calm neutral notice (no risk acknowledgment, ordinary Install button).
	// WarningSeverityDanger → red hazard banner with a required "I understand
	// the risk" checkbox gating an "Install with risk" button. Ignored when
	// Warning is empty.
	WarningSeverity string `json:"warning_severity,omitempty"`
	// Aliases is an optional list of alternate search terms (lowercase) that the
	// catalog UI uses to surface this recipe when the user's search string does
	// not match the ID or DisplayName. For example, the Google Calendar recipe
	// can declare aliases: ["google meet"] so that searching "meet" or "google
	// meet" surfaces the Calendar connector, since Meet has no standalone MCP.
	// The alias list is indexed alongside the ID and DisplayName; matches are
	// shown in the catalog with a note indicating the alternate name.
	Aliases []string `json:"aliases,omitempty"`
	// RecommendedPolicyTemplate names a Cedar policy file under
	// `core/policy/cedar/policies/` that the user is encouraged to
	// install alongside this recipe. The install modal surfaces a
	// "copy recommended policy" affordance when set.
	RecommendedPolicyTemplate string `json:"recommended_policy_template,omitempty"`
	// RequiredCapability names the fleet capability key that must be present
	// (and non-stale per the 24 h TTL) for this recipe to be enabled. When
	// non-empty, the capability reconciler enables the recipe when the
	// capability is present and disables it when it disappears or is stale.
	// Empty (the default) means the recipe has no capability gate.
	// JSON key: required_capability (omitempty — absent from most recipes).
	RequiredCapability string `json:"required_capability,omitempty"`
	// PromptOnFirstUse lists tool names (bare, without the server prefix)
	// within this recipe that opt into the universal interactive-permission
	// prompt gate on their first invocation. When the Cedar engine returns
	// NotApplicable for one of these tools, the toolloop dispatcher routes
	// through the prompt registry so the user can grant or deny access
	// interactively.
	//
	// An empty (or nil) slice — the default — means no per-tool prompts:
	// the caller's existing allow/deny policy drives the verdict, exactly
	// as before WP06. Recipe authors SHOULD list only tools whose default
	// posture is NotApplicable (i.e. not already covered by a broad
	// server-level Cedar permit rule); listing a builtin kenaz tool here
	// has no effect because the default_tool_policy.cedar already allows
	// them unconditionally.
	//
	// Tools listed here are also EXCLUDED from WP11's install-time pre-seed
	// loop: they keep the gate prompt instead of getting a silent
	// tool_allow_*.cedar snippet.
	//
	// Example: a recipe with tool names "write_file" and "delete_file"
	// that wants first-use confirmation only on "delete_file" would set:
	//
	//   "prompt_on_first_use": ["delete_file"]
	//
	// JSON key: prompt_on_first_use. omitempty: present only when non-empty.
	PromptOnFirstUse []string `json:"prompt_on_first_use,omitempty"`
	// PreSeedingPolicy controls how Cedar permit snippets are written
	// for the recipe's tools at install time (cedar WP11). Recognised values:
	//
	//   ""           — same as "allow_all" (backwards-compatible default).
	//   "allow_all"  — write a permit snippet for every discovered tool
	//                  except those named in PromptOnFirstUse.
	//   "prompt_only"— skip pre-seeding entirely; the Cedar gate prompt
	//                  fires for every tool on first call.
	//   "none"       — synonym for "prompt_only"; no snippets written.
	//
	// Failure to write any single snippet is best-effort: a warning is
	// logged and the install continues. The install never rolls back on
	// snippet write failure.
	PreSeedingPolicy string `json:"pre_seeding_policy,omitempty"`
	// Auth declares an OAuth sign-in for remote (http/sse) recipes. When set
	// to Kind=="mcp_oauth" the harness can obtain a bearer token via the MCP
	// authorization flow (RFC 9728 discovery → RFC 8414 → auth-code+PKCE,
	// implemented in core/mcp/oauth) instead of asking the user to paste a
	// long-lived token. Nil for ordinary key-based or local stdio recipes.
	Auth *RecipeAuth `json:"auth,omitempty" yaml:"auth,omitempty"`
	// PrimaryAuth names the auth method the install modal should lead with.
	// When set, the modal presents this method prominently and collapses any
	// additional credential fields (env keys or optional config) under an
	// "Advanced" / "Use a token instead" section.
	//
	// Recognised values:
	//   ""            — legacy / unset: no reordering; the modal renders keys
	//                   as declared (backwards compatible default).
	//   "oauth"       — lead with OAuth "Sign in" button (requires Auth != nil).
	//                   TODO: OAuth client registration required before this is
	//                   live; the UX seam is wired but sign-in is a no-op until
	//                   Auth.ClientID is set (ref: OAuth app registration ticket).
	//   "device_code" — lead with a "Sign in with browser" device-code flow
	//                   message (e.g. Outlook); env keys become "Advanced".
	//   "keys"        — lead with the first env key; all keys are primary.
	//   "none"        — recipe needs no credentials; env keys section hidden
	//                   unless all keys are optional (they're collapsed by
	//                   default).
	PrimaryAuth string `json:"primary_auth,omitempty" yaml:"primary_auth,omitempty"`
	// Source tags the loader that produced this Recipe. It is set by
	// loaders (LoadShipped → SourceShipped, registry loader →
	// SourceRegistry, UserStore → SourceUser/SourceImported) and is
	// never persisted on disk: both the JSON and YAML codecs skip the
	// field. Consumers use it to render badges and to drive the
	// MergedCatalog precedence rules.
	Source string `json:"-" yaml:"-"`
}

// AuthKindMCPOAuth is the only RecipeAuth.Kind currently recognised: the
// harness runs the MCP authorization flow (core/mcp/oauth) to obtain a bearer
// token for a remote server.
const AuthKindMCPOAuth = "mcp_oauth"

// WarningSeverity constants for Recipe.WarningSeverity. They govern how the
// install modal renders a recipe's Warning string.
const (
	// WarningSeverityInfo (also the zero value "") renders Warning as a calm,
	// neutral notice — no risk acknowledgment, ordinary Install button. This is
	// the right level for setup/latency/maturity notes.
	WarningSeverityInfo = "info"
	// WarningSeverityDanger renders Warning as a red hazard banner with a
	// required acknowledgment checkbox gating an "Install with risk" button.
	// Reserve for recipes that grant genuinely elevated trust.
	WarningSeverityDanger = "danger"
)

// PrimaryAuth hint constants for Recipe.PrimaryAuth. The install modal leads
// with the primary auth method and collapses secondary fields under "Advanced".
const (
	// PrimaryAuthOAuth leads with an OAuth "Sign in" browser button. As of
	// D-3/E-006 (kitty-specs/connector-lifecycle-truth-01PMZ303) this arm's
	// six recipes have no working sign-in path yet — none is DCR-capable,
	// PKCE-with-a-client-id, or device-code — and whether they move to one
	// of those arms is an open product question (E-006), not something
	// Kameas registers an app to fix.
	PrimaryAuthOAuth = "oauth"
	// PrimaryAuthDeviceCode leads with a device-code browser sign-in message
	// (e.g. Outlook ms-365-mcp-server). Env keys become secondary ("Advanced").
	PrimaryAuthDeviceCode = "device_code"
	// PrimaryAuthKeys leads with env-key fields; all keys are primary.
	PrimaryAuthKeys = "keys"
	// PrimaryAuthNone signals no credential is required. Any optional env keys
	// are collapsed under "Advanced" by default.
	PrimaryAuthNone = "none"
	// PrimaryAuthBrowserOAuthDCR leads with a browser OAuth "Sign in" button using
	// RFC 7591 Dynamic Client Registration — no pre-registered Kameas app needed.
	// The DCR engine registers the client dynamically at first use.
	PrimaryAuthBrowserOAuthDCR = "browser_oauth_dcr"
	// PrimaryAuthBrowserOAuthPKCE leads with a browser OAuth "Sign in" button
	// using PKCE against an auth server with no DCR support. The client id
	// is BRING-YOUR-OWN (ruling D-3, kitty-specs/connector-lifecycle-truth-
	// 01PMZ303): the operator registers their own OAuth app with the
	// provider and supplies its client id via the recipe's env-key field.
	// Kameas does not register, host, or rotate credentials for this app.
	PrimaryAuthBrowserOAuthPKCE = "browser_oauth_pkce"
)

// RecipeAuth declares OAuth sign-in for a remote recipe. It is advisory
// metadata: the actual authorization-server + endpoints are discovered at
// connect time from the server's 401 challenge (RFC 9728 / RFC 8414), so the
// recipe need only carry the bits discovery cannot supply.
type RecipeAuth struct {
	// Kind selects the auth mechanism. Currently only AuthKindMCPOAuth.
	Kind string `json:"kind" yaml:"kind"`
	// ClientID is a pre-registered public OAuth client id. Required for
	// authorization servers that do not support dynamic client registration
	// (GitHub being the canonical case — its AS publishes no metadata and
	// offers no DCR, so a registered OAuth App client id must be supplied).
	// Empty is permitted at rest (the recipe ships before the operator has
	// registered an app); sign-in fails with a clear error until it is set.
	ClientID string `json:"client_id,omitempty" yaml:"client_id,omitempty"`
	// ClientSecret is a pre-registered confidential OAuth client secret.
	// Required only for providers that mandate a confidential client (e.g.
	// Front). Most OAuth 2.1/PKCE providers do not require a client secret
	// and this field should be left empty. The value is treated as a
	// credential: it must be stored in the credstore (not hardcoded), and
	// the ${KAMEAS_<PROVIDER>_OAUTH_CLIENT_SECRET} seam pattern is used as
	// a placeholder in the recipe definition.
	ClientSecret string `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	// Scopes requested during the grant. When empty the harness falls back to
	// the scopes advertised in the protected-resource metadata.
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	// TokenEnvVar names the env var the bearer token is injected as for stdio
	// recipes that read a token from the environment (e.g. a local server
	// wanting GITHUB_TOKEN). Empty for pure remote recipes, where the token
	// goes into the Authorization header instead.
	TokenEnvVar string `json:"token_env_var,omitempty" yaml:"token_env_var,omitempty"`
}

// Recipe Source values. Loaders stamp these onto Recipe.Source after
// successful parse + Validate; the field is never persisted.
const (
	// SourceShipped marks the embedded shipped catalog (the 3
	// filesystem variants — see C-003).
	SourceShipped = "shipped"
	// SourceRegistry marks the curated in-binary registry (see WP06).
	SourceRegistry = "registry"
	// SourceUser marks recipes loaded from
	// <DataDir>/mcp/recipes/*.yaml.
	SourceUser = "user"
	// SourceImported marks recipes loaded from
	// <DataDir>/mcp/recipes/_imports/*.yaml — translated from a
	// clipboard-pasted Claude Desktop / Cursor config.
	SourceImported = "imported"
)

// ConfigOption is one user-editable knob the install modal renders.
// Kind drives the input shape; Default seeds the form when no last-
// used value is stashed. The struct intentionally carries no
// validation logic — the install path validates by Kind.
type ConfigOption struct {
	Name        string   `json:"name"`
	Display     string   `json:"display"`
	Kind        string   `json:"kind"`
	Default     any      `json:"default,omitempty"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
	// Choices is the closed set of allowed values when Kind ==
	// ConfigKindEnum. The install modal renders a dropdown; any other
	// Kind ignores this field. Empty for non-enum kinds.
	Choices []string `json:"choices,omitempty"`
}

// Recognised values for ConfigOption.Kind.
const (
	ConfigKindDirectoryList = "directory_list"
	ConfigKindBoolean       = "boolean"
	ConfigKindString        = "string"
	ConfigKindEnum          = "enum"
)

// EnvKey is one credential-bearing env var the server reads.
type EnvKey struct {
	// Name is the exact env var the server looks up
	// (e.g. "BRAVE_API_KEY").
	Name string `json:"name"`
	// Display is the modal label.
	Display string `json:"display"`
	// DocsURL points at the provider's API-key issuance page.
	DocsURL string `json:"docs_url"`
	// Required marks env vars whose absence prevents the recipe from
	// running. ResolveEnv returns an error when a required key fails
	// to resolve.
	Required bool `json:"required"`
}

// Capabilities is the recipe-author's declaration of which MCP
// capabilities the server advertises. The harness still issues
// tools/list / resources/list / prompts/list against the negotiated
// capability set returned from initialize; this struct is a hint that
// drives modal copy and pre-flight expectations.
type Capabilities struct {
	Tools     bool `json:"tools"`
	Resources bool `json:"resources"`
	Prompts   bool `json:"prompts"`
	Sampling  bool `json:"sampling"`
}

// SamplingPolicy is the per-recipe sampling configuration.
//
// Allowed: whether the recipe permits sampling at all (recipe-author
// choice; some servers should never call back into the LLM).
// Default: the initial value of the per-server sampling toggle when
// the recipe is first enabled; user can flip it later via FR-015.
type SamplingPolicy struct {
	Allowed bool `json:"allowed"`
	Default bool `json:"default"`
}

// Catalog is the shipped-recipes container parsed from shipped.json.
// Iteration order in List() matches declaration order in the JSON
// (so the modal renders categories in catalog-author order).
type Catalog struct {
	Version int      `json:"version"`
	Recipes []Recipe `json:"recipes"`
}

// List returns the recipes in catalog declaration order. Callers may
// retain the slice; the underlying memory is shared with the catalog
// (recipes are read-only after parse).
func (c *Catalog) List() []Recipe {
	if c == nil {
		return nil
	}
	out := make([]Recipe, len(c.Recipes))
	copy(out, c.Recipes)
	return out
}

// Get returns the recipe with id, or (zero, false) if no such recipe
// exists in the catalog.
func (c *Catalog) Get(id string) (Recipe, bool) {
	if c == nil {
		return Recipe{}, false
	}
	for i := range c.Recipes {
		if c.Recipes[i].ID == id {
			return c.Recipes[i], true
		}
	}
	return Recipe{}, false
}

// Sentinel errors. Callers branch on errors.Is.
var (
	// ErrRecipeNotFound is returned when a lookup misses the catalog.
	ErrRecipeNotFound = errors.New("recipes: recipe not found")
	// ErrInvalidRecipeID is returned when a recipe ID violates the
	// validation regex.
	ErrInvalidRecipeID = errors.New("recipes: invalid recipe id")
	// ErrInvalidRecipe is returned by validation for non-ID problems
	// (empty Command, etc.).
	ErrInvalidRecipe = errors.New("recipes: invalid recipe")
	// ErrUserRecipesDisabled is returned by AddRecipe / EditRecipe /
	// RemoveRecipe when the user-recipe store is not wired (e.g.
	// HARNESS_MCP_USER_RECIPES=off or no DataDir configured).
	ErrUserRecipesDisabled = errors.New("recipes: user-recipe store not configured")
)

// recipeIDPattern is the canonical recipe ID validation regex.
var recipeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidateRecipeID reports whether id is a syntactically valid recipe
// ID. It returns nil on success or ErrInvalidRecipeID wrapped with the
// offending id.
func ValidateRecipeID(id string) error {
	if !recipeIDPattern.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidRecipeID, id)
	}
	return nil
}

// validTransports is the exhaustive set of recognised transport values.
var validTransports = map[string]bool{
	"":     true, // empty = stdio (back-compat)
	"stdio": true,
	"http":  true,
	"sse":   true,
}

// Validate runs the recipe-level invariants. It is called by
// LoadShipped on every parsed recipe so that a bad shipped.json fails
// the binary at init time.
//
// Transport-specific rules:
//   - Empty / "stdio": Command[0] must be non-empty. URL and
//     HeadersTemplate are tolerated but unused (a stdio recipe with a
//     stray URL is a noop, not an error — keeps the import path more
//     forgiving).
//   - "http" / "sse": URL must be set, must parse, must use http or
//     https scheme, must not carry a fragment, and must not embed
//     userinfo (credentials route through HeadersTemplate). For "sse"
//     PostURL must additionally be set and validated identically.
//   - Any other transport string is rejected.
func (r *Recipe) Validate() error {
	if err := ValidateRecipeID(r.ID); err != nil {
		return err
	}
	transport := r.Transport
	if transport == "" {
		transport = TransportStdio
	}
	switch transport {
	case TransportStdio:
		if len(r.Command) == 0 || r.Command[0] == "" {
			return fmt.Errorf("%w: recipe %q has empty Command", ErrInvalidRecipe, r.ID)
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(r.URL) == "" {
			return fmt.Errorf("%w: recipe %q transport %q requires URL", ErrInvalidRecipe, r.ID, transport)
		}
		if err := validateRecipeURL(r.URL); err != nil {
			return fmt.Errorf("%w: recipe %q: %v", ErrInvalidRecipe, r.ID, err)
		}
		if transport == TransportSSE {
			if strings.TrimSpace(r.PostURL) == "" {
				return fmt.Errorf("%w: recipe %q transport %q requires PostURL", ErrInvalidRecipe, r.ID, transport)
			}
			if err := validateRecipeURL(r.PostURL); err != nil {
				return fmt.Errorf("%w: recipe %q post_url: %v", ErrInvalidRecipe, r.ID, err)
			}
		}
	default:
		return fmt.Errorf("%w: recipe %q has unknown transport %q", ErrInvalidRecipe, r.ID, transport)
	}
	for i, k := range r.EnvKeys {
		if k.Name == "" {
			return fmt.Errorf("%w: recipe %q env_keys[%d] has empty Name", ErrInvalidRecipe, r.ID, i)
		}
	}
	switch r.PreSeedingPolicy {
	case "", "allow_all", "prompt_only", "none":
		// valid
	default:
		return fmt.Errorf("%w: recipe %q has invalid pre_seeding_policy %q (want \"\", \"allow_all\", \"prompt_only\", or \"none\")", ErrInvalidRecipe, r.ID, r.PreSeedingPolicy)
	}
	switch r.PrimaryAuth {
	case "", PrimaryAuthOAuth, PrimaryAuthDeviceCode, PrimaryAuthKeys, PrimaryAuthNone,
		PrimaryAuthBrowserOAuthDCR, PrimaryAuthBrowserOAuthPKCE:
		// valid
	default:
		return fmt.Errorf("%w: recipe %q has invalid primary_auth %q (want \"\", \"oauth\", \"device_code\", \"keys\", \"none\", \"browser_oauth_dcr\", or \"browser_oauth_pkce\")", ErrInvalidRecipe, r.ID, r.PrimaryAuth)
	}
	return nil
}

// validateRecipeURL enforces the URL constraints documented on
// Recipe.URL. The URL is allowed to contain ${VAR} substitution
// tokens — those are stripped before parsing so the validator does
// not fight the recipe author who routes part of the path through a
// keychain-sourced env var. Token substitution is re-validated at
// connection-open time.
func validateRecipeURL(raw string) error {
	probe := tokenPattern.ReplaceAllString(raw, "x")
	u, err := url.Parse(probe)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("invalid URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid URL %q: host required", raw)
	}
	if u.Fragment != "" || strings.Contains(probe, "#") {
		return fmt.Errorf("invalid URL %q: fragment not permitted", raw)
	}
	if u.User != nil {
		return fmt.Errorf("invalid URL %q: userinfo not permitted (use headers_template)", raw)
	}
	return nil
}

// ToServerSpec produces the mcp.ServerSpec the pool consumes when
// spawning this recipe. env is the resolved env-var map (typically
// produced by ResolveEnv); config is the per-install user config
// (typically EnabledRecipe.Config) used to expand any ${VAR} tokens
// in ArgsTemplate. Both arguments are nil-tolerant.
//
// When ArgsTemplate is empty and config is nil, the function behaves
// identically to the pre-WP01 single-arg version: Command is copied
// verbatim and no substitution runs. When ArgsTemplate has entries,
// recognised tokens are expanded:
//
//   - ${DATA_DIR}: scalar; pulled from config["data_dir"] (string).
//   - ${ALLOWED_DIRS}: list; pulled from config["allowed_directories"]
//     ([]string or []any of strings). Each element becomes a separate
//     argv element in the resulting Command, NOT one space-joined arg.
//
// The returned ServerSpec uses Transport "stdio" and a fresh slice
// for Command (callers may mutate the input recipe's Command without
// disturbing the spec).
func (r *Recipe) ToServerSpec(env map[string]string, config map[string]any) mcp.ServerSpec {
	// Always build substitution vars so ${HARNESS_EXE} (and any other scalar
	// tokens) in Command are expanded even when ArgsTemplate is empty.
	// This is safe for existing recipes whose Command contains no tokens —
	// Substitute is a no-op for token-free strings.
	vars, listVars := buildSubstitutionVars(config)
	expandedCmd := Substitute(r.Command, vars, listVars)
	cmd := make([]string, 0, len(expandedCmd)+len(r.ArgsTemplate))
	cmd = append(cmd, expandedCmd...)

	if len(r.ArgsTemplate) > 0 {
		cmd = append(cmd, Substitute(r.ArgsTemplate, vars, listVars)...)
	}

	var envCopy map[string]string
	if len(env) > 0 {
		envCopy = make(map[string]string, len(env))
		for k, v := range env {
			envCopy[k] = v
		}
	}

	transport := r.Transport
	if transport == "" {
		transport = TransportStdio
	}

	// Propagate the per-request header template + SSE post URL for remote
	// (http/sse) recipes. Without this, an http recipe's HeadersTemplate
	// (e.g. an Authorization bearer for an OAuth remote server) would be
	// silently dropped and the connection would never authenticate. Values
	// may carry ${ENV_VAR} tokens that the transport factory substitutes from
	// Env at connection-open time.
	var headers map[string]string
	if len(r.HeadersTemplate) > 0 {
		headers = make(map[string]string, len(r.HeadersTemplate))
		for k, v := range r.HeadersTemplate {
			headers[k] = v
		}
	}

	return mcp.ServerSpec{
		Name:            r.ID,
		Transport:       transport,
		Command:         cmd,
		URL:             r.URL,
		PostURL:         r.PostURL,
		Env:             envCopy,
		HeadersTemplate: headers,
	}
}

// buildSubstitutionVars projects an EnabledRecipe.Config map onto the
// scalar/list var maps Substitute consumes. The mapping is intentionally
// small and explicit — every recognised config key is named here, so
// adding a new substitution token is a one-line change in this file
// rather than a generic any-typed-walk.
//
// String-typed values land in vars; []string and []any-of-string land
// in listVars. Other shapes are skipped (Substitute will leave the
// token literal and warn-log).
//
// ${HARNESS_EXE} is resolved at call time via os.Executable() + symlink
// following. If os.Executable() fails, the empty string is used; spawn-spec
// validation will reject an empty Command[0].
func buildSubstitutionVars(config map[string]any) (map[string]string, map[string][]string) {
	vars := map[string]string{}
	listVars := map[string][]string{}

	// ${HARNESS_EXE}: resolve the running binary path (symlink-followed) so
	// recipes using ["${HARNESS_EXE}", "mcp", "sites"] spawn the right binary.
	if exe, err := resolveHarnessExe(); err == nil {
		vars["HARNESS_EXE"] = exe
	}

	if config == nil {
		return vars, listVars
	}
	if v, ok := config["data_dir"].(string); ok {
		vars["DATA_DIR"] = v
	}
	if raw, present := config["allowed_directories"]; present {
		if list, ok := coerceStringSlice(raw); ok {
			listVars["ALLOWED_DIRS"] = list
		}
	}
	return vars, listVars
}

// ErrHarnessExeNotFound is returned by resolveHarnessExe when the resolved
// executable path does not exist on disk. Spawn-spec validation will surface
// a typed error to the caller rather than a confusing "command not found" from
// the OS at subprocess start time.
var ErrHarnessExeNotFound = errors.New("recipes: ${HARNESS_EXE}: resolved executable not found on disk")

// resolveHarnessExe returns the symlink-followed absolute path of the
// currently running executable. Exposed as a var so tests can stub it.
// Returns ErrHarnessExeNotFound if the resolved path does not exist.
var resolveHarnessExe = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("recipes: ${HARNESS_EXE}: os.Executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe // use unresolved path if symlink evaluation fails
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		return "", fmt.Errorf("%w: %s", ErrHarnessExeNotFound, resolved)
	}
	return resolved, nil
}

// RecipeDirs extracts the union of all "allowed_directories" config
// values declared across the supplied recipes. It is the canonical
// source-of-truth builder that the fs gate's NotifyRecipeDirs call
// site invokes at recipe-install / recipe-uninstall time.
//
// Each recipe may declare zero or more ConfigOption entries with
// Kind == ConfigKindDirectoryList whose Default field holds a
// pre-configured list of allowed directories. When a recipe has no
// such option the recipe contributes nothing to the result.
//
// The returned slice is deduplicated (last-write-wins within a single
// recipe; across recipes all unique entries are retained). Callers
// should pass the combined enabled-recipe set; passing a partial list
// replaces the prior full set.
//
// This function is intentionally minimal (one exported symbol, no new
// struct fields) per the WP04 constraint.
func RecipeDirs(recipes []Recipe) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, r := range recipes {
		for _, opt := range r.ConfigOptions {
			if opt.Kind != ConfigKindDirectoryList {
				continue
			}
			if opt.Default == nil {
				continue
			}
			list, ok := coerceStringSlice(opt.Default)
			if !ok {
				continue
			}
			for _, d := range list {
				if d == "" {
					continue
				}
				if _, dup := seen[d]; dup {
					continue
				}
				seen[d] = struct{}{}
				out = append(out, d)
			}
		}
	}
	return out
}

// coerceStringSlice accepts the two shapes a JSON-loaded config may
// surface for a list field: a typed []string (from a Go-side caller)
// or a []any of strings (from json.Unmarshal into map[string]any).
// Anything else returns (nil, false).
func coerceStringSlice(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
