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
	"regexp"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/mcp"
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
	// Category groups recipes in the modal ("search", "filesystem",
	// "memory", "fetch", ...).
	Category string `json:"category"`
	// Command is the argv used to spawn the stdio server.
	// Command[0] must be non-empty for stdio recipes.
	Command []string `json:"command"`
	// EnvKeys are the credential-bearing env vars the server reads.
	// Slice order is render order in the install modal.
	EnvKeys []EnvKey `json:"env_keys"`
	// Transport names the wire protocol the recipe speaks. Empty (or
	// "stdio") preserves backwards compatibility with the shipped
	// stdio catalog. Recognised values: "stdio", "http", "sse".
	Transport string `json:"transport,omitempty" yaml:"transport,omitempty"`
	// URL is the endpoint for HTTP/SSE recipes. Required when
	// Transport is non-stdio. Subject to ${VAR} env-var substitution
	// at connection-open time so credentials in the path are sourced
	// from the keychain rather than the YAML on disk. Must be
	// http:// or https://, with no fragment and no userinfo —
	// credentials flow exclusively through HeadersTemplate.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// HeadersTemplate is the per-request HTTP header set the
	// transport applies on every outbound POST. Values are subject
	// to the same ${VAR} substitution as ArgsTemplate so a recipe
	// can declare e.g. `Authorization: "Bearer ${GITHUB_TOKEN}"`
	// and have the env-var resolved from the keychain at Open time.
	// Header names should be canonical-cased; the transport leaves
	// them untouched so the server sees what the recipe author
	// wrote.
	HeadersTemplate map[string]string `json:"headers_template,omitempty" yaml:"headers_template,omitempty"`
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
	// Warning is an optional user-facing hazard message rendered by the
	// install modal in a stark style (red banner, explicit confirmation
	// checkbox). Used by recipes that grant elevated trust — e.g. the
	// unrestricted filesystem MCP. Empty for ordinary recipes.
	Warning string `json:"warning,omitempty"`
	// RecommendedPolicyTemplate names a Cedar policy file under
	// `core/policy/cedar/policies/` that the user is encouraged to
	// install alongside this recipe. The install modal surfaces a
	// "copy recommended policy" affordance when set.
	RecommendedPolicyTemplate string `json:"recommended_policy_template,omitempty"`
	// Source tags the loader that produced this Recipe. It is set by
	// loaders (LoadShipped → SourceShipped, registry loader →
	// SourceRegistry, UserStore → SourceUser/SourceImported) and is
	// never persisted on disk: both the JSON and YAML codecs skip the
	// field. Consumers use it to render badges and to drive the
	// MergedCatalog precedence rules.
	Source string `json:"-" yaml:"-"`
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
	Name        string `json:"name"`
	Display     string `json:"display"`
	Kind        string `json:"kind"`
	Default     any    `json:"default,omitempty"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// Recognised values for ConfigOption.Kind.
const (
	ConfigKindDirectoryList = "directory_list"
	ConfigKindBoolean       = "boolean"
	ConfigKindString        = "string"
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
//     userinfo (credentials route through HeadersTemplate).
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
	default:
		return fmt.Errorf("%w: recipe %q has unknown transport %q", ErrInvalidRecipe, r.ID, transport)
	}
	for i, k := range r.EnvKeys {
		if k.Name == "" {
			return fmt.Errorf("%w: recipe %q env_keys[%d] has empty Name", ErrInvalidRecipe, r.ID, i)
		}
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
	cmd := make([]string, 0, len(r.Command)+len(r.ArgsTemplate))
	cmd = append(cmd, r.Command...)

	if len(r.ArgsTemplate) > 0 {
		vars, listVars := buildSubstitutionVars(config)
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

	return mcp.ServerSpec{
		Name:      r.ID,
		Transport: transport,
		Command:   cmd,
		URL:       r.URL,
		Env:       envCopy,
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
func buildSubstitutionVars(config map[string]any) (map[string]string, map[string][]string) {
	vars := map[string]string{}
	listVars := map[string][]string{}
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
