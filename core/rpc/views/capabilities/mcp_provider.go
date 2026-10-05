package capabilities

import (
	"context"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
)

// RecipeInstaller is the slice of tools.ToolsAPI the MCP provider adapts:
// the recipe install path (Tools_InstallRecipe's implementation) and the
// listing whose Enabled flag is the MCP supervisor's persisted enabled list
// — the consumer the bootstrap reopens recipes from.
type RecipeInstaller interface {
	ListRecipes(ctx context.Context) ([]tools.RecipeListing, error)
	InstallRecipe(ctx context.Context, id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error)
	UninstallRecipe(ctx context.Context, id string) error
}

// MCPProvider is install.Provider for MCP recipes (WP04): an adapter over
// the existing recipe install path, not a second implementation of it.
type MCPProvider struct {
	tools RecipeInstaller
}

// NewMCPProvider adapts t.
func NewMCPProvider(t RecipeInstaller) *MCPProvider { return &MCPProvider{tools: t} }

var _ install.Provider = (*MCPProvider)(nil)

// mcpConsumer names the consumer that confirms MCP state.
const mcpConsumer = "MCP supervisor"

// Kind implements install.Provider.
func (p *MCPProvider) Kind() install.Kind { return install.KindMCPRecipe }

// List implements install.Provider.
func (p *MCPProvider) List(ctx context.Context, _ install.Filter) (install.Listing, error) {
	listings, err := p.tools.ListRecipes(ctx)
	if err != nil {
		return install.Listing{}, err
	}
	out := install.Listing{Items: make([]install.Item, 0, len(listings))}
	for _, l := range listings {
		out.Items = append(out.Items, recipeItem(l))
	}
	return out, nil
}

// Detail implements install.Provider.
func (p *MCPProvider) Detail(ctx context.Context, id string) (install.Item, error) {
	l, err := p.listing(ctx, id)
	if err != nil {
		return install.Item{}, err
	}
	return recipeItem(l), nil
}

// Requirements implements install.Provider.
func (p *MCPProvider) Requirements(ctx context.Context, id string) ([]install.Requirement, error) {
	l, err := p.listing(ctx, id)
	if err != nil {
		return nil, err
	}
	return recipeRequirements(l), nil
}

// Verify implements install.Provider. Shipped and curated-registry recipes
// are part of the signed app binary; user-authored, imported and
// org-provisioned recipes are local (an org recipe arrives inside a fleet
// config bundle, which the bundle applier verifies on its own path).
func (p *MCPProvider) Verify(ctx context.Context, ref install.Ref) (install.Verification, error) {
	l, err := p.listing(ctx, ref.ID)
	if err != nil {
		return install.Verification{}, err
	}
	switch recipeSource(l.Source) {
	case install.SourceBuiltin, install.SourceRegistry:
		return install.Verification{Method: install.VerifyBuiltin}, nil
	default:
		return install.Verification{Method: install.VerifyLocal}, nil
	}
}

// Install implements install.Provider: the existing recipe install (keys to
// the keychain, enabled-list save, supervisor spawn). Returns the
// supervisor's status snapshot, which Tools_InstallRecipe hands back.
func (p *MCPProvider) Install(ctx context.Context, req install.InstallRequest) (any, error) {
	env := req.Inputs.Secrets
	if env == nil {
		env = map[string]string{}
	}
	return p.tools.InstallRecipe(ctx, req.Ref.ID, env, req.Inputs.Config)
}

// Uninstall implements install.Provider.
func (p *MCPProvider) Uninstall(ctx context.Context, id string) error {
	return p.tools.UninstallRecipe(ctx, id)
}

// InstalledState implements install.Provider: read from the supervisor's
// enabled list (ListRecipes' Enabled overlay), with the live process state
// as detail.
func (p *MCPProvider) InstalledState(ctx context.Context, id string) (install.State, error) {
	l, err := p.listing(ctx, id)
	if err != nil {
		return install.State{}, err
	}
	return recipeState(l), nil
}

