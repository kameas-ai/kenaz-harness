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
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sigil-tech/kaneaz-harness/core"
	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
	corecontexts "github.com/sigil-tech/kaneaz-harness/core/contexts"
	corecorpus "github.com/sigil-tech/kaneaz-harness/core/corpus"
	"github.com/sigil-tech/kaneaz-harness/core/event"
	"github.com/sigil-tech/kaneaz-harness/core/hooks"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/credref"
	"github.com/sigil-tech/kaneaz-harness/core/llm/personal"
	llmregistry "github.com/sigil-tech/kaneaz-harness/core/llm/registry"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	artifactsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	compactionview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/compaction"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	contextview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	corpusview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/corpus"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/shell"
	slashview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/slashcmd"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
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
	Artifacts() artifactsview.ArtifactsAPI
	Tools() tools.ToolsAPI
	Shell() shell.ShellAPI
	Slash() slashview.SlashAPI
	Corpus() corpusview.CorpusAPI
	Compaction() compactionview.CompactionAPI
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
	artifactsMgr    *coreart.Manager
	artifactsStore  coreart.Store
	artifactsAPI    artifactsview.ArtifactsAPI
	mediaStore      coreatt.MediaStore
	toolsAPI        tools.ToolsAPI
	shellImpl       *shell.API
	shellAPI        shell.ShellAPI
	slashAPI        slashview.SlashAPI
	corpusMgr       *corecorpus.Manager
	corpusAPI       corpusview.CorpusAPI
	compactionAPI   compactionview.CompactionAPI

	// stdioPool is the production *stdio.Pool wired into newLLMStack.
	// Held on the API value so the tools view's InstallRecipe /
	// UninstallRecipe path can call OpenOne / CloseOne against the
	// same pool the toolloop dispatches against.
	stdioPool *stdio.Pool

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
//
// The shell view also captures this ctx — runtime.BrowserOpenURL has
// the same context-validation behaviour as EventsEmit.
func (a *API) SetContext(ctx context.Context) {
	if a.bindings != nil {
		a.bindings.SetContext(ctx)
	}
	if a.broker != nil {
		a.broker.SetContext(ctx)
	}
	if a.shellImpl != nil {
		a.shellImpl.SetContext(ctx)
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
	media := newMediaStore(c)
	attMgr := newAttachmentsManager(c, media)
	artStore, artMgr := newArtifactsStack(c, media)
	a := &API{
		core:           c,
		a2aAPI:         &stubA2A{},
		workflowAPI:    &stubWorkflow{},
		sessionsAPI:    newSessionsAPI(c, attMgr, artStore, media),
		trustAPI:       &stubTrust{},
		contextAPI:     &stubContext{},
		policyAPI:      &stubPolicy{},
		projectsAPI:    newProjectsAPI(c),
		attachmentsMgr: attMgr,
		artifactsMgr:   artMgr,
		artifactsStore: artStore,
		mediaStore:     media,
	}
	a.attachmentsAPI = newAttachmentsAPI(c, attMgr)
	a.artifactsAPI = newArtifactsAPI(c, artStore, artMgr, media)
	a.broker = NewStreamBroker(WailsEmitter{})
	a.auditImpl = audit.NewAPI(audit.WithSubscriber(a.broker))
	a.auditAPI = a.auditImpl
	a.mcpAPI = mcp.NewAPI(mcp.WithSubscriber(a.broker))
	contextsAPI, contextsLib := newContextsAPI(c)
	a.contextsAPI = contextsAPI
	startContextsWatcher(contextsLib, a.broker)

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
	confirmEachEnabled := func() bool {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return true
		}
		v, err := settingsImpl.Store().LoadConfirmEach()
		if err != nil {
			return true
		}
		return v
	}
	// Artifacts sink — wires the code-block detector at the
	// assistant-finalize site and the tool-output detector against
	// the toolloop's post-tool-use hook listener fan-out. The sink is
	// shared between the LLM view's Config.Artifacts and the
	// toolloop's RegisterPostListener registration so a single
	// instance owns both capture paths.
	var artifactSink artifactsview.ArtifactSink
	var artifactSinkConcrete *artifactsview.Sink
	if a.artifactsMgr != nil {
		cfgFn := func() coreart.CaptureConfig {
			cfg := coreart.DefaultCaptureConfig()
			if settingsImpl != nil && settingsImpl.Store() != nil {
				if loaded, err := settingsImpl.Store().LoadAll(); err == nil {
					cfg.AutoCaptureCodeBlocks = loaded.AutoCaptureCodeBlocks()
					cfg.AutoCaptureToolOutputs = loaded.AutoCaptureToolOutputs()
					cfg.CodeBlockMinLines = loaded.EffectiveCodeBlockMinLines()
					cfg.CodeBlockMinBytes = loaded.EffectiveCodeBlockMinBytes()
				}
			}
			return cfg
		}
		artifactSinkConcrete = artifactsview.NewSinkConcrete(a.artifactsMgr, cfgFn, nil)
		artifactSink = artifactSinkConcrete
	}

	stack := newLLMStack(c, a.broker, personalForLLM, hooksRunner, attMgr, confirmEachEnabled, artifactSink, artifactSinkConcrete)
	a.llmAPI = stack.api
	a.stdioPool = stack.pool
	if c != nil && a.stdioPool != nil {
		c.SetMCP(a.stdioPool)
		// Persisted-recipes bootstrap — Core.Start invokes this once
		// Storage() is up, so the pool is populated before the chat
		// surface accepts a turn (FR-030).
		c.SetMCPRecipeBootstrap(makeMCPRecipeBootstrap(c, a.stdioPool, stack.secrets))
	}
	a.toolsAPI = newToolsAPI(c, stack.pool, stack.secrets)
	a.shellImpl = shell.New(nil)
	a.shellAPI = a.shellImpl
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

	// Slash-command surface — registry constructed against narrow
	// adapters over the session manager (for /clear) and the LLM
	// connector view (for /model). A construction failure soft-fails
	// to a nil-registry surface; the chassis still boots and Execute
	// returns a friendly "not wired" error.
	slashRegistry := newSlashRegistry(c, a.llmAPI)
	a.slashAPI = slashview.New(slashRegistry)

	// Corpora subsystem (mission agent-kernel-graph; Bundle C). Wired
	// only when the chassis has a real DataDir + storage; otherwise the
	// view falls back to ErrManagerUnavailable so the frontend renders
	// an empty state.
	a.corpusMgr = newCorpusManager(c, embedder)
	a.corpusAPI = corpusview.New(a.corpusMgr)

	a.bindings = NewBindings(a)
	if a.settingsImpl != nil {
		a.bindings.SetSettingsStore(a.settingsImpl.Store())
	}
	return a
}

