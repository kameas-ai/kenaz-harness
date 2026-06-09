package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migration0331IDCustomEndpointCapabilities identifies migration 0331 — the
// custom_endpoint_capabilities table added by the
// custom-openai-compatible-endpoint-01KQ8VN0 mission.
//
// Numbering: 0331, the next free version after 0328 (media_artifact_meta).
// Versions 0329–0330 are reserved for the provider-implementation-uniformity
// foundation (parallel agent). This migration is declared at 0331 per the
// tasking spec.
const migration0331IDCustomEndpointCapabilities = "sessions/0331-custom-endpoint-capabilities"

const sqlCustomEndpointCapabilities0331 = `
	CREATE TABLE IF NOT EXISTS custom_endpoint_capabilities (
		endpoint    TEXT PRIMARY KEY,
		probed_at   INTEGER NOT NULL,
		matrix_json TEXT NOT NULL DEFAULT '{}'
	);

	CREATE INDEX IF NOT EXISTS idx_custom_endpoint_probed_at
		ON custom_endpoint_capabilities(probed_at);
`

// migration0331 returns the custom_endpoint_capabilities migration.
func migration0331() migrations.Migration {
	return migrations.Migration{
		ID:            migration0331IDCustomEndpointCapabilities,
		Version:       331,
		OwningMission: OwningMission,
		UpSource:      sqlCustomEndpointCapabilities0331,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlCustomEndpointCapabilities0331) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_custom_endpoint_probed_at",
				"DROP TABLE IF EXISTS custom_endpoint_capabilities",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
