package capabilities

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/storage"
)

const (
	// cacheTTL is the duration after which a cached capability record is
	// considered stale and a background refresh is triggered (WP05).
	cacheTTL = 7 * 24 * time.Hour

	// EnvCapabilityCache is the env var that selects the cache backend.
	// Values: "sqlite" | "memory" | "off". Default (unset): "sqlite" when
	// a real storage.DB handle is available to DefaultCache, "memory"
	// otherwise (model-settings-reach-the-model-01PMZ101 WP12 correction,
	// 2026-09-15 — see DefaultCache's own doc comment for why the
	// previous "unset = memory, always" default left the persistent
	// backend unreachable in every shipped binary).
	EnvCapabilityCache = "HARNESS_LLM_CAPABILITY_CACHE"
)

// CapabilityCache is the interface all cache backends implement.
// Get and Put are the hot paths; Invalidate is called when a profile
// is edited or when the schema version changes.
type CapabilityCache interface {
	// Get retrieves a cached ProviderCapabilities for (profileID, modelID).
	// Returns (zero, false) on cache miss.
	Get(ctx context.Context, profileID, modelID string) (llm.ProviderCapabilities, bool)
	// Put stores a ProviderCapabilities. Implementations must store the
	// current schema version so stale entries can be invalidated on startup.
	Put(ctx context.Context, profileID, modelID string, caps llm.ProviderCapabilities) error
	// Invalidate removes all entries for profileID. Called on profile edit.
	Invalidate(ctx context.Context, profileID string) error
	// InvalidateSchemaVersion removes all entries whose capability_schema_version
	// differs from llm.CapabilitySchemaVersion. Called on startup.
	InvalidateSchemaVersion(ctx context.Context) error
}

// cacheEntry is one in-memory cache record.
type cacheEntry struct {
	caps      llm.ProviderCapabilities
	fetchedAt time.Time
	version   int
}

// MemoryCache is a lightweight in-process cache backed by a sync.Map.
// It is the default when HARNESS_LLM_CAPABILITY_CACHE="memory", and the
// degrade path when it is unset/"sqlite" but no storage.DB is available.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry // key = profileID + "\x00" + modelID
}

// NewMemoryCache returns an empty in-memory capability cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		entries: make(map[string]cacheEntry),
	}
}

func cacheKey(profileID, modelID string) string {
	return profileID + "\x00" + modelID
}

// Get retrieves a cached record. Returns (zero, false) on cache miss or stale.
func (c *MemoryCache) Get(_ context.Context, profileID, modelID string) (llm.ProviderCapabilities, bool) {
	c.mu.RLock()
	e, ok := c.entries[cacheKey(profileID, modelID)]
	c.mu.RUnlock()
	if !ok {
		return llm.ProviderCapabilities{}, false
	}
	if e.version != llm.CapabilitySchemaVersion {
		return llm.ProviderCapabilities{}, false
	}
	if time.Since(e.fetchedAt) > cacheTTL {
		// Stale — caller should trigger background refresh but can use this.
		return e.caps, false
	}
	return e.caps, true
}

// Put stores a capabilities record.
func (c *MemoryCache) Put(_ context.Context, profileID, modelID string, caps llm.ProviderCapabilities) error {
	c.mu.Lock()
	c.entries[cacheKey(profileID, modelID)] = cacheEntry{
		caps:      caps,
		fetchedAt: time.Now(),
		version:   llm.CapabilitySchemaVersion,
	}
	c.mu.Unlock()
	return nil
}

// Invalidate removes all entries for profileID.
func (c *MemoryCache) Invalidate(_ context.Context, profileID string) error {
	prefix := profileID + "\x00"
	c.mu.Lock()
	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
	return nil
}

// InvalidateSchemaVersion removes stale-schema entries.
func (c *MemoryCache) InvalidateSchemaVersion(_ context.Context) error {
	c.mu.Lock()
	for k, e := range c.entries {
		if e.version != llm.CapabilitySchemaVersion {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
	return nil
}

// Snapshot returns a JSON-serialisable copy of all cache entries (for
// testing and diagnostics).
func (c *MemoryCache) Snapshot() map[string]json.RawMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]json.RawMessage, len(c.entries))
	for k, e := range c.entries {
		b, _ := json.Marshal(e.caps)
		out[k] = b
	}
	return out
}

