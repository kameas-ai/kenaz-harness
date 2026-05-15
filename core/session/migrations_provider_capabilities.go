package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDProviderCapabilities identifies migration 0329 — the schema
// addition for the provider capability cache (provider-implementation-
// uniformity-01KQ8V4F WP05). Creates the provider_capabilities table with
// a 7-day TTL refresh cycle.
//
// Numbering: 0329, the next free version after 0328
// (media-artifact-meta — see migrations_media_artifact_meta.go).
const migrationIDProviderCapabilities = "sessions/0329-provider-capabilities"

const sqlProviderCapabilitiesSchema = `
        CREATE TABLE IF NOT EXISTS provider_capabilities (
            profile_id                TEXT NOT NULL,
            model_id                  TEXT NOT NULL,
            fetched_at                INTEGER NOT NULL,
            flags_json                TEXT NOT NULL,
            capability_schema_version INTEGER NOT NULL,
            PRIMARY KEY (profile_id, model_id)
        );
        CREATE INDEX IF NOT EXISTS idx_provider_capabilities_fetched_at
            ON provider_capabilities (fetched_at);
    `

// migration0329 returns the provider_capabilities cache migration.
// Down drops the table and index; safe because the cache is rebuilt
// on demand from the per-adapter curated data.
func migration0329() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDProviderCapabilities,
		Version:       329,
		OwningMission: OwningMission,
		UpSource:      sqlProviderCapabilitiesSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlProviderCapabilitiesSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(`
                DROP INDEX IF EXISTS idx_provider_capabilities_fetched_at;
                DROP TABLE IF EXISTS provider_capabilities;
            `) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
