package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDLastUsage is the identifier for migration 0322.
//
// This migration adds a single nullable TEXT column to the sessions table:
//
//   - last_usage_json TEXT — JSON-encoded SessionLastUsage snapshot written
//     by the chat runner after every completed LLM turn. NULL until the
//     first turn completes. The frontend reads this snapshot (via the
//     session.usage.updated broker event — WP03) to update the context-
//     window indicator without a round-trip to GetUsage.
//
// The payload schema matches the LastUsage struct defined below, stored as
// a flat JSON object:
//
//	{"promptTokens":N,"completionTokens":N,"totalTokens":N,"costUsd":F,"costSource":"derived"}
//
// Numbering: 0322 — next free version in the sessions block (300-399)
// after 0321 (workflow_schedules, workflows-agentic-01KW2D3X WP02).
//
// Mission: backend-context-window-length-01KQ8TD3 WP02.
const migrationIDLastUsage = "sessions/0322-last-usage-json"

const sqlLastUsageSchema = `
        ALTER TABLE sessions ADD COLUMN last_usage_json TEXT;
    `

// migration0322 returns the last-usage-json migration. Down is a no-op:
// SQLite ALTER TABLE DROP COLUMN requires ≥3.35.0 and the column has a
// safe NULL default so leaving it behind is harmless.
func migration0322() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDLastUsage,
		Version:       322,
		OwningMission: OwningMission,
		UpSource:      sqlLastUsageSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, "ALTER TABLE sessions ADD COLUMN last_usage_json TEXT")
			return err
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// SQLite ALTER TABLE DROP COLUMN not supported on older macOS.
			// Accept no-op; extra NULL columns cause no regressions.
			return nil
		},
	}
}
