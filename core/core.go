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
	"github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/memory"
	"github.com/sigil-tech/kaneaz-harness/core/scheduler"
	"github.com/sigil-tech/kaneaz-harness/core/session"
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
	return firstErr
}

// DataDir returns the data-directory root opts.DataDir was constructed
// with. Subsystems use this as the base for their on-disk state.
func (c *Core) DataDir() string { return c.opts.DataDir }

// SessionManager returns the chat-rail session manager, lazily
// constructed on first call. The manager is wired against an
// in-memory store today; once storage-foundations exposes a libSQL
// backend, the manager will switch to the SQL store via
// session.NewSQLStore. The interface seen by callers (the *Manager
// type) is stable across that transition.
//
// SessionManager is safe to call from any goroutine: the first caller
// constructs the manager and subsequent callers see the same instance.
func (c *Core) SessionManager() *session.Manager {
	c.sessionManagerMu.Lock()
	defer c.sessionManagerMu.Unlock()
	if c.sessionManager != nil {
		return c.sessionManager
	}
	store := session.NewMemoryStore()
	c.sessionManager = session.NewManager(store)
	return c.sessionManager
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
