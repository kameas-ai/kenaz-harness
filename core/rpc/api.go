// Package rpc is the Wails-binding façade for the harness frontend.
//
// HarnessAPI mirrors Kenaz's KenazAPI shape — top-level cross-cutting
// methods (ShellStatus, AppInfo) plus 12 view-scoped sub-interfaces
// returning stable Go pointers for the lifetime of the API value.
//
// DIRECTIVE_001: the frontend talks to core/ ONLY through this package.
// No core/ package imports anything from frontend/.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/sigil-tech/kaneaz-harness/core"
	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
	corecontexts "github.com/sigil-tech/kaneaz-harness/core/contexts"
	"github.com/sigil-tech/kaneaz-harness/core/event"
	"github.com/sigil-tech/kaneaz-harness/core/hooks"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/credref"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
	llmregistry "github.com/sigil-tech/kaneaz-harness/core/llm/registry"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/fixture"
	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	contextview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	secretsref "github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
	"github.com/zalando/go-keyring"
)

// HarnessAPI is the boundary between the Wails-hosted Vue frontend and
// the Go core. Top-level cross-cutting methods (ShellStatus, AppInfo)
// live here; view-specific surfaces are accessed through stable,
// view-scoped sub-interfaces. Implementations MUST be safe for
// concurrent use.
type HarnessAPI interface {
	ShellStatus(ctx context.Context) (ShellStatus, error)
	AppInfo(ctx context.Context) (AppInfo, error)

	LLMConnector() llm.LLMConnectorAPI
	MCP() mcp.MCPAPI
	A2A() a2a.A2AAPI
	Workflow() workflow.WorkflowAPI
	Sessions() sessions.SessionsAPI
	Trust() trust.TrustAPI
	Context() contextview.ContextAPI
	Contexts() contextsview.ContextsAPI
	Bundle() bundle.BundleAPI
	Policy() policy.PolicyAPI
	Audit() audit.AuditAPI
	Settings() settings.SettingsAPI
	Memory() memoryview.MemoryAPI
	Hooks() hooksview.HooksAPI
	Projects() projectsview.ProjectsAPI
	Attachments() attachmentsview.AttachmentsAPI
}

// ShellStatus drives the Toolbar status pills + LegendBar live-rate
// indicators. Polled every 5 s while focused; future optimization
// replaces the poll with a `shell:status-changed` push event.
type ShellStatus struct {
	ActiveProvider string  `json:"activeProvider"` // FR-001f
	TrustTier      string  `json:"trustTier"`      // FR-001f
	HarnessBuild   string  `json:"harnessBuild"`   // FR-001f
	Connection     string  `json:"connection"`     // connecting | ready | degraded | lost (FR-013, FR-017)
	EventRate      float64 `json:"eventRate"`      // events/sec (FR-001g)
	PolicyApplied  bool    `json:"policyApplied"`  // FR-001e
	RedactionOn    bool    `json:"redactionOn"`    // FR-001e
	LocalFirstOn   bool    `json:"localFirstOn"`   // FR-001e
}

// AppInfo is read once on app start; cached frontend-side for the session.
type AppInfo struct {
	Build      string     `json:"build"`
	Commit     string     `json:"commit"`
	BuildTime  string     `json:"buildTime"`
	GoVersion  string     `json:"goVersion"`
	Platform   string     `json:"platform"`
	WindowSize WindowSize `json:"windowSize"`
}

// WindowSize mirrors the charter shape.
type WindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// API is the concrete HarnessAPI implementation backed by core.
// Currently a stub: each view accessor returns a no-op implementation
// stable for the lifetime of API. Real wiring lands in feature missions.
type API struct {
	core *core.Core

	// Stable view-accessor instances (plan §4.2).
	llmAPI      llm.LLMConnectorAPI
	mcpAPI      mcp.MCPAPI
	a2aAPI      a2a.A2AAPI
	workflowAPI workflow.WorkflowAPI
	sessionsAPI sessions.SessionsAPI
	trustAPI    trust.TrustAPI
	contextAPI  contextview.ContextAPI
	contextsAPI contextsview.ContextsAPI
	bundleAPI    bundle.BundleAPI
	policyAPI    policy.PolicyAPI
	auditImpl    *audit.API
	auditAPI     audit.AuditAPI
	settingsImpl *settings.API
	settingsAPI  settings.SettingsAPI
	memoryAPI       memoryview.MemoryAPI
	hooksAPI        hooksview.HooksAPI
	projectsAPI     projectsview.ProjectsAPI
	attachmentsMgr  *coreatt.Manager
	attachmentsAPI  attachmentsview.AttachmentsAPI

	// broker fans typed source channels to Wails event topics. Held for
	// the lifetime of the API value; per-view bridges (llm, sessions,
	// audit, mcp, …) emit through it so the privacy CI invariant —
	// only emitter.go / stream_broker.go call runtime.EventsEmit —
	// stays intact.
	broker *StreamBroker

	// bindings is the Wails-reflected surface; held for the lifetime of
	// API so OnStartup can call SetContext on it.
	bindings *Bindings
}

