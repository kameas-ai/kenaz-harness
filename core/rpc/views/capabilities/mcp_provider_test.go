package capabilities_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/install"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// supervisorPool stands in for the transport pool (no subprocess). The
// consumer this test asserts on is NOT the pool — it is the persisted
// enabled list the MCP supervisor's boot path reopens recipes from
// (recipes.LoadEnabled), read through a fresh loader, plus the tools
// view's own listing. Race-safe: the framework may call it concurrently.
type supervisorPool struct {
	mu     sync.Mutex
	status map[string]stdio.RecipeStatus
}

func newSupervisorPool() *supervisorPool {
	return &supervisorPool{status: map[string]stdio.RecipeStatus{}}
}

func (p *supervisorPool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status[spec.Name] = stdio.RecipeStatus{ID: spec.Name, Enabled: true, State: string(stdio.StateRunning)}
	return nil
}

func (p *supervisorPool) CloseOne(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.status, id)
	return nil
}

func (p *supervisorPool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.status[id]
	return s, ok
}

func (p *supervisorPool) ServerTools(string) []coremcp.Tool { return nil }

type recordingPublisher struct {
	mu  sync.Mutex
	evs []install.Event
	tps []string
}

func (r *recordingPublisher) Emit(topic string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tps = append(r.tps, topic)
	r.evs = append(r.evs, payload.(install.Event))
}

func (r *recordingPublisher) snapshot() ([]string, []install.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tps...), append([]install.Event(nil), r.evs...)
}

type mcpFixture struct {
	dataDir string
	tools   *tools.API
	pool    *supervisorPool
	api     *capabilities.API
	pub     *recordingPublisher
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	dataDir := t.TempDir()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{
		{ID: "fetch", DisplayName: "Fetch", Description: "Fetch URLs", Category: "web",
			Command: []string{"kenaz-test-fetch-server"}, Aliases: []string{"http-get"}, Source: recipes.SourceShipped},
		{ID: "brave", DisplayName: "Brave Search", Command: []string{"kenaz-test-brave-server"},
			EnvKeys: []recipes.EnvKey{{Name: "BRAVE_API_KEY", Display: "Brave API key", Required: true}},
			Source:  recipes.SourceRegistry},
		{ID: "org-wiki", DisplayName: "Org wiki", Command: []string{"kenaz-test-wiki-server"}, Source: recipes.SourceOrg},
	}}
	pool := newSupervisorPool()
	toolsAPI := tools.New(tools.Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})
	pub := &recordingPublisher{}
	fw := install.New(pub, nil)
	if err := fw.Register(install.KindMCPRecipe, capabilities.NewMCPProvider(toolsAPI)); err != nil {
		t.Fatal(err)
	}
	return &mcpFixture{dataDir: dataDir, tools: toolsAPI, pool: pool, api: capabilities.New(fw), pub: pub}
}

// persistedEnabled reads the enabled list the way the boot-time bootstrap
// does — a fresh load from disk, not the in-memory struct the install
// mutated.
func (f *mcpFixture) persistedEnabled(t *testing.T, id string) bool {
	t.Helper()
	en, err := recipes.LoadEnabled(f.dataDir)
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	_, ok := en.Get(id)
	return ok
}

func (f *mcpFixture) row(t *testing.T, id string) install.Item {
	t.Helper()
	l, err := f.api.List(context.Background(), install.Filter{Kind: install.KindMCPRecipe})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range l.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("List has no row %q", id)
	return install.Item{}
}

