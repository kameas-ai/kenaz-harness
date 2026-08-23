package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// SQLiteCache is the durable CapabilityCache backend over the
// provider_capabilities table (migration sessions/0329-provider-
// capabilities, core/session/migrations_provider_capabilities.go).
//
// model-settings-reach-the-model-01PMZ101 WP14, re-derivation finding
// R-4: the table and its migration shipped in v0.63.0 (register A-5's
// correction) but nothing ever wrote to it — DefaultCache's "sqlite"
// case fell through to MemoryCache. This is the first real
// implementation; the table itself is untouched (D-5: no new
// migration).
//
// Schema (unchanged from migration 0329):
//
//	CREATE TABLE provider_capabilities (
//	    profile_id                TEXT NOT NULL,
//	    model_id                  TEXT NOT NULL,
//	    fetched_at                INTEGER NOT NULL,
//	    flags_json                TEXT NOT NULL,
//	    capability_schema_version INTEGER NOT NULL,
//	    PRIMARY KEY (profile_id, model_id)
//	);
type SQLiteCache struct {
	db storage.DB
}

// NewSQLiteCache returns a CapabilityCache backed by db's
// provider_capabilities table. db must already have migration
// sessions/0329-provider-capabilities applied — true for every
// storage.DB the harness opens, since that migration is registered
// unconditionally at boot (core/session/migrations.go).
func NewSQLiteCache(db storage.DB) *SQLiteCache {
	return &SQLiteCache{db: db}
}

// Get retrieves the cached record for (profileID, modelID). Returns
// (zero, false) on a cache miss, a schema-version mismatch (mirrors
// MemoryCache.Get — a stale-schema row is treated as absent so callers
// fall back to the static catalog baseline rather than trusting a
// record shaped by an older CapabilitySchemaVersion), or when the
// record has exceeded cacheTTL (same stale-while-revalidate contract
// as MemoryCache: the caller still gets the stale value back so it can
// use it while a background refresh runs, but ok=false signals "this
// is due for a refresh").
func (c *SQLiteCache) Get(ctx context.Context, profileID, modelID string) (llm.ProviderCapabilities, bool) {
	row := c.db.Reader().QueryRow(ctx,
		`SELECT flags_json, fetched_at, capability_schema_version
		   FROM provider_capabilities
		  WHERE profile_id = ? AND model_id = ?`,
		profileID, modelID,
	)
	var flagsJSON string
	var fetchedAt int64
	var version int
	if err := row.Scan(&flagsJSON, &fetchedAt, &version); err != nil {
		// storage.Row wraps sql.ErrNoRows and other backends may not;
		// either way, "no row" and "read failure" both mean "no usable
		// cache entry" from the caller's perspective — the static
		// catalog baseline is always a safe fallback (Gate.Check runs
		// regardless of cache state).
		return llm.ProviderCapabilities{}, false
	}
	if version != llm.CapabilitySchemaVersion {
		return llm.ProviderCapabilities{}, false
	}
	var caps llm.ProviderCapabilities
	if err := json.Unmarshal([]byte(flagsJSON), &caps); err != nil {
		return llm.ProviderCapabilities{}, false
	}
	if time.Since(time.Unix(fetchedAt, 0)) > cacheTTL {
		return caps, false
	}
	return caps, true
}

// Put upserts the capabilities record for (profileID, modelID).
func (c *SQLiteCache) Put(ctx context.Context, profileID, modelID string, caps llm.ProviderCapabilities) error {
	payload, err := json.Marshal(caps)
	if err != nil {
		return fmt.Errorf("capabilities: sqlite cache: marshal: %w", err)
	}
	return c.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO provider_capabilities (profile_id, model_id, fetched_at, flags_json, capability_schema_version)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(profile_id, model_id) DO UPDATE SET
			   fetched_at                = excluded.fetched_at,
			   flags_json                = excluded.flags_json,
			   capability_schema_version = excluded.capability_schema_version`,
			profileID, modelID, time.Now().Unix(), string(payload), llm.CapabilitySchemaVersion,
		)
		return err
	})
}

// Invalidate removes every cached entry for profileID. Called on
// profile edit/removal — see registry.Registry.Evict.
func (c *SQLiteCache) Invalidate(ctx context.Context, profileID string) error {
	return c.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, `DELETE FROM provider_capabilities WHERE profile_id = ?`, profileID)
		return err
	})
}

// InvalidateSchemaVersion removes every entry whose
// capability_schema_version differs from the current
// llm.CapabilitySchemaVersion. Intended for a startup sweep the same
// way MemoryCache's in-process equivalent would be, though nothing
// calls either today — the per-Get version check above already keeps a
// stale-schema row from being trusted, so this method's absence of a
// production caller is not the same defect class as the rest of this
// mission's findings (a stale row cannot silently pass as valid; it
// can only accumulate as harmless dead space until this method, or a
// manual DELETE, is wired to a boot hook).
func (c *SQLiteCache) InvalidateSchemaVersion(ctx context.Context) error {
	return c.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM provider_capabilities WHERE capability_schema_version != ?`,
			llm.CapabilitySchemaVersion,
		)
		return err
	})
}

// Compile-time witness.
var _ CapabilityCache = (*SQLiteCache)(nil)