// SetContext threads the Wails app context to the Bindings surface
// AND to the StreamBroker, which needs the OnStartup-supplied context
// so runtime.EventsEmit dispatches correctly (background contexts
// crash Wails). main.go calls this from OnStartup.
func (a *API) SetContext(ctx context.Context) {
	if a.bindings != nil {
		a.bindings.SetContext(ctx)
	}
	if a.broker != nil {
		a.broker.SetContext(ctx)
	}
}

// New constructs a HarnessAPI implementation. Sub-interfaces start as
// stubs and are replaced by real impls as each feature mission lands.
//
// Sessions wiring: when c is non-nil, New wires the real
// session.Manager-backed impl; otherwise the surface falls back to a
// safe stub so test fixtures can call New(nil) without booting core.
//
// LLMConnector wiring:
//   - A connector Registry is constructed with the embedded capability
//     catalog and a credref bridge pointing at a memory secrets backend
//     so env-var-resolved API keys (e.g. ANTHROPIC_API_KEY) flow through
//     without bundle support.
//   - The view-scoped llm.API is constructed with a streamSinkAdapter
//     that wraps a lazily-created StreamBroker — chunks emit on the
//     "llm:stream-chunk" topic via the broker so the privacy CI
//     invariant (only emitter.go and stream_broker.go call
//     runtime.EventsEmit) stays intact.
func New(c *core.Core) *API {
	attMgr := newAttachmentsManager(c)
	a := &API{
		core:           c,
		a2aAPI:         &stubA2A{},
		workflowAPI:    &stubWorkflow{},
		sessionsAPI:    newSessionsAPI(c, attMgr),
		trustAPI:       &stubTrust{},
		contextAPI:     &stubContext{},
		policyAPI:      &stubPolicy{},
		projectsAPI:    newProjectsAPI(c),
		attachmentsMgr: attMgr,
	}
	a.attachmentsAPI = newAttachmentsAPI(c, attMgr)
	a.broker = NewStreamBroker(WailsEmitter{})
	a.auditImpl = audit.NewAPI(audit.WithSubscriber(a.broker))
	a.auditAPI = a.auditImpl
	a.mcpAPI = mcp.NewAPI(mcp.WithSubscriber(a.broker))
	a.contextsAPI = newContextsAPI(c)

	// Settings: file-backed when we have a user config dir; in-memory
	// fallback for the test harness path so New(nil) keeps working.
	var settingsStore settings.SettingsStore
	if fs, err := settings.NewFileStoreFromEnv(); err == nil {
		settingsStore = fs
	}
	settingsImpl := settings.NewAPI(settingsStore)
	a.settingsAPI = settingsImpl
	a.settingsImpl = settingsImpl

	// LLM stack uses the settings store as the opt-in gate for
	// retrieval, and shares the memory store with the MemoryAPI so a
	// pin in the chat surface and a retrieval at send-time see the
	// same gob file.
	memStore := openMemoryStore(c)
	personalForLLM := newPersonalStore(c)
	embedder := newEmbedder(c, personalForLLM)
	memoryEnabled := func() bool {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return false
		}
		v, err := settingsImpl.Store().LoadMemory()
		if err != nil {
			return false
		}
		return v
	}
	retriever := corememory.NewRetriever(memStore, embedder, memoryEnabled, 0.7)
	hooksRunner, hookRegistry, hookBuiltins := newHooksStack(c, retriever, memStore, embedder)
	a.llmAPI = newLLMStack(c, a.broker, personalForLLM, hooksRunner, attMgr)
	a.memoryAPI = memoryview.New(memoryview.Config{
		Store:    memStore,
		Embedder: embedder,
		Reader:   newMemoryMessageReader(c),
	})
	if hookRegistry != nil {
		a.hooksAPI = hooksview.New(hooksview.Config{
			Registry: hookRegistry,
			Builtins: &hooksBuiltinDescriber{r: hookBuiltins},
		})
	} else {
		a.hooksAPI = hooksview.New(hooksview.Config{})
	}

	// Wire the bundle reader against the core data dir. nil core (test
	// harness path) leaves the impl with a nil reader — List returns an
	// empty slice and Get returns "not found", which is the contract the
	// frontend's empty-state path expects.
	bundleOpts := []bundle.Option{}
	if c != nil {
		bundleOpts = append(bundleOpts, bundle.WithReader(bundle.NewFSReader(c.DataDir())))
		if cas, err := c.BundleCache(); err == nil && cas != nil {
			bundleOpts = append(bundleOpts, bundle.WithCAS(bundle.CASFromCache(cas)))
		}
	}
	a.bundleAPI = bundle.NewAPI(bundleOpts...)

	a.bindings = NewBindings(a)
	if a.settingsImpl != nil {
		a.bindings.SetSettingsStore(a.settingsImpl.Store())
	}
	return a
}