// newSlashRegistry wires the slash-command registry against the
// session manager (used by /clear) and the LLM connector view (used
// by /model). Returns nil when neither dependency is available; the
// view degrades to a friendly error response on every Execute.
func newSlashRegistry(c *core.Core, llmAPI llm.LLMConnectorAPI) *coreslashcmd.Registry {
	deps := coreslashcmd.Deps{}
	if c != nil && c.SessionManager() != nil {
		deps.Sessions = &slashSessionAppender{mgr: c.SessionManager()}
	}
	if llmAPI != nil {
		deps.Providers = &slashProviderLister{inner: llmAPI}
	}
	registry, err := coreslashcmd.NewRegistry(deps)
	if err != nil {
		logging.L().Warn("slashcmd.registry.construct_failed", "err", err.Error())
		return nil
	}
	return registry
}

// slashSessionAppender adapts session.Manager into the narrow
// SessionAppender contract /clear consumes. Maps the manager's
// AppendMessage shape onto the slashcmd-side AppendSystemMessage
// shape (always role=system, returns the persisted message id).
type slashSessionAppender struct {
	mgr *session.Manager
}

func (a *slashSessionAppender) AppendSystemMessage(ctx context.Context, sessionID, content string) (string, error) {
	if a == nil || a.mgr == nil {
		return "", errors.New("slashcmd: session manager not wired")
	}
	stored, err := a.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleSystem,
		Content: content,
	})
	if err != nil {
		return "", err
	}
	return stored.ID, nil
}

// slashProviderLister adapts the LLM connector view into the
// ProviderLister contract /model consumes. The translation projects
// the rich llm.Provider shape onto the smaller slashcmd.Provider so
// the slashcmd package never imports the rpc/views/llm package.
type slashProviderLister struct {
	inner llm.LLMConnectorAPI
}

