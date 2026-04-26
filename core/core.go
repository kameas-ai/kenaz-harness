// Package core is the harness's top-level lifecycle facade. It wires
// the cross-mission subsystems (event log, bundle resolver, blob store,
// session executor, scheduler, llm registry, mcp pool, secrets) into a
// single Start/Shutdown surface that the embedding application drives.
//
// SEAM 1 wiring contract:
//
//   - Subsystem field types remain interfaces / public façade types so
//     that embedding applications can substitute fakes in tests without
//     forcing core to depend on every concrete subsystem at boot.
//   - Subsystems whose construction needs only opts.DataDir (today: the
//     bundle CAS) are constructed lazily on first access. Subsystems
//     that need a SQLite DB (event-log Emitter, secrets cache rotation
//     listener, ACP task store) are deferred to Start so the
//     storage-foundations DB has time to come up first.
//   - Shutdown invokes every wired subsystem's Close exactly once and
//     swallows errors after the first one so a downstream failure
//     cannot mask an upstream one.
package core

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/blob"
	"github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/bundle/cache"
	"github.com/sigil-tech/kaneaz-harness/core/config"
	"github.com/sigil-tech/kaneaz-harness/core/event"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/memory"
	"github.com/sigil-tech/kaneaz-harness/core/projects"
	"github.com/sigil-tech/kaneaz-harness/core/scheduler"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// Options carries the bootstrap configuration passed to New. DataDir
// is the only required field; subsystem instances may be wired in
// directly for embedding tests via the optional Subsystems struct.
type Options struct {
	DataDir string

	// Subsystems lets an embedding application inject pre-constructed
	// subsystems (e.g. a fake event-log Emitter for tests) instead of
	// relying on Core's lazy defaults. Fields left zero fall through
	// to the default lazy-init path in Start.
	Subsystems Subsystems
}

// Subsystems is the explicit subsystem-injection record. Every field
// is optional: nil values fall through to Core's default construction
// in Start. Tests fill the fields they care about and ignore the rest.
type Subsystems struct {
	Events    event.Log
	Bundles   bundle.Resolver
	Sessions  session.Executor
	Scheduler scheduler.Scheduler
	Blobs     blob.CAS
	Config    config.Store
	Memory    memory.View
	LLMs      llm.Registry
	MCP       mcp.Pool
}

// Core is the harness's top-level lifecycle facade. Subsystem fields
// are populated by New (from Options.Subsystems) and Start (lazy
// defaults). Embedding applications read the fields directly; the
// fields are only mutated during Start/Shutdown.
type Core struct {
	opts Options

	Events    event.Log
	Bundles   bundle.Resolver
	Sessions  session.Executor
	Scheduler scheduler.Scheduler
	Blobs     blob.CAS
	Config    config.Store
	Memory    memory.View
	LLMs      llm.Registry
	MCP       mcp.Pool

	// bundleCacheMu guards bundleCache lazy construction so concurrent
	// callers do not race during the first BundleCache() / Start() call.
	bundleCacheMu sync.Mutex
	bundleCache   cache.CAS

	// sessionManagerMu guards sessionManager lazy construction.
	sessionManagerMu sync.Mutex
	sessionManager   *session.Manager

	// projectManagerMu guards projectManager lazy construction.
	projectManagerMu sync.Mutex
	projectManager   *projects.Manager

	// storageMu guards lazy construction of the unified storage handle.
	// storageOpened tracks whether the lazy-init path has run, so a
	// failed Open is not retried on every Storage() call.
	storageMu     sync.Mutex
	storage       storage.DB
	storageOpened bool
}

// New constructs a Core. It validates DataDir and wires any
// pre-constructed subsystems supplied via opts.Subsystems. New is
// non-blocking and does no I/O — subsystem construction that needs
// the filesystem or a database connection happens in Start.
func New(opts Options) (*Core, error) {
	if opts.DataDir == "" {
		return nil, errors.New("core: DataDir required")
	}
	c := &Core{opts: opts}
	c.Events = opts.Subsystems.Events
	c.Bundles = opts.Subsystems.Bundles
	c.Sessions = opts.Subsystems.Sessions
	c.Scheduler = opts.Subsystems.Scheduler
	c.Blobs = opts.Subsystems.Blobs
	c.Config = opts.Subsystems.Config
	c.Memory = opts.Subsystems.Memory
	c.LLMs = opts.Subsystems.LLMs
	c.MCP = opts.Subsystems.MCP
	// Run the legacy sessions.db → data.db migration before any consumer
	// (notably rpc.New → c.SessionManager → c.Storage) can open the
	// unified DB and grab the lock. The migration opens its own
	// short-lived handle to apply schema + ATTACH/copy, so it must run
	// while the harness lock is unowned.
	if err := storagesqlite.MigrateLegacySessionsDB(opts.DataDir); err != nil {
		logging.L().Error("storage.legacy_sessions.migrate_failed",
			"data_dir", opts.DataDir, "err", err.Error())
	}
	return c, nil
}