// newSessionsAPI returns the real Manager-backed SessionsAPI when c
// is non-nil; otherwise a noop stub for callers that pass New(nil)
// (see api_test.go's TestViewAccessorStability).
//
// When attMgr is non-nil the returned impl drives the attachments table
// for SetSystemPrompt, with the session.system_prompt column kept for
// the one-release compat buffer.
func newSessionsAPI(c *core.Core, attMgr *coreatt.Manager) sessions.SessionsAPI {
	if c == nil {
		return &stubSessions{}
	}
	if attMgr == nil {
		return sessions.NewManagerAPI(c.SessionManager())
	}
	return sessions.NewManagerAPIWithAttachments(c.SessionManager(), attMgr)
}

// newProjectsAPI returns the real Manager-backed ProjectsAPI when c is
// non-nil; otherwise a noop stub.
func newProjectsAPI(c *core.Core) projectsview.ProjectsAPI {
	if c == nil {
		return &stubProjects{}
	}
	return projectsview.New(c.ProjectManager(), c.SessionManager())
}

// newAttachmentsManager constructs the core/attachments.Manager backed
// by storage.DB. Returns nil when c is nil or storage isn't available;
// the rpc surface treats nil as "attachments disabled" and the
// SessionsAPI / LLM stack fall back to legacy behaviour.
func newAttachmentsManager(c *core.Core) *coreatt.Manager {
	if c == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	return coreatt.NewManager(coreatt.NewSQLStore(s))
}

// newAttachmentsAPI returns the real Manager-backed AttachmentsAPI
// when both c and the attachments manager are wired; otherwise a noop
// stub keeps the chassis bootable.
func newAttachmentsAPI(c *core.Core, mgr *coreatt.Manager) attachmentsview.AttachmentsAPI {
	if c == nil || mgr == nil {
		return &stubAttachments{}
	}
	return attachmentsview.New(mgr, &sessionProjectReader{mgr: c.SessionManager()})
}

// sessionProjectReader adapts session.Manager into the small
// SessionProjectReader the attachments view needs.
type sessionProjectReader struct {
	mgr *session.Manager
}

