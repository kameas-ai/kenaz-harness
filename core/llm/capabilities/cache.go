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
	// Values: "sqlite" | "memory" | "off". Default is "memory".
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
// It is the default when HARNESS_LLM_CAPABILITY_CACHE is unset or "memory".
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
// "off" → NullCache; "memory" or unset → MemoryCache; "sqlite" →
// SQLiteCache over db (model-settings-reach-the-model-01PMZ101 WP14 /
// re-derivation finding R-4: the previous version of this function
// documented "sqlite" as a value and silently fell through to
// MemoryCache for it — the exact "documented value a switch absorbs
// into default:" class G-6 exists to catch).
//
// db is the harness's unified storage handle (core/storage.DB); pass
// nil when no DB is available (e.g. the nil-core test chassis) — DB
// construction is the caller's job (see core/rpc's newLLMStack), this
// function only selects among cache STRATEGIES. "sqlite" with a nil db
// degrades to MemoryCache rather than panicking on first use, logged so
// the gap is visible instead of silently losing persistence.
func DefaultCache(db storage.DB) CapabilityCache {
	switch os.Getenv(EnvCapabilityCache) {
	case "off":
		return NullCache{}
	case "sqlite":
		if db != nil {
			return NewSQLiteCache(db)
		}
		logging.L().Warn("llm.capability_cache.sqlite_unavailable",
			"reason", "HARNESS_LLM_CAPABILITY_CACHE=sqlite but no storage.DB handle was supplied; falling back to MemoryCache")
		return NewMemoryCache()
	default:
		// "memory" and unset fall back to MemoryCache, safe for
		// in-process use with no persistence across restarts.
		return NewMemoryCache()
	}
}
