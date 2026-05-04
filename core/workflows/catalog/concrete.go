package catalog

import (
	"context"
	"fmt"
	"sync"

	wfsched "github.com/sigil-tech/kaneaz-harness/core/workflows/scheduler"
	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

// RecipeRegistry is the narrow read-only interface the catalog uses to
// decide which mcp_call.server references are already configured.
// Satisfied by *recipes.MergedCatalog and fakes in tests.
type RecipeRegistry interface {
	// Has reports whether serverName is a known recipe in the registry.
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
	RecipeRegistry RecipeRegistry
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
	builtins, _ := corewf.LoadBuiltins()
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
			e.InstallStatus = "installed"
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
			e.InstallStatus = "installed"
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

	saved, err := c.cfg.Store.Save(ctx, w)
	if err != nil {
		return InstalledRef{}, fmt.Errorf("catalog: install %s: %w", id, err)
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