// NullCache is returned when HARNESS_LLM_CAPABILITY_CACHE=off. Every
// Get is a miss; Put and Invalidate are no-ops.
type NullCache struct{}

func (NullCache) Get(_ context.Context, _, _ string) (llm.ProviderCapabilities, bool) {
	return llm.ProviderCapabilities{}, false
}
func (NullCache) Put(_ context.Context, _, _ string, _ llm.ProviderCapabilities) error {
	return nil
}
func (NullCache) Invalidate(_ context.Context, _ string) error    { return nil }
func (NullCache) InvalidateSchemaVersion(_ context.Context) error { return nil }

// DefaultCache returns the cache selected by HARNESS_LLM_CAPABILITY_CACHE.
// "off" → NullCache; "sqlite" (or unset, when db is available) →
// SQLiteCache over db; "memory" → MemoryCache always; unset with no db
// → MemoryCache.
//
// CORRECTED (model-settings-reach-the-model-01PMZ101 WP14 / WP12 finding,
// 2026-09-15): this function used to document "sqlite" as a value and
// silently fall through to MemoryCache for it (R-4, the exact
// "documented value a switch absorbs into default:" class G-6 exists to
// catch) — that half was fixed by adding the "sqlite" case below. A
// SEPARATE, subtler defect survived that fix: even after "sqlite" was a
// real case, nothing in production ever SET
// HARNESS_LLM_CAPABILITY_CACHE=sqlite (no launcher, no default Settings
// value, no packaging script — verified by repo-wide search), so the
// real backend was still never selected in any shipped binary — the
// registered kind existed, the case existed, and the env var that would
// have chosen it was simply never set. core/llm/
// wp_pi_persistence_integrity_z101_unit9_test.go's own "Correction
// (2026-08-25, Finding 6...)" section recorded this exact gap and
// explicitly left the decision to an owner ruling: "Whether to flip the
// default is an owner decision, not this test file's to make."
//
// That decision is made HERE: now that the SQLite backend is real (this
// package) and its table ships in every install (migration
// sessions/0329, registered since v0.63.0), the PERSISTENT backend is
// the correct default whenever a real db handle is available — an
// operator opting OUT (testing, or a deliberate no-persistence choice)
// still can via the explicit "memory" value. This flips the true
// default from "always MemoryCache" to "SQLiteCache when db != nil,
// MemoryCache otherwise" — no launcher/packaging change is required
// because the production call site (core/rpc/api.go's newLLMStack)
// already passes a real db unconditionally.
//
// db is the harness's unified storage handle (core/storage.DB); pass
// nil when no DB is available (e.g. the nil-core test chassis) — DB
// construction is the caller's job (see core/rpc's newLLMStack), this
// function only selects among cache STRATEGIES. A nil db always
// degrades to MemoryCache rather than panicking on first use; the
// explicit "sqlite" value additionally logs when that degrade happens,
// since an explicit request for persistence silently not getting it is
// more surprising than the unset default quietly doing the same thing.
func DefaultCache(db storage.DB) CapabilityCache {
	switch os.Getenv(EnvCapabilityCache) {
	case "off":
		return NullCache{}
	case "memory":
		return NewMemoryCache()
	case "sqlite":
		if db != nil {
			return NewSQLiteCache(db)
		}
		logging.L().Warn("llm.capability_cache.sqlite_unavailable",
			"reason", "HARNESS_LLM_CAPABILITY_CACHE=sqlite but no storage.DB handle was supplied; falling back to MemoryCache")
		return NewMemoryCache()
	default:
		// Unset (or an unrecognized value): prefer the persistent
		// backend whenever a real db handle is available (see the
		// correction above); degrade to MemoryCache when it is not.
		if db != nil {
			return NewSQLiteCache(db)
		}
		return NewMemoryCache()
	}
}
