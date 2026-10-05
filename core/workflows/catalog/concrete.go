package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"

	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// RecipeRegistry is the narrow read-only interface the catalog uses to
// decide which mcp_call.server references are already configured.
//
// The production implementation (core/rpc's wfRecipeRegistryAdapter) is
// backed by recipes.EnabledRecipes — the installed-server list — NOT
// *recipes.MergedCatalog, which was the wrong registry: MergedCatalog is
// the full shipped+registry+user catalog (installed or not), so a Has()
// answering from it would report every cataloged recipe as configured
// regardless of install state (automation-actually-runs-01PMZ404
// UNIT-10, spec X-2 / X-8).
type RecipeRegistry interface {
	// Has reports whether serverName is an installed (enabled) recipe.
	Has(serverName string) bool
}

// Config bundles the dependencies of the concrete catalog.
type Config struct {
	// Store persists installed workflows. Required for Install; nil
	// returns ErrStoreUnavailable on Install.
	Store corewf.Store
	// Scheduler, when non-nil, arms cron schedules for workflows that
	// carry schedule+timezone fields. nil silently skips scheduling.
	Scheduler wfsched.Scheduler
	// RecipeRegistry, when non-nil, is used to detect missing credentials.
	// Credentials for mcp_call steps that reference a serverName not in
	// the registry are reported in InstalledRef.MissingCredentials.
	//
	// What "missing credentials" actually measures (automation-actually-
	// runs-01PMZ404 UNIT-10): whether the server is INSTALLED (an
	// EnabledRecipes entry), not whether its keychain-backed credential
	// values are still present right now. Install requires every
	// Required EnvKey to be supplied before the recipe is enabled
	// (core/rpc/views/tools/impl.go's InstallRecipe), so "installed"
	// is a true statement about credentials at install time; it does not
	// catch a credential later revoked or deleted from the keychain
	// out-of-band. Renaming the wire field to something more precise
	// (e.g. MissingServers) needs a `wails generate module` bindings
	// regen this mission's sandbox explicitly may not run — tracked as a
	// follow-up, not resolved here.
	RecipeRegistry RecipeRegistry
	// Provenance, when non-nil, records every template install (source
	// "builtin", the shipped version) so List can report
	// "installed_outdated" when the binary ships a newer version
	// (install-framework-01DOGF0B WP05 review H4). nil: installs are not
	// recorded and no update is ever offered.
	Provenance corewf.ProvenanceStore
	// Builtins are the shipped templates. rpc.New passes the list the
	// workflows view also lists; tests pass a fixture whose version is
	// bumped between two catalogs. nil → corewf.LoadBuiltins.
	Builtins []corewf.Workflow
}

// ErrStoreUnavailable is returned by Install when no Store is wired.
var ErrStoreUnavailable = errStore("catalog: storage unavailable")

type errStore string

func (e errStore) Error() string { return string(e) }

// concreteCatalog is the production Catalog backed by the builtin FS
// + the user Store.
type concreteCatalog struct {
	cfg   Config
	mu    sync.RWMutex
	byID  map[string]corewf.Workflow
}

// New returns a Catalog backed by the builtin embedded YAML files and
// optionally the user store (for install-status reflection).
//
// LoadBuiltins errors are silently swallowed for robustness; a catalog
// that fails individual YAML parsing degrades gracefully to an
// incomplete list rather than crashing the chassis.
func New(cfg Config) Catalog {
	builtins := cfg.Builtins
	if builtins == nil {
		builtins, _ = corewf.LoadBuiltins()
	}
	c := &concreteCatalog{
		cfg:  cfg,
		byID: make(map[string]corewf.Workflow, len(builtins)),
	}
	for _, w := range builtins {
		c.byID[w.ID] = w
	}
	return c
}

// List implements Catalog.
func (c *concreteCatalog) List(ctx context.Context) ([]Entry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Collect installed ids so we can set InstallStatus.
	installed := make(map[string]bool)
	if c.cfg.Store != nil {
		if sums, err := c.cfg.Store.List(ctx); err == nil {
			for _, s := range sums {
				installed[s.ID] = true
			}
		}
	}

	out := make([]Entry, 0, len(c.byID))
	for _, w := range c.byID {
		e := c.projectEntry(w)
		if installed[w.ID] {
			e.InstallStatus = c.installedStatus(w)
		}
		out = append(out, e)
	}
	return out, nil
}

