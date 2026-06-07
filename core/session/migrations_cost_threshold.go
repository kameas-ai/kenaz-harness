package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDCostThresholdFired is the identifier for migration 0315.
//
// This migration adds the cost_threshold_fired idempotency table that
// the threshold notification scheduler
// (token-cost-telemetry-01KQ8TD7 WP06) uses to dedupe its 50/80/100/
// 150/200 % per-month firings. One row per (year_month, pct) — the
// scheduler INSERT-OR-IGNOREs on every Manager.Add call and only
// publishes the broker event when a fresh row is written.
//
// On a calendar-month rollover the table simply doesn't contain rows
// for the new YYYY-MM yet, so the next-month fires re-engage cleanly
// without any explicit reset.
//
// Numbering: 0315 — the version reserved by migrations.go for this
// table; sits between 0314 (session_messages cost columns) and 0316
// (autonomy columns).
const migrationIDCostThresholdFired = "sessions/0315-cost-threshold-fired"

const sqlCostThresholdFiredSchema = `
        CREATE TABLE IF NOT EXISTS cost_threshold_fired (
            year_month TEXT NOT NULL,
            pct        INTEGER NOT NULL,
            fired_at   INTEGER NOT NULL,
            PRIMARY KEY (year_month, pct)
        );
    `

// migration0315 returns the cost-threshold-fired idempotency-table
// migration. Down drops the table; the scheduler tolerates a missing
// table on read by treating "no row" as "not yet fired this month."
func migration0315() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDCostThresholdFired,
		Version:       315,
		OwningMission: OwningMission,
		UpSource:      sqlCostThresholdFiredSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, sqlCostThresholdFiredSchema)
			return err
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS cost_threshold_fired")
			return err
		},
	}
}