func (r *sessionProjectReader) ProjectID(ctx context.Context, sessionID string) (*string, error) {
	if r == nil || r.mgr == nil {
		return nil, nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if rec.ProjectID == nil {
		return nil, nil
	}
	v := *rec.ProjectID
	return &v, nil
}

// newLLMStack constructs the connector Registry + view-scoped API. It
// is split out of New so the Wails wiring stays declarative.
//
// When core is nil (test path: api_test.go's chassis tests construct
// API{nil}), we still build a working stack — the registry is
// process-local and does not depend on the harness data directory for
// anything other than the personal-providers escape hatch.
//
// The shared broker is supplied by New so all view bridges fan out to
// the same StreamBroker instance.
func newLLMStack(c *core.Core, broker *StreamBroker, store personal.Store, hooksRunner llm.HookRunner, attMgr *coreatt.Manager) llm.LLMConnectorAPI {
	// Share ONE secrets backend between the credref resolver (which
	// reads keys when streaming) and the keychain writer (which stages
	// keys when the user submits AddProvider). Without this sharing,
	// AddProvider would write into a backend the resolver can't see.
	secretsBackend := secrets.NewMemoryBackend()
	reg, err := llmregistry.New(llmregistry.Options{
		Resolver: credref.New(secretsBackend),
	})
	if err != nil {
		// Fall back to the stub on a registry construction failure so
		// the chassis still boots. The error path is exercised only by
		// catalog-load failures, which should never happen in
		// production builds.
		return &stubLLM{}
	}
	// Compile-time witness: *llmregistry.Registry satisfies the local
	// Registry interface used by the view impl.
	var _ corellm.Registry = (*llmregistry.Registry)(nil)

	credResolver := credref.New(secretsBackend)
	historyAdapter := newSessionHistoryReader(c)
	// WP02 — wire the global static resolver against
	// <DataDir>/mcp_servers.json. A missing file soft-fails to
	// auto_allow (the file is opt-in); a malformed file logs a
	// warning and the resolver is left nil so the loop's built-in
	// auto_allow default applies. Per-session overrides (C2) are
	// composed on top of this when the session manager grows the
	// MCPOverrides reader.
	var perms toolloop.PermissionResolver
	if c != nil && c.DataDir() != "" {
		staticPerms, permErr := toolloop.NewStaticResolverFromDataDir(c.DataDir())
		if permErr != nil {
			logging.L().Warn("toolloop.permissions.static_load_failed",
				"data_dir", c.DataDir(), "err", permErr.Error())
		} else {
			perms = staticPerms
		}
	}
	// Construct the toolloop with the registry and a placeholder
	// in-memory fixture pool. Production wiring (real MCP server pool)
	// arrives with mission C1; until then the fixture is empty so a
	// model that asks for a tool gets a synthetic "not registered"
	// result and the conversation continues.
	loop, loopErr := toolloop.New(toolloop.Config{
		Registry:    reg,
		Pool:        &mcpPoolAdapter{inner: fixture.New()},
		History:     historyAdapter,
		Permissions: perms,
	})
	if loopErr != nil {
		// New only errors on missing registry/pool — both are
		// supplied here, so this branch is defensive. Fall through
		// without a loop wired so the chat surface stays usable.
		loop = nil
	}
	var attResolver llm.AttachmentsResolver
	if attMgr != nil {
		attResolver = &attachmentsResolverAdapter{
			mgr:    attMgr,
			reader: &sessionProjectReader{mgr: c.SessionManager()},
		}
	}
	return llm.New(llm.Config{
		Registry:      reg,
		Sink:          &streamSinkAdapter{broker: broker},
		Store:         store,
		Keychain:      &keychainWriter{backend: secretsBackend},
		Prober:        &registryProber{reg: reg, creds: credResolver},
		History:       historyAdapter,
		HistoryWriter: historyAdapter,
		Hooks:         hooksRunner,
		Attachments:   attResolver,
		ToolLoop:      loop,
	})
}

// attachmentsResolverAdapter bridges core/attachments.Manager into the
// llm view's AttachmentsResolver shape so the LLM impl never imports
// core/attachments directly.
type attachmentsResolverAdapter struct {
	mgr    *coreatt.Manager
	reader coreatt.SessionProjectReader
}

func (a *attachmentsResolverAdapter) ListResolved(ctx context.Context, sessionID string) ([]llm.ResolvedAttachment, error) {
	if a == nil || a.mgr == nil {
		return nil, nil
	}
	rows, err := a.mgr.ListResolved(ctx, a.reader, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.ResolvedAttachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, llm.ResolvedAttachment{
			Content: r.Content,
			Kind:    r.Kind,
		})
	}
	return out, nil
}

// newContextsAPI opens the Context Library rooted at <DataDir>/contexts/
// and wraps it behind the view-scoped surface. A nil core (test path)
// or any open failure soft-fails to a nil-library API — Contexts_List
// returns ErrLibraryUnavailable so the frontend's empty-state card is
// the user-visible behaviour instead of a hard chassis crash.
func newContextsAPI(c *core.Core) contextsview.ContextsAPI {
	if c == nil || c.DataDir() == "" {
		return contextsview.New(nil)
	}
	lib, err := corecontexts.Open(filepath.Join(c.DataDir(), "contexts"))
	if err != nil {
		return contextsview.New(nil)
	}
	// SweepTrash is best-effort on boot — a stale trash directory
	// shouldn't keep the surface from coming up.
	_ = lib.SweepTrash()
	return contextsview.New(lib)
}

// mcpPoolAdapter bridges the wider core/mcp.Pool surface (which knows
// about ServerSpec, Open, Close) to the toolloop's narrow MCPPool
// view. The toolloop only needs Tools + Call; this adapter projects
// the pool's coremcp.Tool slice into toolloop.Tool form.
type mcpPoolAdapter struct {
	inner coremcp.Pool
}