// P-3 (mcp): install → the consumer lists it; uninstall → the consumer drops
// it; the installed badge on the Capability_List row is derived from that
// consumer state. Named for scripts/ci/check-install-provider-coverage.sh.
func TestInstallProvider_MCPRecipe_ConsumerSeesInstall(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()

	if f.row(t, "fetch").State.Installed {
		t.Fatal("fetch reads installed before any install")
	}

	got, err := f.api.Install(ctx, string(install.KindMCPRecipe), "fetch", "")
	if err != nil {
		t.Fatalf("Capability install: %v", err)
	}
	if !got.State.Installed || got.State.Consumer != "MCP supervisor" {
		t.Fatalf("returned row = %+v", got)
	}
	if !f.persistedEnabled(t, "fetch") {
		t.Fatal("the supervisor's persisted enabled list does not contain fetch after install")
	}
	listings, _ := f.tools.ListRecipes(ctx)
	for _, l := range listings {
		if l.Recipe.ID == "fetch" && !l.Enabled {
			t.Fatal("Tools_ListRecipes does not show fetch enabled after install")
		}
	}
	if row := f.row(t, "fetch"); !row.State.Installed || row.State.Detail != string(stdio.StateRunning) {
		t.Fatalf("Capability_List badge not derived from the consumer: %+v", row.State)
	}

	if err := f.api.Uninstall(ctx, string(install.KindMCPRecipe), "fetch"); err != nil {
		t.Fatalf("Capability uninstall: %v", err)
	}
	if f.persistedEnabled(t, "fetch") {
		t.Fatal("persisted enabled list still contains fetch after uninstall")
	}
	if f.row(t, "fetch").State.Installed {
		t.Fatal("Capability_List still paints fetch installed after uninstall")
	}

	topics, evs := f.pub.snapshot()
	if len(topics) != 2 || topics[0] != install.TopicCapabilityInstalled || topics[1] != install.TopicCapabilityUninstalled {
		t.Fatalf("events = %v", topics)
	}
	if evs[0].VerifyMethod != install.VerifyBuiltin || !evs[0].Verified {
		t.Fatalf("a shipped recipe verifies as part of the binary: %+v", evs[0])
	}
}

func TestMCPProvider_RequiredKeyMissing_RefusedBeforeSideEffects(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	fw := f.api.Framework()

	_, err := fw.Install(ctx, install.Ref{Kind: install.KindMCPRecipe, ID: "brave"}, install.Inputs{})
	if !errors.Is(err, install.ErrRequirementsUnmet) {
		t.Fatalf("got %v, want ErrRequirementsUnmet", err)
	}
	if f.persistedEnabled(t, "brave") {
		t.Fatal("a refused install touched the enabled list")
	}
	if _, ok := f.pool.RecipeStatus("brave"); ok {
		t.Fatal("a refused install spawned the server")
	}
}

func TestMCPProvider_ListMapsSourcesRequirementsAndReadOnly(t *testing.T) {
	f := newMCPFixture(t)
	brave := f.row(t, "brave")
	if brave.Source != install.SourceRegistry {
		t.Fatalf("brave source = %q", brave.Source)
	}
	if len(brave.Requirements) != 1 || brave.Requirements[0].Kind != install.RequirementKey ||
		!brave.Requirements[0].Required || brave.Requirements[0].Satisfied {
		t.Fatalf("brave requirements = %+v", brave.Requirements)
	}
	fetch := f.row(t, "fetch")
	if fetch.Source != install.SourceBuiltin || len(fetch.Keywords) != 1 || fetch.Category != "web" {
		t.Fatalf("fetch row = %+v", fetch)
	}
	org := f.row(t, "org-wiki")
	if org.Source != install.SourceOrgCatalog || !org.ReadOnly || org.ReadOnlyReason == "" {
		t.Fatalf("org row = %+v", org)
	}

	// Search reaches aliases.
	l, err := f.api.List(context.Background(), install.Filter{Query: "http-get"})
	if err != nil || len(l.Items) != 1 || l.Items[0].ID != "fetch" {
		t.Fatalf("alias search = %+v, %v", l.Items, err)
	}
}

func TestMCPProvider_OrgRecipeUninstallRefused(t *testing.T) {
	f := newMCPFixture(t)
	ctx := context.Background()
	if _, err := f.api.Install(ctx, string(install.KindMCPRecipe), "org-wiki", ""); err != nil {
		t.Fatalf("install org recipe: %v", err)
	}
	err := f.api.Uninstall(ctx, string(install.KindMCPRecipe), "org-wiki")
	if !errors.Is(err, install.ErrReadOnly) {
		t.Fatalf("got %v, want ErrReadOnly", err)
	}
	if !f.persistedEnabled(t, "org-wiki") {
		t.Fatal("a refused uninstall removed the org recipe")
	}
}

func TestMCPProvider_UpdateIsNotAnInstall(t *testing.T) {
	f := newMCPFixture(t)
	if _, err := f.api.Update(context.Background(), string(install.KindMCPRecipe), "fetch"); !errors.Is(err, install.ErrNoUpdate) {
		t.Fatalf("got %v, want ErrNoUpdate", err)
	}
}