// Start brings every wired subsystem online. It is safe to call once;
// repeated calls are not supported.
//
// Lazy-initialization contract: subsystems that need only opts.DataDir
// are constructed here if they were not supplied via opts.Subsystems.
// Subsystems that need a SQLite DB (event-log Emitter, ACP task store,
// secrets cache rotation listener) are deferred to the embedder; Core
// itself does not own the DB lifecycle yet (see storage-foundations
// mission for that wiring).
func (c *Core) Start(ctx context.Context) error {
	// Touch Storage() so any deferred migrations and the lockfile claim
	// happen on Start rather than on first session-manager access. The
	// legacy sessions.db → data.db migration ran in New(), before any
	// consumer could grab the lock.
	if c.opts.DataDir != "" {
		_ = c.Storage()
	}
	if c.Scheduler != nil {
		if err := c.Scheduler.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown tears down every wired subsystem in reverse-construction
// order. Errors are collected and the first one is returned; subsequent
// errors are swallowed so a downstream failure cannot mask an upstream
// one (the embedder logs the first; subsequent failures are usually
// caused by the first).
func (c *Core) Shutdown(ctx context.Context) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.Scheduler != nil {
		record(c.Scheduler.Stop(ctx))
	}
	if c.MCP != nil {
		record(c.MCP.Close(ctx))
	}
	if c.Events != nil {
		record(c.Events.Close())
	}
	c.storageMu.Lock()
	if c.storage != nil {
		record(c.storage.Close(ctx))
		c.storage = nil
	}
	c.storageMu.Unlock()
	return firstErr
}

// DataDir returns the data-directory root opts.DataDir was constructed
// with. Subsystems use this as the base for their on-disk state.
func (c *Core) DataDir() string { return c.opts.DataDir }

// SessionManager returns the chat-rail session manager, lazily
// constructed on first call. The manager is wired against the unified
// storage.DB once it is available; an open / migration failure
// degrades gracefully to the in-memory store so the chassis still boots.
//
// SessionManager is safe to call from any goroutine: the first caller
// constructs the manager and subsequent callers see the same instance.
func (c *Core) SessionManager() *session.Manager {
	c.sessionManagerMu.Lock()
	defer c.sessionManagerMu.Unlock()
	if c.sessionManager != nil {
		return c.sessionManager
	}
	store := c.openSessionStore()
	c.sessionManager = session.NewManager(store)
	return c.sessionManager
}

// ProjectManager returns the projects facade, lazily constructed on
// first call. Falls back to an in-memory store when storage is
// unavailable so the chassis still boots without persistence.
func (c *Core) ProjectManager() *projects.Manager {
	c.projectManagerMu.Lock()
	defer c.projectManagerMu.Unlock()
	if c.projectManager != nil {
		return c.projectManager
	}
	c.projectManager = projects.NewManager(c.openProjectStore())
	return c.projectManager
}

func (c *Core) openProjectStore() projects.Store {
	if c.opts.DataDir == "" {
		logging.L().Warn("projects.store.fallback_memory", "reason", "empty data dir")
		return projects.NewMemoryStore()
	}
	s := c.Storage()
	if s == nil {
		logging.L().Warn("projects.store.fallback_memory", "reason", "storage unavailable")
		return projects.NewMemoryStore()
	}
	return projects.NewSQLStore(s)
}

// openSessionStore wraps the unified storage.DB through
// session.NewStorageDB and returns a SQL-backed store. It falls back to
// the in-memory store when storage is unavailable so callers see a
// usable manager either way (with the known limitation that sessions
// evaporate on restart).
func (c *Core) openSessionStore() session.Store {
	if c.opts.DataDir == "" {
		logging.L().Warn("session.store.fallback_memory", "reason", "empty data dir")
		return session.NewMemoryStore()
	}
	s := c.Storage()
	if s == nil {
		logging.L().Warn("session.store.fallback_memory", "reason", "storage unavailable")
		return session.NewMemoryStore()
	}
	logging.L().Info("session.store.opened", "path", filepath.Join(c.opts.DataDir, "data.db"))
	return session.NewSQLStore(session.NewStorageDB(s))
}

// Storage returns the unified harness DB, lazily opened on first call.
// A construction failure is logged once and the method returns nil for
// the lifetime of this Core; callers must handle nil and substitute a
// degraded code path.
func (c *Core) Storage() storage.DB {
	c.storageMu.Lock()
	defer c.storageMu.Unlock()
	if c.storageOpened {
		return c.storage
	}
	c.storageOpened = true
	if c.opts.DataDir == "" {
		logging.L().Warn("storage.unavailable", "reason", "empty data dir")
		return nil
	}
	cfg := storage.Config{
		DataDir:          c.opts.DataDir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		logging.L().Error("storage.open_failed",
			"data_dir", c.opts.DataDir, "err", err.Error())
		return nil
	}
	c.storage = db
	return db
}

// BundleCache returns the bundle layer's content-addressable storage,
// constructed lazily under <DataDir>/cache on first call. The CAS owns
// the sha256/, staging/, and manifests/ subtrees; nothing outside of
// core/bundle/cache may write to those paths directly.
//
// SEAM 6 wiring: the CAS root is derived from opts.DataDir at this
// call site; no bundle path is hardcoded elsewhere in the harness.
// Concurrent callers see the same instance after the first
// successful construction.
func (c *Core) BundleCache() (cache.CAS, error) {
	c.bundleCacheMu.Lock()
	defer c.bundleCacheMu.Unlock()
	if c.bundleCache != nil {
		return c.bundleCache, nil
	}
	if c.opts.DataDir == "" {
		return nil, errors.New("core: BundleCache: DataDir empty")
	}
	cas, err := cache.New(filepath.Join(c.opts.DataDir, "cache"))
	if err != nil {
		return nil, err
	}
	c.bundleCache = cas
	return cas, nil
}