func (a *mcpPoolAdapter) Tools(ctx context.Context) ([]toolloop.Tool, error) {
	tools, err := a.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]toolloop.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolloop.Tool{Server: t.Server, Name: t.Name})
	}
	return out, nil
}

func (a *mcpPoolAdapter) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	return a.inner.Call(ctx, server, tool, args)
}

// openMemoryStore opens the long-term-memory vector DB at
// <DataDir>/memory.gob. A nil core or any IO failure soft-fails to nil
// so the chassis still boots — the rpc surface treats nil as "memory
// unavailable" and the toggle in settings becomes a no-op.
func openMemoryStore(c *core.Core) corememory.Store {
	if c == nil || c.DataDir() == "" {
		return nil
	}
	store, err := corememory.NewChromemStore(filepath.Join(c.DataDir(), "memory.gob"))
	if err != nil {
		return nil
	}
	return store
}

// newEmbedder picks the first openai-kind personal provider and wires
// its keychain credential as the embedder's key source. When no openai
// provider exists, we return the NoopEmbedder so RememberMessage can
// surface a friendly "configure an OpenAI provider" error and the
// retriever's Retrieve becomes a cheap noop.
func newEmbedder(_ *core.Core, store personal.Store) corememory.Embedder {
	if store == nil {
		return corememory.NoopEmbedder{}
	}
	profiles, err := store.List()
	if err != nil {
		return corememory.NoopEmbedder{}
	}
	for _, p := range profiles {
		if p.Kind == "openai" && p.Cred.Kind == "keychain" && p.Cred.Locator != "" {
			locator := p.Cred.Locator
			return corememory.NewOpenAIEmbedder(func(_ context.Context) ([]byte, error) {
				val, err := keyring.Get(keyringService, locator)
				if err != nil {
					return nil, fmt.Errorf("memory: keychain get %q: %w", locator, err)
				}
				return []byte(val), nil
			})
		}
	}
	return corememory.NoopEmbedder{}
}

// newHooksStack constructs a hooks.Runner with the memory builtins
// preregistered and returns the runner alongside the registry +
// builtins so the hooksview surface can list / mutate hooks against
// the same instances the runner dispatches on. Returns (nil, nil, nil)
// when memStore is nil — the LLM impl falls back to "no hooks
// configured" and the hooksview surface renders an empty list.
//
// Starter memory hooks are NOT auto-installed on boot; the settings
// "long-term memory" toggle owns that lifecycle so a fresh install
// stays quiet until the user opts in.
func newHooksStack(
	c *core.Core,
	retriever *corememory.Retriever,
	memStore corememory.Store,
	embedder corememory.Embedder,
) (llm.HookRunner, *hooks.Registry, *hooks.BuiltinRegistry) {
	if memStore == nil {
		return nil, nil, nil
	}
	dataDir := ""
	if c != nil {
		dataDir = c.DataDir()
	}
	registry, err := hooks.NewRegistry(dataDir)
	if err != nil {
		return nil, nil, nil
	}
	builtins := hooks.NewBuiltinRegistry()
	hooks.RegisterMemoryBuiltins(builtins, hooks.MemoryDeps{
		Retriever: &retrieverAdapter{r: retriever},
		Store:     memStore,
		Embedder:  embedder,
	})
	runner := hooks.NewRunner(hooks.Config{
		Registry: registry,
		Builtins: builtins,
	})
	return &hooksRunnerAdapter{r: runner}, registry, builtins
}

// hooksBuiltinDescriber adapts *hooks.BuiltinRegistry to the
// hooksview.BuiltinDescriber interface (Builtins()) by forwarding to
// the underlying Describe() method.
type hooksBuiltinDescriber struct {
	r *hooks.BuiltinRegistry
}

func (a *hooksBuiltinDescriber) Builtins() []hooks.BuiltinDescriptor {
	if a == nil || a.r == nil {
		return nil
	}
	return a.r.Describe()
}

// retrieverAdapter satisfies hooks.MemoryRetriever (which uses
// memory.Snippet) by delegating to core/memory.Retriever.
type retrieverAdapter struct {
	r *corememory.Retriever
}

func (a *retrieverAdapter) Retrieve(ctx context.Context, query string, k int) ([]corememory.Snippet, error) {
	if a == nil || a.r == nil {
		return nil, nil
	}
	return a.r.Retrieve(ctx, query, k)
}

