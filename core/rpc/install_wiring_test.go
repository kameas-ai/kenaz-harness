package rpc

// install-framework-01DOGF0B WP04: the per-kind MCP bindings and the fleet
// MCP sync applier route through install.Framework. Without these, a
// binding reverted to call the tools view directly would leave every
// framework unit test green while installs stopped emitting
// capability:installed and stopped being consumer-checked — the exact
// "wired in tests, unwired in New()" shape the catalog skill-state wiring
// test (api_catalog_skill_state_wiring_test.go) exists for.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	capabilitiesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
)

// TestChassis_ToolsInstallRecipe_RoutesThroughInstallFramework boots a real
// chassis and drives the Wails binding. A required-key recipe installed with
// no key fails with install.ErrRequirementsUnmet — the framework's own
// pre-install check, raised before the tools view is reached. Revert the
// binding to b.api.Tools().InstallRecipe and the error becomes the tools
// view's "required env key … is missing" instead, failing this test.
func TestChassis_ToolsInstallRecipe_RoutesThroughInstallFramework(t *testing.T) {
	sandboxUserConfigDir(t)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	b := NewBindings(api)

	if api.Capabilities().Framework() == nil {
		t.Fatal("New() did not construct the install framework")
	}
	if got := api.Capabilities().Framework().Registered(); len(got) == 0 || got[0] != install.KindMCPRecipe {
		t.Fatalf("registered kinds = %v, want mcp_recipe registered by New()", got)
	}

	// Find a registry recipe with a required key in the real merged catalog.
	listing, err := b.Capability_List(install.Filter{Kind: install.KindMCPRecipe})
	if err != nil {
		t.Fatalf("Capability_List: %v", err)
	}
	var keyed string
	for _, it := range listing.Items {
		if it.State.Installed {
			t.Fatalf("fresh profile lists %q installed", it.ID)
		}
		for _, r := range it.Requirements {
			if r.Kind == install.RequirementKey && r.Required && !r.Satisfied && keyed == "" {
				keyed = it.ID
			}
		}
	}
	if keyed == "" {
		t.Fatal("no recipe with a required key in the shipped catalog — the routing probe has nothing to install")
	}

	_, err = b.Tools_InstallRecipe(keyed, map[string]string{}, nil)
	if !errors.Is(err, install.ErrRequirementsUnmet) {
		t.Fatalf("Tools_InstallRecipe(%s) err = %v, want install.ErrRequirementsUnmet (the binding must route through the framework)", keyed, err)
	}
}

// fakeRecipeTools is a minimal tools.ToolsAPI for the sync-applier routing
// test: only the methods the MCP provider and the applier use are real;
// the embedded nil interface panics on anything else, so an unexpected call
// is loud. Race-safe.
type fakeRecipeTools struct {
	tools.ToolsAPI
	mu      sync.Mutex
	enabled map[string]bool
}

func (f *fakeRecipeTools) ListRecipes(context.Context) ([]tools.RecipeListing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []tools.RecipeListing{{
		Recipe:  recipes.Recipe{ID: "fetch", DisplayName: "Fetch"},
		Enabled: f.enabled["fetch"],
		Source:  recipes.SourceShipped,
	}}, nil
}

func (f *fakeRecipeTools) InstallRecipe(_ context.Context, id string, _ map[string]string, _ map[string]any) (stdio.RecipeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[id] = true
	return stdio.RecipeStatus{ID: id, Enabled: true, State: "running"}, nil
}

func (f *fakeRecipeTools) UninstallRecipe(context.Context, string) error { return nil }

type capturePublisher struct {
	mu     sync.Mutex
	topics []string
}

func (c *capturePublisher) Emit(topic string, _ any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topics = append(c.topics, topic)
}

func (c *capturePublisher) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.topics...)
}

// TestSyncMCPApply_InstallsThroughTheFramework pins the one production
// caller of ToolsAPI.InstallRecipe outside the bindings: a recipe a fleet
// sync pull applies is installed through the framework (consumer check +
// capability:installed), via the frameworkRoutedTools wrapper api.go hands
// newToolsMCPRegistry.
func TestSyncMCPApply_InstallsThroughTheFramework(t *testing.T) {
	ft := &fakeRecipeTools{enabled: map[string]bool{}}
	pub := &capturePublisher{}
	fw := install.New(pub, nil)
	if err := fw.Register(install.KindMCPRecipe, capabilitiesview.NewMCPProvider(ft)); err != nil {
		t.Fatal(err)
	}
	reg := newToolsMCPRegistry(frameworkRoutedTools{ToolsAPI: ft, fw: fw})
	if err := reg.ApplyInstalled([]corefleet.InstalledMCP{{RecipeID: "fetch", EnabledState: true}}); err != nil {
		t.Fatalf("ApplyInstalled: %v", err)
	}
	got := pub.snapshot()
	if len(got) != 1 || got[0] != install.TopicCapabilityInstalled {
		t.Fatalf("events = %v, want one %s", got, install.TopicCapabilityInstalled)
	}
}

// TestObserveRecipeFlow_ReauthEmitsNoSecondInstall — review: an OAuth
// re-sign-in (or device-code re-approval) of an already-enabled recipe is
// not a new install; a first sign-in that enables it is.
func TestObserveRecipeFlow_ReauthEmitsNoSecondInstall(t *testing.T) {
	ft := &fakeRecipeTools{enabled: map[string]bool{}}
	pub := &capturePublisher{}
	fw := install.New(pub, nil)
	if err := fw.Register(install.KindMCPRecipe, capabilitiesview.NewMCPProvider(ft)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	signIn := func() (stdio.RecipeStatus, error) { return ft.InstallRecipe(ctx, "fetch", nil, nil) }

	if _, err := observeRecipeFlow(ctx, fw, "fetch", signIn); err != nil {
		t.Fatal(err)
	}
	if got := pub.snapshot(); len(got) != 1 || got[0] != install.TopicCapabilityInstalled {
		t.Fatalf("first sign-in events = %v, want one capability:installed", got)
	}
	if _, err := observeRecipeFlow(ctx, fw, "fetch", signIn); err != nil {
		t.Fatal(err)
	}
	if got := pub.snapshot(); len(got) != 1 {
		t.Fatalf("re-auth emitted again: %v", got)
	}
}
