package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDTelemetry identifies migration 0305 — the local-first
// landing for the OpenTelemetry instrumentation mission. Adds three
// flat tables that back the local SpanExporter / MetricExporter /
// LogExporter (telemetry-otel WP01). The OTLP composite layer in WP02
// fans out to these tables in addition to any user-configured fleet
// endpoint; the retention sweep in WP03 prunes rows older than
// Settings.Telemetry.RetentionDays.
//
// The schema is deliberately denormalized: each row is one fully
// resolved data point so the inspector view (WP06) can SELECT without
// joins. attributes_json + events_json carry the variable-shape OTel
// attribute bags. Indexes match the inspector's "last N by time" and
// "all rows for trace X" query shapes.
const migrationIDTelemetry = "sessions/0305-telemetry"

const sqlTelemetrySchema = `
        CREATE TABLE IF NOT EXISTS telemetry_spans (
            trace_id        TEXT NOT NULL,
            span_id         TEXT PRIMARY KEY,
            parent_span_id  TEXT,
            name            TEXT NOT NULL,
            kind            TEXT NOT NULL,
            start_ns        INTEGER NOT NULL,
            end_ns          INTEGER NOT NULL,
            status_code     INTEGER NOT NULL,
            status_message  TEXT NOT NULL DEFAULT '',
            attributes_json TEXT NOT NULL DEFAULT '{}',
            events_json     TEXT NOT NULL DEFAULT '[]'
        );

        CREATE INDEX IF NOT EXISTS idx_telemetry_spans_trace
            ON telemetry_spans (trace_id);

        CREATE INDEX IF NOT EXISTS idx_telemetry_spans_start
            ON telemetry_spans (start_ns);

        CREATE TABLE IF NOT EXISTS telemetry_metrics (
            name            TEXT NOT NULL,
            kind            TEXT NOT NULL,
            attributes_json TEXT NOT NULL DEFAULT '{}',
            value           REAL NOT NULL,
            ts_ns           INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_telemetry_metrics_name
            ON telemetry_metrics (name, ts_ns);

        CREATE TABLE IF NOT EXISTS telemetry_logs (
            severity        INTEGER NOT NULL,
            body            TEXT NOT NULL,
            attributes_json TEXT NOT NULL DEFAULT '{}',
            trace_id        TEXT,
            span_id         TEXT,
            ts_ns           INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_telemetry_logs_ts
            ON telemetry_logs (ts_ns);

        CREATE INDEX IF NOT EXISTS idx_telemetry_logs_trace
            ON telemetry_logs (trace_id) WHERE trace_id IS NOT NULL;
    `

// migration0305 returns the migration that lands the telemetry tables.
// Down is a best-effort no-op for parity with the rest of the sessions
// block: rolling back schema this granular requires restoring from a
// backup taken before 0305 applied.
func migration0305() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDTelemetry,
		Version:       305,
		OwningMission: OwningMission,
		UpSource:      sqlTelemetrySchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlTelemetrySchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			return nil
		},
	}
}