func (a *retrieverAdapter) RetrieveScoped(ctx context.Context, query, sessionID, projectID string, k int) ([]corememory.Snippet, error) {
	if a == nil || a.r == nil {
		return nil, nil
	}
	return a.r.RetrieveScoped(ctx, query, sessionID, projectID, k)
}

// hooksRunnerAdapter bridges hooks.Runner to llm.HookRunner. The
// translation between event payload shapes is mechanical — both sides
// carry the same fields under different package types.
type hooksRunnerAdapter struct {
	r *hooks.Runner
}

func (a *hooksRunnerAdapter) RunPreSend(ctx context.Context, ev llm.PreSendHookEvent) (llm.PreSendHookEvent, error) {
	if a == nil || a.r == nil {
		return ev, nil
	}
	in := hooks.PreSendEvent{
		SessionID: ev.SessionID,
		Messages:  hookMessagesFromLLM(ev.Messages),
		Model:     ev.Model,
		Kind:      ev.Kind,
		UserText:  ev.UserText,
	}
	out, err := a.r.RunPreSend(ctx, in)
	if err != nil {
		return ev, err
	}
	ev.Messages = hookMessagesToLLM(out.Messages)
	return ev, nil
}

func (a *hooksRunnerAdapter) RunPostSend(ctx context.Context, ev llm.PostSendHookEvent) {
	if a == nil || a.r == nil {
		return
	}
	a.r.RunPostSend(ctx, hooks.PostSendEvent{
		SessionID:     ev.SessionID,
		UserTurn:      ev.UserTurn,
		AssistantTurn: ev.AssistantTurn,
		Model:         ev.Model,
		Kind:          ev.Kind,
		FinishReason:  ev.FinishReason,
	})
}