// Get implements Catalog.
func (c *concreteCatalog) Get(ctx context.Context, id string) (WorkflowDoc, error) {
	c.mu.RLock()
	w, ok := c.byID[id]
	c.mu.RUnlock()
	if !ok {
		return WorkflowDoc{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	yamlSrc := w.YAMLSource()
	if yamlSrc == "" {
		if b, err := corewf.ExportYAML(w); err == nil {
			yamlSrc = string(b)
		}
	}

	e := c.projectEntry(w)
	// Check install status.
	if c.cfg.Store != nil {
		if _, err := c.cfg.Store.Load(ctx, id); err == nil {
			e.InstallStatus = c.installedStatus(w)
		}
	}

	return WorkflowDoc{Entry: e, YAMLSource: yamlSrc}, nil
}

// Install implements Catalog.
func (c *concreteCatalog) Install(ctx context.Context, id string) (InstalledRef, error) {
	if c.cfg.Store == nil {
		return InstalledRef{}, ErrStoreUnavailable
	}

	c.mu.RLock()
	w, ok := c.byID[id]
	c.mu.RUnlock()
	if !ok {
		return InstalledRef{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	// created-vs-updated, so a failed provenance write can only remove a
	// row this call created (never the user's existing copy).
	_, lerr := c.cfg.Store.Load(ctx, w.ID)
	created := errors.Is(lerr, corewf.ErrWorkflowNotFound)
	saved, err := c.cfg.Store.Save(ctx, w)
	if err != nil {
		return InstalledRef{}, fmt.Errorf("catalog: install %s: %w", id, err)
	}
	if c.cfg.Provenance != nil {
		if err := c.cfg.Provenance.Put(corewf.InstallProvenance{
			WorkflowID: saved.ID,
			Source:     corewf.ProvenanceBuiltin,
			Slug:       w.ID,
			Version:    corewf.BuiltinVersionTag(w),
		}); err != nil {
			if created {
				_ = c.cfg.Store.Delete(ctx, saved.ID)
			}
			return InstalledRef{}, fmt.Errorf("catalog: install %s: record provenance: %w", id, err)
		}
	}

	ref := InstalledRef{
		WorkflowID:         saved.ID,
		MissingCredentials: c.missingCredentials(w),
	}

	// Arm the cron schedule when the workflow has one.
	if c.cfg.Scheduler != nil {
		if sched, tz := extractSchedule(w); sched != "" {
			if err := c.cfg.Scheduler.Register(ctx, saved.ID, sched, tz); err == nil {
				ref.Scheduled = true
			}
		}
	}

	return ref, nil
}

// installedStatus is "installed_outdated" when the persisted copy was
// installed from an older shipped version than w (recorded provenance),
// else "installed". A copy with no provenance record (installed before
// provenance existed, or saved by hand) is "installed": there is no
// recorded version to compare.
func (c *concreteCatalog) installedStatus(w corewf.Workflow) string {
	if c.cfg.Provenance == nil {
		return "installed"
	}
	p, ok, err := c.cfg.Provenance.Get(w.ID)
	if err != nil || !ok || p.Source != corewf.ProvenanceBuiltin {
		return "installed"
	}
	if p.Version != corewf.BuiltinVersionTag(w) {
		return "installed_outdated"
	}
	return "installed"
}

// projectEntry builds the Entry for w with credential / grant analysis.
func (c *concreteCatalog) projectEntry(w corewf.Workflow) Entry {
	e := ProjectEntry(w)
	e.RequiresCredentials = c.missingCredentials(w)
	return e
}

// missingCredentials returns mcp_call.server names referenced by w that
// are not in the recipe registry. When no registry is wired every
// mcp_call.server is reported as missing.
func (c *concreteCatalog) missingCredentials(w corewf.Workflow) []string {
	seen := make(map[string]bool)
	var missing []string
	for _, st := range w.Steps {
		if st.Kind != corewf.StepKindMCPCall || st.Server == "" {
			continue
		}
		if seen[st.Server] {
			continue
		}
		seen[st.Server] = true
		if c.cfg.RecipeRegistry == nil || !c.cfg.RecipeRegistry.Has(st.Server) {
			missing = append(missing, st.Server)
		}
	}
	return missing
}

// extractSchedule reads schedule + timezone from a workflow's metadata.
// WP04 added first-class schedule:/timezone: top-level fields to the
// Workflow struct; this function reads them directly.
func extractSchedule(w corewf.Workflow) (cron, tz string) {
	return w.Schedule, w.Timezone
}