// Update implements install.Provider. Recipes are versioned with the app:
// a new recipe definition arrives with an app update, not as an install.
func (p *MCPProvider) Update(_ context.Context, id string) (install.Ref, error) {
	return install.Ref{}, fmt.Errorf("%w: MCP recipe %q updates with the app", install.ErrNoUpdate, id)
}

func (p *MCPProvider) listing(ctx context.Context, id string) (tools.RecipeListing, error) {
	listings, err := p.tools.ListRecipes(ctx)
	if err != nil {
		return tools.RecipeListing{}, err
	}
	for _, l := range listings {
		if l.Recipe.ID == id {
			return l, nil
		}
	}
	return tools.RecipeListing{}, fmt.Errorf("%w: MCP recipe %q", install.ErrNotFound, id)
}

// recipeSource maps the recipe catalog layer onto the FR-4 source filter.
func recipeSource(s string) install.Source {
	switch s {
	case recipes.SourceShipped:
		return install.SourceBuiltin
	case recipes.SourceRegistry:
		return install.SourceRegistry
	case recipes.SourceOrg:
		return install.SourceOrgCatalog
	default: // user, imported
		return install.SourceLocal
	}
}

func recipeState(l tools.RecipeListing) install.State {
	return install.State{
		Installed: l.Enabled,
		Consumer:  mcpConsumer,
		Detail:    l.Status.State,
	}
}

func recipeItem(l tools.RecipeListing) install.Item {
	r := l.Recipe
	it := install.Item{
		Kind:         install.KindMCPRecipe,
		ID:           r.ID,
		Name:         r.DisplayName,
		Description:  r.Description,
		Category:     r.Category,
		Source:       recipeSource(l.Source),
		Keywords:     append([]string(nil), r.Aliases...),
		State:        recipeState(l),
		Requirements: recipeRequirements(l),
	}
	if it.Name == "" {
		it.Name = r.ID
	}
	if l.Source == recipes.SourceOrg {
		// Org-provisioned recipes are re-applied by every bundle poll; a
		// local uninstall would not stick (spec §2.2, FR-302 parity).
		it.ReadOnly = true
		it.ReadOnlyReason = "Provisioned by your org"
	}
	return it
}

// recipeRequirements declares what the MCP key-prompt flow collects.
// Satisfied is what the device already holds: KeysPresent for keys and
// OAuth (the keychain resolves them), the persisted config of an enabled
// recipe for config options and the warning acknowledgement.
func recipeRequirements(l tools.RecipeListing) []install.Requirement {
	r := l.Recipe
	var out []install.Requirement
	for _, k := range r.EnvKeys {
		out = append(out, install.Requirement{
			Kind: install.RequirementKey, Name: k.Name, Display: k.Display,
			Required: k.Required, Satisfied: l.KeysPresent,
		})
	}
	for _, o := range r.ConfigOptions {
		kind := install.RequirementConfig
		if o.Kind == recipes.ConfigKindDirectoryList {
			kind = install.RequirementDirectory
		}
		out = append(out, install.Requirement{
			Kind: kind, Name: o.Name, Display: o.Display,
			// An option with a default is satisfiable without input.
			Required:  o.Required && o.Default == nil,
			Satisfied: l.Enabled,
		})
	}
	if r.Auth != nil && r.Auth.Kind == recipes.AuthKindMCPOAuth {
		out = append(out, install.Requirement{
			Kind: install.RequirementOAuth, Name: "oauth", Display: "Sign in",
			Satisfied: l.KeysPresent,
		})
	}
	if r.Warning != "" {
		out = append(out, install.Requirement{
			Kind: install.RequirementConsent, Name: "warning", Display: r.Warning,
			Required: true, Satisfied: l.Enabled,
		})
	}
	return out
}