func hookMessagesFromLLM(in []llm.HookMessage) []hooks.HookMessage {
	out := make([]hooks.HookMessage, len(in))
	for i, m := range in {
		out[i] = hooks.HookMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

func hookMessagesToLLM(in []hooks.HookMessage) []llm.HookMessage {
	out := make([]llm.HookMessage, len(in))
	for i, m := range in {
		out[i] = llm.HookMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// newMemoryMessageReader adapts the session manager to the
// MessageReader shape the memory view uses to look up a message by id
// before embedding its content.
func newMemoryMessageReader(c *core.Core) memoryview.MessageReader {
	if c == nil {
		return nil
	}
	mgr := c.SessionManager()
	if mgr == nil {
		return nil
	}
	return &memoryMessageReader{mgr: mgr}
}

type memoryMessageReader struct {
	mgr *session.Manager
}

func (r *memoryMessageReader) ListMessages(ctx context.Context, sessionID string) ([]memoryview.Message, error) {
	stored, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]memoryview.Message, 0, len(stored))
	for _, m := range stored {
		out = append(out, memoryview.Message{
			ID:      m.ID,
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out, nil
}

// newSessionHistoryReader wires the LLM impl to the session manager
// so buildMessages can thread the user's actual conversation into the
// upstream provider call. Without this, the connector falls back to a
// hardcoded demo prompt — the symptom is "my messages aren't reaching
// the LLM" no matter what the user types.
//
// The returned value satisfies BOTH SessionMessageReader and
// SessionMessageWriter so the LLM impl can also persist the
// assistant turn at stream completion.
func newSessionHistoryReader(c *core.Core) *sessionHistoryReader {
	if c == nil {
		return nil
	}
	mgr := c.SessionManager()
	if mgr == nil {
		return nil
	}
	return &sessionHistoryReader{mgr: mgr}
}

type sessionHistoryReader struct {
	mgr *session.Manager
}

func (r *sessionHistoryReader) ListMessages(ctx context.Context, sessionID string) ([]llm.SessionMessage, error) {
	if r == nil || r.mgr == nil {
		return nil, nil
	}
	stored, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.SessionMessage, 0, len(stored))
	for _, m := range stored {
		out = append(out, llm.SessionMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out, nil
}

// AppendMessage persists the assistant turn at stream completion so a
// future ListMessages call rehydrates it. Implements
// llm.SessionMessageWriter.
func (r *sessionHistoryReader) AppendMessage(ctx context.Context, sessionID, role, content string) error {
	if r == nil || r.mgr == nil {
		return nil
	}
	_, err := r.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	return err
}

// SystemPromptFor implements llm.SessionContextReader by reading the
// session's persisted starting context. A nil receiver is the test
// fallback path; return zero values so buildMessages skips the prepend.
func (r *sessionHistoryReader) SystemPromptFor(ctx context.Context, sessionID string) (string, string, error) {
	if r == nil || r.mgr == nil {
		return "", "", nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	return rec.SystemPrompt, rec.ContextKind, nil
}

// registryProber satisfies llm.ProviderProber by routing through the
// adapter's ModelLister capability when one is registered. A successful
// /models call proves the credential resolves and the provider API
// answers. Adapters that have no ModelLister (or kinds with no adapter
// registered) report a "not yet supported" message instead of a
// confusing "no provider prober configured" — the chassis still reports
// success=false but the row no longer looks broken.
type registryProber struct {
	reg   *llmregistry.Registry
	creds *credref.Resolver
}

func (p *registryProber) Probe(ctx context.Context, profile corellm.ProviderProfile) llm.ProberResult {
	if p == nil || p.reg == nil {
		return llm.ProberResult{Message: "prober unavailable"}
	}
	adapter := p.reg.Adapter(profile.Kind)
	if adapter == nil {
		return llm.ProberResult{
			Message: fmt.Sprintf("no adapter for kind %q yet", profile.Kind),
		}
	}
	lister, ok := adapter.(corellm.ModelLister)
	if !ok {
		return llm.ProberResult{
			Message: fmt.Sprintf("%s adapter has no probe endpoint", profile.Kind),
		}
	}
	cred, err := p.creds.Resolve(ctx, profile.ID, profile.Cred)
	if err != nil {
		return llm.ProberResult{Message: fmt.Sprintf("credential: %v", err)}
	}
	defer cred.Destroy()
	var models []corellm.ModelInfo
	probeErr := cred.Use(func(buf []byte) error {
		out, err := lister.ListModels(ctx, buf)
		if err != nil {
			return err
		}
		models = out
		return nil
	})
	if probeErr != nil {
		return llm.ProberResult{Message: probeErr.Error()}
	}
	return llm.ProberResult{
		Success: true,
		Message: fmt.Sprintf("ok — %d model(s) available", len(models)),
	}
}

// keychainWriter implements llm.KeychainWriter by storing the
// plaintext in the OS keychain (zalando/go-keyring routes to macOS
// Keychain, Windows Credential Manager, or libsecret on Linux) AND
// in the shared in-memory backend so the credref resolver can read
// it without an OS-keychain round-trip mid-session.
//
// Persistence: the OS-keychain copy survives Wails restarts, which
// is what users expect from the "API key" flow. The in-memory copy
// is a hot cache — Resolve checks it after the keychain miss path.
type keychainWriter struct {
	backend *secrets.MemoryBackend
}

// keyringService matches secrets.MemoryBackend's namespace so reads
// via Resolve(RefKeychain) find the entry written here.
const keyringService = "kaneaz-harness"

// Write stores plaintext in the OS keychain under the harness's
// service namespace, mirrors it to the in-memory backend, and zeroes
// the supplied buffer.
func (w *keychainWriter) Write(_ context.Context, locator string, plaintext []byte) error {
	if w == nil {
		return nil
	}
	if err := keyring.Set(keyringService, locator, string(plaintext)); err != nil {
		// Don't hard-fail — fall through to the in-memory cache so
		// the user can still chat in the current session even if the
		// OS keychain backend is unavailable (CI / sandbox / Linux
		// without libsecret).
		_ = err
	}
	if w.backend != nil {
		w.backend.SetEntry(secretsref.RefKeychain, locator, plaintext)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

// newPersonalStore constructs the personal-providers FileStore. It
// prefers c.DataDir()/providers.json when core is wired so test
// harnesses with an explicit DataDir stay isolated; otherwise it falls
// back to personal.DefaultPath() ($USER_CONFIG_DIR/kaneaz-harness).
// A construction failure returns nil; the rpc impl treats a nil store
// as "personal store unavailable" and the chassis still boots.
func newPersonalStore(c *core.Core) personal.Store {
	var path string
	if c != nil && c.DataDir() != "" {
		path = filepath.Join(c.DataDir(), "providers.json")
	} else {
		p, err := personal.DefaultPath()
		if err != nil {
			return nil
		}
		path = p
	}
	store, err := personal.NewFileStore(path)
	if err != nil {
		return nil
	}
	return store
}

// streamSinkAdapter wraps a *StreamBroker so the view package never
// imports the rpc package directly (keeps the import graph acyclic).
//
// The adapter calls broker.emitter.Emit under the hood. Privacy CI
// invariant #1 still holds: runtime.EventsEmit lives only in
// emitter.go and stream_broker.go.
type streamSinkAdapter struct {
	broker *StreamBroker
}

// Emit forwards topic+payload to the broker's underlying emitter
// using the Wails-supplied context — context.Background() crashes
// runtime.EventsEmit with "invalid context was passed".
func (s *streamSinkAdapter) Emit(topic string, payload any) {
	if s == nil || s.broker == nil {
		return
	}
	s.broker.emitter.Emit(s.broker.EmitCtx(), topic, payload)
}

// AuditObserver returns a function suitable for passing to
// event.WithObserver — every successful Append fans into the audit
// API's ring buffer + active subscribers. Wiring lives at the call
// site (main.go) so core/rpc stays decoupled from the emitter
// constructor (DIRECTIVE_001).
func (a *API) AuditObserver() func(event.Event) {
	if a.auditImpl == nil {
		return func(event.Event) {}
	}
	return a.auditImpl.ObserveEvent
}

// ShellStatus returns a default shell status. Real values are filled by
// downstream missions; for now the chassis renders a quiet baseline.
func (a *API) ShellStatus(_ context.Context) (ShellStatus, error) {
	return ShellStatus{
		ActiveProvider: "—",
		TrustTier:      "Local",
		HarnessBuild:   "0.0.0-dev",
		Connection:     "ready",
		EventRate:      0,
		PolicyApplied:  true,
		RedactionOn:    true,
		LocalFirstOn:   true,
	}, nil
}

// AppInfo returns build metadata. Real values come from build-time ldflags.
func (a *API) AppInfo(_ context.Context) (AppInfo, error) {
	return AppInfo{
		Build:      "dev",
		Commit:     "unknown",
		BuildTime:  "",
		GoVersion:  "",
		Platform:   "",
		WindowSize: WindowSize{Width: 1280, Height: 800},
	}, nil
}

// View accessors return the stable instance constructed in New.
func (a *API) LLMConnector() llm.LLMConnectorAPI { return a.llmAPI }
func (a *API) MCP() mcp.MCPAPI                   { return a.mcpAPI }
func (a *API) A2A() a2a.A2AAPI                   { return a.a2aAPI }
func (a *API) Workflow() workflow.WorkflowAPI    { return a.workflowAPI }
func (a *API) Sessions() sessions.SessionsAPI    { return a.sessionsAPI }
func (a *API) Trust() trust.TrustAPI             { return a.trustAPI }
func (a *API) Context() contextview.ContextAPI   { return a.contextAPI }
func (a *API) Contexts() contextsview.ContextsAPI {
	if a.contextsAPI == nil {
		return contextsview.New(nil)
	}
	return a.contextsAPI
}
func (a *API) Bundle() bundle.BundleAPI          { return a.bundleAPI }
func (a *API) Policy() policy.PolicyAPI          { return a.policyAPI }
func (a *API) Audit() audit.AuditAPI             { return a.auditAPI }
func (a *API) Settings() settings.SettingsAPI    { return a.settingsAPI }
func (a *API) Memory() memoryview.MemoryAPI {
	if a.memoryAPI == nil {
		return &stubMemory{}
	}
	return a.memoryAPI
}
func (a *API) Hooks() hooksview.HooksAPI {
	if a.hooksAPI == nil {
		return &stubHooks{}
	}
	return a.hooksAPI
}
func (a *API) Projects() projectsview.ProjectsAPI {
	if a.projectsAPI == nil {
		return &stubProjects{}
	}
	return a.projectsAPI
}
func (a *API) Attachments() attachmentsview.AttachmentsAPI {
	if a.attachmentsAPI == nil {
		return &stubAttachments{}
	}
	return a.attachmentsAPI
}

// Bindings returns the slice of Wails-bound objects. The Bindings struct
// (bindings.go) is the flat-method surface Wails reflects. Stable for the
// lifetime of API.
func (a *API) Bindings() []any { return []any{a.bindings} }

// StreamBroker returns the lazily-constructed broker. Future view
// bridges (sessions, audit, …) reuse this instance so the privacy CI
// invariant #1 — only emitter.go / stream_broker.go call
// runtime.EventsEmit — keeps holding.
func (a *API) StreamBroker() *StreamBroker { return a.broker }
