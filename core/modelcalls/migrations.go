// Package modelcalls is the durable local store of LLM provider calls
// (llm-call-telemetry-01LLMTEL01 WP02, owner ruling 2026-10-09: "the
// harness collects and stores locally; users opt in to send to Fleet").
//
// One row per gen_ai.chat call — every attempt, retry and fallback hop —
// holding exactly the allowlisted, bounded attributes the span carries
// (core/llm/genaitrace) plus the provider stats pull's kenaz.provider.*
// fields. Never content: the table has no column for prompts, completions,
// tool arguments or results, and check-genai-span-no-content.sh fails the
// build if a column appears that genaitrace.RecordColumns /
// RecordStructuralColumns do not declare.
//
// Pieces:
//   - Store (store.go): insert, stats update, per-session list, prune.
//   - Recorder (recorder.go): the genaitrace.Recorder — non-blocking,
//     bounded queue, counted drops, batched writes.
//   - Pruner (pruner.go): retention, read from a settings closure on every
//     run (default 30 days).
//   - StatsPuller (stats.go): the OpenRouter generation-stats pull — async,
//     token-bucket rate-limited, retried with backoff, then dropped.
//
// Nothing in this package sends a record anywhere: export to Fleet is the
// span lane's opt-in (core/fleet), not this store.
package modelcalls

import (
	"context"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// MigrationOwner is the canonical owning-mission name for the
// model-calls block (versions 1800-1899,
// core/storage/migrations/blocks.go). 1700-1799 is ml-producer's
// (ml-producer-01MLPRD01, ml-producer/1700-ml-outbox-and-tasks), so this
// is the first range above it.
const MigrationOwner = "model-calls"

// migrationIDModelCallsInit is the stable id of the model_calls table.
const migrationIDModelCallsInit = "model-calls/1800-model-calls"

// sqlModelCallsInit creates model_calls. CREATE ... IF NOT EXISTS only:
// purely additive, touches no other table, safe on a populated database.
//
// Column classes (check-genai-span-no-content.sh enforces this split):
//   - structural (genaitrace.RecordStructuralColumns): id, span/trace ids,
//     timestamps, duration, status ('ok' | 'error'), stats_state
//     (” | 'fetched' | 'gave_up') and stats_fetched_at_ms;
//   - attribute columns (genaitrace.RecordColumns): one per allowlisted
//     key, typed — counts INTEGER, money/temperature REAL, ids/models/
//     tokens/enums TEXT. finish_reasons holds the allowlisted token list
//     joined with ',' (a token cannot contain one).
//
// session_hash is the per-install keyed hash, never the raw session id.
const sqlModelCallsInit = `
	CREATE TABLE IF NOT EXISTS model_calls (
	  id                          INTEGER PRIMARY KEY AUTOINCREMENT,
	  span_id                     TEXT NOT NULL,
	  trace_id                    TEXT NOT NULL DEFAULT '',
	  started_at_ms               INTEGER NOT NULL,
	  ended_at_ms                 INTEGER NOT NULL,
	  duration_ms                 INTEGER NOT NULL DEFAULT 0,
	  status                      TEXT NOT NULL,
	  session_hash                TEXT NOT NULL DEFAULT '',
	  provider                    TEXT NOT NULL DEFAULT '',
	  request_model               TEXT NOT NULL DEFAULT '',
	  response_model              TEXT NOT NULL DEFAULT '',
	  response_id                 TEXT NOT NULL DEFAULT '',
	  finish_reasons              TEXT NOT NULL DEFAULT '',
	  error_type                  TEXT NOT NULL DEFAULT '',
	  attempt                     INTEGER NOT NULL DEFAULT 0,
	  fallback                    INTEGER NOT NULL DEFAULT 0,
	  stream                      INTEGER NOT NULL DEFAULT 0,
	  max_tokens                  INTEGER NOT NULL DEFAULT 0,
	  temperature                 REAL,
	  input_tokens                INTEGER NOT NULL DEFAULT 0,
	  output_tokens               INTEGER NOT NULL DEFAULT 0,
	  cache_read_tokens           INTEGER NOT NULL DEFAULT 0,
	  cache_write_tokens          INTEGER NOT NULL DEFAULT 0,
	  reasoning_tokens            INTEGER NOT NULL DEFAULT 0,
	  tool_count                  INTEGER NOT NULL DEFAULT 0,
	  tool_schema_tokens_est      INTEGER NOT NULL DEFAULT 0,
	  ttft_ms                     INTEGER NOT NULL DEFAULT 0,
	  cost_usd                    REAL,
	  request_id                  TEXT NOT NULL DEFAULT '',
	  system_fingerprint          TEXT NOT NULL DEFAULT '',
	  upstream                    TEXT NOT NULL DEFAULT '',
	  tool_use_prompt_tokens      INTEGER NOT NULL DEFAULT 0,
	  stats_state                 TEXT NOT NULL DEFAULT '',
	  stats_fetched_at_ms         INTEGER NOT NULL DEFAULT 0,
	  provider_cost_usd           REAL,
	  provider_generation_time_ms INTEGER,
	  provider_latency_ms         INTEGER,
	  native_cached_tokens        INTEGER,
	  native_finish_reason        TEXT NOT NULL DEFAULT '',
	  native_input_tokens         INTEGER,
	  native_output_tokens        INTEGER,
	  native_reasoning_tokens     INTEGER
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_model_calls_span_id
	  ON model_calls(span_id);

	CREATE INDEX IF NOT EXISTS idx_model_calls_session_started
	  ON model_calls(session_hash, started_at_ms DESC);

	CREATE INDEX IF NOT EXISTS idx_model_calls_started
	  ON model_calls(started_at_ms);

	CREATE INDEX IF NOT EXISTS idx_model_calls_response_id
	  ON model_calls(response_id);
`

// MigrationSQL returns the DDL of every model-calls migration, in order.
// check-genai-span-no-content.sh's structural half parses the
// sqlModelCallsInit literal directly; this accessor is for tests.
func MigrationSQL() []string { return []string{sqlModelCallsInit} }

// Migrations returns the migration set owned by the model-calls mission.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDModelCallsInit,
			Version:       1800,
			OwningMission: MigrationOwner,
			UpSource:      sqlModelCallsInit,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitSQL(sqlModelCallsInit) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down drops the table: the store is an observational record
			// of provider calls; nothing else in the product depends on
			// it for correctness (same posture as advice_labels).
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range []string{
					"DROP INDEX IF EXISTS idx_model_calls_response_id",
					"DROP INDEX IF EXISTS idx_model_calls_started",
					"DROP INDEX IF EXISTS idx_model_calls_session_started",
					"DROP INDEX IF EXISTS idx_model_calls_span_id",
					"DROP TABLE IF EXISTS model_calls",
				} {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
}

// RegisterMigrations registers Migrations() with reg (called from
// core/storage/sqlite.Open, like every other mission's block).
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitSQL splits the DDL above on ';' (it contains no quoted semicolons).
func splitSQL(src string) []string {
	parts := strings.Split(src, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
