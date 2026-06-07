package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDKind lands the kind column the harness-self-mcp-onboarding
// mission (harness-self-mcp-onboarding-01KQ8TDU WP03) needs to differentiate
// onboarding sessions from normal user sessions:
//
//   - sessions.kind — TEXT NOT NULL DEFAULT 'chat'.
//     Known values: "chat" (the default — normal user-driven session),
//     "onboarding" (a session spawned by the onboarding flow that has
//     access to harness-self write tools), "system" (reserved for future
//     internal sessions). The chat-runner stuffs this value into the Cedar
//     `tool_call` action context so the harness-self write-tool default
//     policies can gate by session kind.
//
// Numbering: 0318, the next free version after 0317
// (long-turn-resilience-01KR3PRS WP03 — see migrations_resume.go).
//
// Idempotent: the Up function checks pragma_table_info before issuing the
// ALTER TABLE.
//
// Down: leaves the column in place (column-add convention from 0302/0310/0311).
const migrationIDKind = "sessions/0318-kind"

// SessionKindChat is the default Record.Kind for ordinary user sessions.
const SessionKindChat = "chat"

// SessionKindOnboarding marks a session created by the onboarding flow.
// Cedar policies for harness-self write tools default to permitting them
// only in sessions with this kind.
const SessionKindOnboarding = "onboarding"

// SessionKindSystem is reserved for future internal-only sessions.
const SessionKindSystem = "system"

// sqlKindColumn is the canonical DDL body for migration 0318.
const sqlKindColumn = `ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat'`

// migration0318 returns the migration that adds the kind column to the
// sessions table.
func migration0318() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDKind,
		Version:       318,
		OwningMission: OwningMission,
		UpSource:      sqlKindColumn,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			row := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='kind'")
			var n int
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			_, err := tx.Exec(ctx, sqlKindColumn)
			return err
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			return nil
		},
	}
}