func (a *slashProviderLister) ListProviders(ctx context.Context) ([]coreslashcmd.Provider, error) {
	if a == nil || a.inner == nil {
		return nil, nil
	}
	rows, err := a.inner.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]coreslashcmd.Provider, 0, len(rows))
	for _, p := range rows {
		out = append(out, coreslashcmd.Provider{
			ID:           p.ID,
			Name:         p.Name,
			Kind:         p.Kind,
			DefaultModel: p.Model,
			Models:       append([]string(nil), p.Models...),
		})
	}
	return out, nil
}

// newSessionsAPI returns the real Manager-backed SessionsAPI when c
// is non-nil; otherwise a noop stub for callers that pass New(nil)
// (see api_test.go's TestViewAccessorStability).
//
// When attMgr is non-nil the returned impl drives the attachments table
// for SetSystemPrompt, with the session.system_prompt column kept for
// the one-release compat buffer.
//
// artStore + media plumb the artifacts cascade extension (FR-014):
// session-delete reads the artifact list, drops the rows via the
// session FK CASCADE, then refcount-sweeps any orphaned CAS files.
// Both nil falls back to the pre-WP02 cascade (attachments + session
// row, no artifacts cleanup).
func newSessionsAPI(c *core.Core, attMgr *coreatt.Manager, artStore coreart.Store, media coreatt.MediaStore) sessions.SessionsAPI {
	if c == nil {
		return &stubSessions{}
	}
	if attMgr == nil {
		return sessions.NewManagerAPI(c.SessionManager())
	}
	return sessions.NewManagerAPIWithAttachmentsAndArtifacts(c.SessionManager(), attMgr, artStore, media, c.DataDir())
}

// newProjectsAPI returns the real Manager-backed ProjectsAPI when c is
// non-nil; otherwise a noop stub.
func newProjectsAPI(c *core.Core) projectsview.ProjectsAPI {
	if c == nil {
		return &stubProjects{}
	}
	return projectsview.New(c.ProjectManager(), c.SessionManager())
}

// newMediaStore constructs the core/attachments.MediaStore that owns
// the on-disk CAS at <DataDir>/media/. Returns nil when c is nil or
// storage isn't available. The returned store has the multimodal-io
// AttachmentsRefcountSource pre-registered; the artifacts mission's
// ArtifactsRefcountSource is registered separately by the caller
// after the artifacts store has been constructed.
func newMediaStore(c *core.Core) coreatt.MediaStore {
	if c == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	media := coreatt.NewSQLMediaStore(s, c.DataDir())
	media.RegisterRefcountSource(coreatt.AttachmentsRefcountSource{DB: s})
	return media
}

