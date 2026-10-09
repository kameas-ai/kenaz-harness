package rpc

// Wiring for on-demand tool exposure (tool-context-budget-01TCBUD01):
// the loadtools.Service the chat runner's request builder, the
// kenaz__load_tools built-in, Sessions_LoadTools and the exposure write
// guard all share, and the adapters it reads the catalog, the server
// pool, the session's turn count and the audit log through.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	"github.com/kameas-ai/kenaz-harness/core/projects"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// builtinServerPurpose is the digest purpose of the built-in server.
const builtinServerPurpose = "Built-in harness tools beyond the always-loaded set."

// buildLoadToolsService constructs the shared load core. It returns nil
// when a required source is missing (the nil-core test chassis): the
// chat runner then sends the whole catalog, and neither the built-in nor
// the guard is installed.
func buildLoadToolsService(
	settingsImpl *settings.API,
	sessionMgr *session.Manager,
	projectMgr *projects.Manager,
	discoverer corellm.ToolDiscoverer,
	pool recipeStatusPool,
	auditEm contextaudit.Emitter,
) (*loadtools.Service, *toolServerDirectory) {
	if settingsImpl == nil || sessionMgr == nil || projectMgr == nil || discoverer == nil {
		logging.L().Info("rpc.tool_exposure.not_wired", "reason", "settings, sessions, projects or discoverer unavailable")
		return nil, nil
	}
	resolver, err := toolexposure.NewResolver(toolexposure.Deps{
		Settings: settingsImpl,
		Sessions: sessionMgr,
		Projects: projectMgr,
	})
	if err != nil {
		logging.L().Warn("rpc.tool_exposure.resolver_failed", "err", err.Error())
		return nil, nil
	}
	dir := &toolServerDirectory{pool: pool}
	svc, err := loadtools.NewService(loadtools.Deps{
		Catalog:     discovererCatalog{inner: discoverer},
		Resolver:    resolver,
		Servers:     dir,
		Activations: sessionMgr,
		Turns:       turnRunCounter{mgr: sessionMgr},
		Audit:       auditEm,
		Now:         time.Now,
	})
	if err != nil {
		logging.L().Warn("rpc.tool_exposure.service_failed", "err", err.Error())
		return nil, nil
	}
	return svc, dir
}

// discovererCatalog lists a session's servable tools through the same
// discoverer the chat runner's request builder uses, so the tool, the
// binding and the builder agree on what exists.
type discovererCatalog struct{ inner corellm.ToolDiscoverer }

func (d discovererCatalog) Catalog(ctx context.Context, sessionID string) ([]loadtools.CatalogEntry, error) {
	specs, err := d.inner.Tools(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]loadtools.CatalogEntry, 0, len(specs))
	for _, s := range specs {
		out = append(out, loadtools.CatalogEntry{Name: s.Name, Server: s.Server})
	}
	return out, nil
}

// turnRunCounter counts the session's recorded turns: the chat runner
// records a turn run before the turn's first model call, so during a
// turn the count is that turn's ordinal.
type turnRunCounter struct{ mgr *session.Manager }

func (t turnRunCounter) TurnOrdinal(ctx context.Context, sessionID string) (int, error) {
	runs, err := t.mgr.ListTurnRuns(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	return len(runs), nil
}

// recipeStatusPool is the slice of the dispatch pool the directory reads.
type recipeStatusPool interface {
	AllRecipeStatuses() []stdio.RecipeStatus
}

// toolServerDirectory reports every server the pool knows with its live
// state, and each one's purpose from its recipe's description. Server
// state comes from the pool, never from the tool catalog: the catalog
// omits servers that are not running.
//
// Only servers the pool knows are listed (2026-10-09, owner alec): an
// enabled recipe that never reached the pool (its env failed to resolve
// at boot) is omitted rather than marked stopped. WP08 of
// tool-context-budget-01TCBUD01 adds those from the recipe store.
type toolServerDirectory struct {
	pool recipeStatusPool

	mu         sync.Mutex
	userSource func() []recipes.Recipe
	purposes   map[string]string
}

// setUserRecipes adds user-imported recipes to the purpose lookup.
func (d *toolServerDirectory) setUserRecipes(src func() []recipes.Recipe) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userSource = src
	d.purposes = nil
}

// Servers implements loadtools.ServerDirectory.
func (d *toolServerDirectory) Servers(_ context.Context) []loadtools.ServerInfo {
	out := []loadtools.ServerInfo{{
		Name:    toolexposure.BuiltinServer,
		Purpose: builtinServerPurpose,
		State:   string(transport.StateRunning),
		Running: true,
	}}
	if d == nil || d.pool == nil {
		return out
	}
	for _, st := range d.pool.AllRecipeStatuses() {
		out = append(out, loadtools.ServerInfo{
			Name:    st.ID,
			Purpose: d.purpose(st.ID),
			State:   st.State,
			Running: st.State == string(transport.StateRunning),
		})
	}
	return out
}

// purpose returns a server's recipe description, rebuilding the recipe
// index only when a server it has not seen appears.
func (d *toolServerDirectory) purpose(id string) string {
	if id == harnessmcp.ServerName {
		return harnessmcp.Purpose
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.purposes[id]; ok {
		return p
	}
	idx := map[string]string{}
	for _, r := range mergedRecipeCatalog(d.userSource).List() {
		idx[r.ID] = r.Description
	}
	if _, ok := idx[id]; !ok {
		idx[id] = ""
	}
	d.purposes = idx
	return idx[id]
}

var _ loadtools.ServerDirectory = (*toolServerDirectory)(nil)

// toolsAuditEmitter forwards tool-exposure audit events into the audit
// view's log under category LLM — activation changes what the model is
// shown, and LLM is a category the audit view renders and filters (the
// audit view's categoryForKind maps "tools." kinds to it too). The audit
// API is bound late: it is built after the LLM stack.
type toolsAuditEmitter struct {
	impl atomic.Pointer[audit.API]
}

func (e *toolsAuditEmitter) bind(impl *audit.API) {
	if e != nil {
		e.impl.Store(impl)
	}
}

func (e *toolsAuditEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	impl := e.impl.Load()
	if impl == nil {
		return nil
	}
	impl.Push(audit.Entry{
		ID:        "tools-" + newBlockedRequestID(),
		Timestamp: ev.TS.UTC().Format(time.RFC3339Nano),
		Category:  "LLM",
		Subject:   string(ev.Kind),
		Trailing:  string(ev.Payload),
	})
	return nil
}

var _ toolloop.BuiltinTool = (*loadtools.Tool)(nil)