// newAttachmentsManager constructs the core/attachments.Manager
// against the supplied (already-constructed) MediaStore. Returns nil
// when either core or media is nil; the rpc surface treats nil as
// "attachments disabled" and the SessionsAPI / LLM stack fall back
// to legacy behaviour.
func newAttachmentsManager(c *core.Core, media coreatt.MediaStore) *coreatt.Manager {
	if c == nil || media == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	return coreatt.NewManager(
		coreatt.NewSQLStore(s),
		coreatt.WithMediaStore(media),
	)
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

// newArtifactsStack constructs the artifacts Store + Manager and
// wires the ArtifactsRefcountSource onto the supplied MediaStore so
// the composite refcount sees both attachments AND artifacts before
// any on-disk file is reclaimed. Returns (nil, nil) when core or
// media is nil; the chassis treats that as "artifacts disabled" and
// the rpc surface falls back to the noop sink + stub view.
func newArtifactsStack(c *core.Core, media coreatt.MediaStore) (coreart.Store, *coreart.Manager) {
	if c == nil || media == nil {
		return nil, nil
	}
	s := c.Storage()
	if s == nil {
		return nil, nil
	}
	store := coreart.NewSQLStore(s,
		coreart.WithSessionProjectReader(&artifactSessionProjectReader{mgr: c.SessionManager()}),
	)
	// Register the artifacts refcount source on the SHARED MediaStore
	// (the same instance the attachments manager already wired the
	// AttachmentsRefcountSource into). This is the WP02 risk-note
	// hookup: the on-disk file is only reclaimed when no attachments
	// row AND no artifacts row references the hash.
	media.RegisterRefcountSource(coreart.ArtifactsRefcountSource{Store: store})
	mgr := coreart.NewManager(store, &mediaStorePutAdapter{inner: media},
		coreart.WithSessionReader(&artifactSessionProjectReader{mgr: c.SessionManager()}),
	)
	return store, mgr
}

// newArtifactsAPI returns the real Store + Manager-backed
// ArtifactsAPI when wired; otherwise a noop stub keeps the chassis
// bootable.
func newArtifactsAPI(c *core.Core, store coreart.Store, mgr *coreart.Manager, media coreatt.MediaStore) artifactsview.ArtifactsAPI {
	if c == nil || store == nil || mgr == nil || media == nil {
		return &stubArtifacts{}
	}
	return artifactsview.New(artifactsview.Config{
		Store:    store,
		Manager:  mgr,
		Media:    media,
		Messages: &artifactMessageReader{mgr: c.SessionManager()},
		DataDir:  c.DataDir(),
	})
}

// artifactSessionProjectReader adapts session.Manager into the narrow
// SessionProjectReader the artifacts package expects. Empty-string
// projectID means "session has no project"; matches the artifacts
// package's "skip the promote" contract.
type artifactSessionProjectReader struct {
	mgr *session.Manager
}

func (r *artifactSessionProjectReader) SessionProject(ctx context.Context, sessionID string) (string, error) {
	if r == nil || r.mgr == nil {
		return "", nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if rec.ProjectID == nil {
		return "", nil
	}
	return *rec.ProjectID, nil
}

// mediaStorePutAdapter projects the wider MediaStore surface onto the
// narrow MediaStorer interface the artifacts manager consumes (Put
// only). Lets the artifacts manager stay decoupled from the on-disk
// + SQL details while sharing the same store instance.
type mediaStorePutAdapter struct {
	inner coreatt.MediaStore
}

func (a *mediaStorePutAdapter) Put(ctx context.Context, b []byte, mediaType, originalName string) (coreatt.MediaArtifact, error) {
	return a.inner.Put(ctx, b, mediaType, originalName)
}

// artifactMessageReader adapts session.Manager to the artifacts view's
// MessageReader. Used by SaveFromMessage to pull the message text by
// (session, message) id.
type artifactMessageReader struct {
	mgr *session.Manager
}

func (r *artifactMessageReader) GetMessage(ctx context.Context, sessionID, messageID string) (artifactsview.Message, error) {
	if r == nil || r.mgr == nil {
		return artifactsview.Message{}, errors.New("rpc: session manager not wired")
	}
	msgs, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return artifactsview.Message{}, err
	}
	for _, m := range msgs {
		if m.ID == messageID {
			return artifactsview.Message{
				ID:        m.ID,
				SessionID: m.SessionID,
				Role:      string(m.Role),
				Content:   m.Content,
			}, nil
		}
	}
	return artifactsview.Message{}, fmt.Errorf("rpc: message %q not found in session %q", messageID, sessionID)
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

// makeMCPRecipeBootstrap returns a closure suitable for
// Core.SetMCPRecipeBootstrap. The closure walks the persisted enabled-
// recipes list, resolves env via the shared secrets backend, builds
// ServerSpec values via recipe.ToServerSpec, and Opens them onto the
// pool. Per-recipe failures (missing required env, OS-keychain entry
// purged out-of-band, recipe id no longer in the catalog) log at warn
// and skip — the chat surface stays usable without that recipe's
// tools (FR-030).
func makeMCPRecipeBootstrap(c *core.Core, pool *stdio.Pool, secretsBackend *secrets.MemoryBackend) func(context.Context) error {
	if c == nil || pool == nil || secretsBackend == nil {
		return nil
	}
	return func(ctx context.Context) error {
		dataDir := c.DataDir()
		if dataDir == "" {
			return nil
		}
		enabled, err := recipes.LoadEnabled(dataDir)
		if err != nil {
			return fmt.Errorf("rpc: load enabled recipes: %w", err)
		}
		catalog := recipes.Shipped()
		entries := enabled.List()
		specs := make([]coremcp.ServerSpec, 0, len(entries))
		for _, entry := range entries {
			recipe, ok := catalog.Get(entry.ID)
			if !ok {
				logging.L().Warn("rpc.mcp_bootstrap.unknown_recipe", "recipe_id", entry.ID)
				continue
			}
			resolved, err := recipes.ResolveEnv(ctx, secretsBackend, recipe)
			if err != nil {
				// Most likely cause: the user uninstalled the OS
				// keychain entry out-of-band, so a required env
				// key fails to resolve. Skip + log; the user can
				// re-install the recipe from the Tools panel.
				logging.L().Warn("rpc.mcp_bootstrap.resolve_env_failed",
					"recipe_id", entry.ID, "err", err.Error())
				continue
			}
			specs = append(specs, recipe.ToServerSpec(resolved, entry.Config))
		}
		if len(specs) == 0 {
			return nil
		}
		if err := pool.Open(ctx, specs); err != nil {
			// Pool.Open aggregates per-spec failures; treat the
			// aggregate as non-fatal so a single bad recipe
			// doesn't prevent the others from coming up.
			logging.L().Warn("rpc.mcp_bootstrap.partial_open", "err", err.Error())
		}
		return nil
	}
}

// newToolsAPI constructs the view-scoped Tools surface. The view
// shares the same *stdio.Pool the toolloop dispatches against (so
// install/uninstall is visible to the chat surface immediately) and
// the same in-memory secrets backend the LLM stack uses (so newly-
// staged keychain entries are visible to the next ResolveEnv without
// an OS round-trip).
//
// Returns the stub when c is nil — the test harness path constructs
// rpc.New(nil) and we keep the chassis bootable without crashing on
// the catalog access.
func newToolsAPI(c *core.Core, pool *stdio.Pool, secretsBackend *secrets.MemoryBackend) tools.ToolsAPI {
	if c == nil {
		return &stubTools{}
	}
	dataDir := c.DataDir()
	enabled, err := recipes.LoadEnabled(dataDir)
	if err != nil {
		logging.L().Warn("tools.load_enabled_failed", "data_dir", dataDir, "err", err.Error())
		enabled = &recipes.EnabledRecipes{}
	}
	cfg := tools.Config{
		Catalog:   recipes.Shipped(),
		Enabled:   enabled,
		Pool:      pool,
		Secrets:   secretsBackend,
		DataDir:   dataDir,
		Audit:     nil, // TODO(audit-wired): reuse process-wide event.Emitter once it's available
		Keychain:  &keychainWriter{backend: secretsBackend},
		Forgetter: &keychainForgetter{backend: secretsBackend},
	}
	return tools.New(cfg)
}

// keychainForgetter is the deletion counterpart to keychainWriter.
// It pops the locator out of the OS keychain (best-effort — Linux
// installs without libsecret return an error, which we swallow) and
// out of the in-memory secrets backend.
type keychainForgetter struct {
	backend *secrets.MemoryBackend
}

func (f *keychainForgetter) Forget(_ context.Context, locator string) error {
	if f == nil {
		return nil
	}
	// OS-keychain is best-effort: a missing entry on the deletion
	// path is non-fatal.
	_ = keyring.Delete(keyringService, locator)
	if f.backend != nil {
		f.backend.ClearEntry(secretsref.RefKeychain, locator)
	}
	return nil
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
//
// confirmEachEnabled is read on every loop construction (effectively
// every newLLMStack invocation, which is once at boot). The flag is
// captured into the loop's Config rather than re-read per call — the
// rpc layer's settings binding mutates the FileStore directly, and a
// process restart picks up the new value. This keeps the loop's
// per-Run hot path free of settings-store I/O.
// llmStack bundles the artefacts newLLMStack constructs that the
// outer New func also needs to wire into other views (the tools view
// shares the same *stdio.Pool the toolloop dispatches against, and the
// shared secrets backend is what InstallRecipe writes credentials
// into so the resolver finds them on the next ResolveEnv).
type llmStack struct {
	api     llm.LLMConnectorAPI
	pool    *stdio.Pool
	secrets *secrets.MemoryBackend
}

func newLLMStack(
	c *core.Core,
	broker *StreamBroker,
	store personal.Store,
	hooksRunner llm.HookRunner,
	attMgr *coreatt.Manager,
	confirmEachEnabled func() bool,
	artifactSink llm.ArtifactSink,
	artifactSinkConcrete *artifactsview.Sink,
) llmStack {
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
		return llmStack{api: &stubLLM{}, secrets: secretsBackend}
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
	// WP03 — pre/post-tool-use hooks and audit emission. core/hooks
	// only exposes pre_send / post_send for chat-pipeline events;
	// there is no pre_tool_use / post_tool_use surface there yet, so
	// the toolloop falls back to its built-in noopHookRunner. A real
	// runner lands when core/hooks grows the tool-use lifecycle (see
	// kitty-specs/mcp-tool-execution-01KQ3JCS/spec.md FR-003).
	//
	// TODO(audit): wire a real AuditEmitter once the rpc layer
	// materializes a process-wide event.Emitter (core/event.NewEmitter
	// + redact.Pipeline). Until then audit emission is silenced and
	// the privacy-CI guard is "no emitter, no leak".
	// WP05 — confirm-each modal flow. The gateway brokers the
	// pause/resume between the toolloop (which blocks on
	// RequestConfirm) and the frontend (which calls ResolveConfirm
	// via the LLM_ResolveConfirm Wails binding). The same broker
	// powers chat stream chunks so the modal subscribes to the same
	// topic the rest of the chat surface already consumes.
	confirmGateway := llm.NewConfirmGateway(&streamSinkAdapter{broker: broker})
	flagOn := true
	if confirmEachEnabled != nil {
		flagOn = confirmEachEnabled()
	}
	// Stdio MCP pool — empty at boot. Persisted recipes are spawned
	// onto this pool from core.Core.Start (so they're up before the
	// chat surface accepts a turn), and the tools view's
	// InstallRecipe / UninstallRecipe paths use the same pool's
	// OpenOne / CloseOne for dynamic add/remove.
	dataDir := ""
	if c != nil {
		dataDir = c.DataDir()
	}
	mcpPool := stdio.NewPool(stdio.PoolOptions{
		Sampler: stdio.LLMSamplingHandler(reg, func() (string, string) {
			// v1: no active-provider selector wired yet — sampling
			// callbacks land on whichever profile resolves first via
			// the registry's Stream contract. The user-trust mission
			// owns the explicit selector knob.
			return "", ""
		}),
		Roots:  stdio.DefaultRoots(dataDir, nil),
		Broker: &poolEventPublisher{broker: broker},
		Logger: nil, // defaults to slog.Default
	})
	// Toolloop hooks runner — we hand-pick a listener-friendly noop
	// runner so the artifacts sink can subscribe via
	// RegisterPostListener. Without an explicit instance the loop
	// would build its own internal default and we'd have no handle
	// to register the listener against.
	toolloopHooks := toolloop.NewNoopHookRunner()
	if artifactSinkConcrete != nil {
		toolloopHooks.RegisterPostListener(artifactSinkConcrete.PostListener())
	}
	loop, loopErr := toolloop.New(toolloop.Config{
		Registry:           reg,
		Pool:               &mcpPoolAdapter{inner: mcpPool},
		History:            historyAdapter,
		Permissions:        perms,
		Hooks:              toolloopHooks,
		Audit:              nil,
		Confirm:            confirmGateway,
		ConfirmEachEnabled: flagOn,
		// OverrideWriter intentionally nil for now — the C2 session
		// override writer hasn't landed yet, and the confirm gate
		// degrades to per-call allow/deny without persistence (logged
		// at warn). Wire when sessions.MCPOverridesWriter exists.
		OverrideWriter: nil,
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
	// Tool discovery wiring — the discoverer projects the same MCP
	// pool the toolloop dispatches against onto each GenerationRequest's
	// Tools field, namespaced as "<server>__<tool>" so the toolloop can
	// split the response back into a (server, tool) pair at dispatch.
	// Without this, the model never sees any tools and the loop is
	// dead code from the user's perspective.
	toolDiscoverer := llm.NewMCPToolDiscoverer(mcpPool, perms)
	api := llm.New(llm.Config{
		Registry:       reg,
		Sink:           &streamSinkAdapter{broker: broker},
		Store:          store,
		Keychain:       &keychainWriter{backend: secretsBackend},
		Prober:         &registryProber{reg: reg, creds: credResolver},
		History:        historyAdapter,
		HistoryWriter:  &llmHistoryWriter{inner: historyAdapter},
		Hooks:          hooksRunner,
		Attachments:    attResolver,
		ToolLoop:       loop,
		Tools:          toolDiscoverer,
		ConfirmGateway: confirmGateway,
		Artifacts:      &llmArtifactSinkAdapter{inner: artifactSink},
	})
	return llmStack{api: api, pool: mcpPool, secrets: secretsBackend}
}

// poolEventPublisher adapts the rpc.StreamBroker to the stdio
// pool's EventPublisher contract. Nil broker → no-op so embedding
// tests can leave the broker unset.
type poolEventPublisher struct {
	broker *StreamBroker
}

func (p *poolEventPublisher) Publish(topic string, payload any) {
	if p == nil || p.broker == nil {
		return
	}
	p.broker.emitter.Emit(p.broker.EmitCtx(), topic, payload)
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
//
// Returns the wrapped API and the open library (or nil) so the caller
// can wire the fsnotify watcher → broker bridge.
func newContextsAPI(c *core.Core) (contextsview.ContextsAPI, *corecontexts.Library) {
	if c == nil || c.DataDir() == "" {
		return contextsview.New(nil), nil
	}
	lib, err := corecontexts.Open(filepath.Join(c.DataDir(), "contexts"))
	if err != nil {
		return contextsview.New(nil), nil
	}
	// SweepTrash is best-effort on boot — a stale trash directory
	// shouldn't keep the surface from coming up.
	_ = lib.SweepTrash()
	return contextsview.New(lib), lib
}

// startContextsWatcher wires the library's fsnotify-backed watcher into
// the StreamBroker's Wails event surface so the frontend's listener on
// "contexts:tree-changed" receives ticks for both in-process mutations
// and external writes (operator drops a file in via Finder). A failing
// StartWatching is soft — Library mutators still notify subscribers
// in-process, just not external writes.
func startContextsWatcher(lib *corecontexts.Library, broker *StreamBroker) {
	if lib == nil || broker == nil {
		return
	}
	if err := lib.StartWatching(); err != nil {
		// Sandboxed runners (some CI configurations) deny inotify;
		// the in-process notification path still works via
		// notifyOp on every Save / CreateFolder / Rename / Delete.
		logging.L().Warn("contexts.watcher.start_failed", "err", err.Error())
		return
	}
	w := lib.Subscribe()
	go func() {
		for range w.Events() {
			broker.emitter.Emit(broker.EmitCtx(), "contexts:tree-changed", struct{}{})
		}
	}()
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

// newCorpusManager wires the corpora subsystem against the chassis's
// storage + DataDir + the same embedder the memory retriever uses.
// Returns nil when c lacks a real storage handle (test path) so the
// rpc view surfaces ErrManagerUnavailable.
//
// The corpus.Embedder seam mirrors core/memory.Embedder shape so the
// adapter is trivial; embedding model parity matters because the
// vector index dimensions must align across the two subsystems if a
// future "memory pulls from corpora" path lands.
func newCorpusManager(c *core.Core, embedder corememory.Embedder) *corecorpus.Manager {
	if c == nil || c.DataDir() == "" {
		return nil
	}
	store := c.Storage()
	if store == nil {
		return nil
	}
	var corpusEmb corecorpus.Embedder
	if embedder != nil {
		if _, ok := embedder.(corememory.NoopEmbedder); !ok {
			corpusEmb = &corpusEmbedderAdapter{inner: embedder}
		}
	}
	return corecorpus.NewManager(corecorpus.NewStorageDB(store), c.DataDir(), corpusEmb)
}

// corpusEmbedderAdapter bridges core/memory.Embedder onto the narrower
// corpus.Embedder seam. The two interfaces share the Embed signature;
// keeping them disjoint avoids a corpus -> memory import edge.
type corpusEmbedderAdapter struct {
	inner corememory.Embedder
}

func (a *corpusEmbedderAdapter) Dimensions() int {
	if a == nil || a.inner == nil {
		return 0
	}
	return a.inner.Dimensions()
}

func (a *corpusEmbedderAdapter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if a == nil || a.inner == nil {
		return nil, corecorpus.ErrEmbedderUnavailable
	}
	vecs, err := a.inner.Embed(ctx, texts)
	if err != nil {
		if errors.Is(err, corememory.ErrEmbedderUnavailable) {
			return nil, corecorpus.ErrEmbedderUnavailable
		}
		return nil, err
	}
	return vecs, nil
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
			Role:          string(m.Role),
			Content:       m.Content,
			ContentBlocks: m.ContentBlocks,
		})
	}
	return out, nil
}

// AppendMessage persists the assistant turn at stream completion so a
// future ListMessages call rehydrates it. Implements the toolloop's
// SessionHistoryRW.AppendMessage shape (error-only return).
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

// llmHistoryWriter wraps sessionHistoryReader to satisfy the LLM
// view's SessionMessageWriter shape, which returns the persisted
// message id alongside the error so the post-finalize hooks
// (artifacts code-block detector) can anchor SourceRef.MessageID to
// the freshly persisted row.
type llmHistoryWriter struct {
	inner *sessionHistoryReader
}

func (w *llmHistoryWriter) ListMessages(ctx context.Context, sessionID string) ([]llm.SessionMessage, error) {
	if w == nil || w.inner == nil {
		return nil, nil
	}
	return w.inner.ListMessages(ctx, sessionID)
}

func (w *llmHistoryWriter) SystemPromptFor(ctx context.Context, sessionID string) (string, string, error) {
	if w == nil || w.inner == nil {
		return "", "", nil
	}
	return w.inner.SystemPromptFor(ctx, sessionID)
}

func (w *llmHistoryWriter) AppendMessage(ctx context.Context, sessionID, role, content string) (string, error) {
	if w == nil || w.inner == nil || w.inner.mgr == nil {
		return "", nil
	}
	stored, err := w.inner.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	if err != nil {
		return "", err
	}
	return stored.ID, nil
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

// llmArtifactSinkAdapter bridges the artifacts view's ArtifactSink
// shape onto the llm view's structurally-identical interface. The
// indirection keeps the import direction one-way: the llm view does
// not import the artifacts view, and a nil inner sink is treated as
// "no artifacts wired" (chat path stays clean).
type llmArtifactSinkAdapter struct {
	inner artifactsview.ArtifactSink
}

func (a *llmArtifactSinkAdapter) OnAssistantMessage(ctx context.Context, sessionID, messageID, text string) error {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.OnAssistantMessage(ctx, sessionID, messageID, text)
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
func (a *API) Artifacts() artifactsview.ArtifactsAPI {
	if a.artifactsAPI == nil {
		return &stubArtifacts{}
	}
	return a.artifactsAPI
}
func (a *API) Tools() tools.ToolsAPI {
	if a.toolsAPI == nil {
		return &stubTools{}
	}
	return a.toolsAPI
}
func (a *API) Shell() shell.ShellAPI {
	if a.shellAPI == nil {
		return &stubShell{}
	}
	return a.shellAPI
}
func (a *API) Slash() slashview.SlashAPI {
	if a.slashAPI == nil {
		// nil-registry surface — Execute returns the friendly
		// "not wired" result; List returns empty.
		return slashview.New(nil)
	}
	return a.slashAPI
}
func (a *API) Corpus() corpusview.CorpusAPI {
	if a.corpusAPI == nil {
		// nil-manager surface — methods return ErrManagerUnavailable
		// so the frontend renders the empty state.
		return corpusview.New(nil)
	}
	return a.corpusAPI
}

// Compaction returns the configurable-compaction view (mission
// agent-kernel-graph; Bundle D WP12/WP13). The defensive nil-pipeline
// branch keeps parallel-agent edits safe: callers may construct an
// API value before the kernel wires the pipeline in.
func (a *API) Compaction() compactionview.CompactionAPI {
	if a.compactionAPI == nil {
		return compactionview.New(nil)
	}
	return a.compactionAPI
}

// SetCompactionAPI wires the compaction view onto the API. Production
// chassis calls this once the kernel constructs its pipeline; tests
// pass a fake.
func (a *API) SetCompactionAPI(c compactionview.CompactionAPI) {
	a.compactionAPI = c
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
