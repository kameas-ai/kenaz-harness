# Implementation Plan: OpenTelemetry instrumentation

**Branch**: `telemetry-otel-01KQ5RW1` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/telemetry-otel-01KQ5RW1/spec.md`

## Summary

Instrument the harness with OpenTelemetry SDK (traces + metrics + logs). Local SQLite store for all signals as the always-on backend (default 30-day retention). Optional OTLP HTTP export to a user-configured "fleet" endpoint. PII guardrails via attribute allowlists per span name; a "Verbose attributes" toggle (off by default) widens the set when the user explicitly accepts the trade-off. Settings → Monitoring tab surfaces retention slider, fleet endpoint editor, signal toggles, and a basic event inspector.

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: `go.opentelemetry.io/otel` v1.30+ (SDK + OTLP/HTTP exporters for traces, metrics, logs). Stdlib `database/sql` for the local SQLite exporters.
- **Storage**: 3 new tables in unified `data.db` (migration 0304). Persistent `<DataDir>/telemetry/instance.id` ULID file.
- **Testing**: `-race -count=1 -short`; vitest. OTLP export tests use httptest receiver. Inspector tests use stub spans.
- **Performance**: NFR-007 (<5 ms p99 chat-turn overhead); NFR-008 (<100 MB local DB at default retention + typical use).
- **Constraints**: NFR-003 never-block (background goroutines + bounded buffer + drop-on-full); NFR-005 local-first (no traffic when endpoint unset); NFR-006 PII (verbose=false → no chat content in any persisted row).

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/telemetry/` depends on stdlib + `core/storage` + `core/logging` (existing). The reverse direction — instrumented packages (`core/llm`, `core/toolloop`, `core/hooks`, `core/mcp`) — depend on `core/telemetry` for tracer/meter handles. Sane DAG.
- C-001 (no third-party SDK in `core/`): `go.opentelemetry.io/otel` is the OpenTelemetry standard; not a vendor SDK. Pass.
- Privacy CI: telemetry attribute allowlist per span name is the privacy gate; verbose mode requires explicit toggle. PII test plants sentinel string + scans tables — fail-loud regression net.

## Project Structure

```
core/telemetry/
├── telemetry.go                  # Init + Shutdown + Resource builder + service.instance.id
├── tracer.go                     # Tracer wrapper + span helpers (Start, End, RecordError, AddAttr)
├── meter.go                      # Meter wrapper (Counter, Histogram, UpDownCounter, Gauge)
├── logger.go                     # OTel Logger + slog bridge handler
├── exporter_local.go             # SpanExporter / MetricExporter / LogExporter → SQLite
├── exporter_local_test.go
├── exporter_otlp.go              # OTLP HTTP wrappers (one per signal)
├── exporter_otlp_test.go
├── exporter_composite.go         # Fan-out: local always; OTLP when configured
├── exporter_composite_test.go
├── sweep.go                      # Retention sweep, hourly tick
├── sweep_test.go
├── instance.go                   # service.instance.id persistence
├── instance_test.go
├── attrs.go                      # AllowList per span name; verbose-mode expansion
└── attrs_test.go

core/session/migrations.go        # MODIFIED: register migration 0304
core/session/migrations_telemetry.go  # NEW: 3 tables + indexes

core/llm/                         # MODIFIED: tracer.Start(ctx, "llm.stream", ...) wrappers; metrics
core/toolloop/                    # MODIFIED: tool.dispatch + tool.execute spans; metrics
core/hooks/                       # MODIFIED: hook.run spans
core/mcp/stdio/                   # MODIFIED: mcp.spawn/initialize/restart/close spans
core/rpc/views/llm/impl.go        # MODIFIED: chat.turn span as parent of llm.stream; metric

core/rpc/views/telemetry/
├── api.go                        # TelemetryAPI surface
├── impl.go                       # Stats, Recent*, ExportToFile, RunSweep, TestConnection
└── impl_test.go

core/rpc/views/settings/
├── api.go                        # MODIFIED: Settings.Telemetry sub-struct
└── impl.go

core/rpc/api.go                   # MODIFIED: telemetry init + accessor + bindings
core/rpc/bindings.go              # MODIFIED: Telemetry_*

frontend/src/views/settings/
├── MonitoringTab.vue             # NEW: Settings → Monitoring sub-tab
├── TelemetryInspector.vue        # NEW: last-100 events table
├── FleetEndpointEditor.vue       # NEW: URL + headers + insecure + test connection
└── __tests__/

frontend/src/lib/types.ts         # MODIFIED: TelemetrySettings, TelemetryStats, TelemetryEvent
frontend/src/lib/harnessClient.ts # MODIFIED: telemetry namespace

docs/telemetry.md                 # NEW
```

## Phase 0 — Research summary

- **OTel SDK**: `go.opentelemetry.io/otel` v1.30+ stable. Pin in `go.mod`. SDK provides `TracerProvider`, `MeterProvider`, `LoggerProvider`. Each accepts a list of exporters; we plug ours in.
- **OTLP/HTTP exporters**: `otlptracehttp`, `otlpmetrichttp`, `otlploghttp`. Standard endpoints: `/v1/traces`, `/v1/metrics`, `/v1/logs` per the OTLP spec.
- **slog bridge**: implement `slog.Handler` that fans out to `core/logging`'s file handler AND emits OTel log records. SDK has `otel/log.Logger` for emission.
- **service.instance.id**: ULID per harness install (NOT per process). Persist to `<DataDir>/telemetry/instance.id` on first boot. Same lifetime as the data dir.
- **Resource discovery**: `service.name=kaneaz-harness`, `service.version=<build>`, `host.os=runtime.GOOS`, `process.pid=os.Getpid()`. Add via `resource.New`.
- **Retention semantics**: `delete from telemetry_X where ts_ns < ?` chunked at 1000 rows per write transaction to avoid lock contention.
- **PII guardrail**: per-span-name allowlist defined in `attrs.go`. Verbose-mode toggle expands the allowlist set. The instrumentation sites use a wrapper `RecordAttr(span, name, key, value)` that drops keys not in the active allowlist for that span name + verbose flag.

## Phase 1 — SDK init + Resource + instance.id

**Targets**: `core/telemetry/{telemetry.go, instance.go, instance_test.go}`.

- `Init(ctx, cfg Config) (*Telemetry, error)` constructs TracerProvider, MeterProvider, LoggerProvider with the composite exporter. Returns the handle for shutdown.
- `Shutdown(ctx) error` flushes all providers + closes exporters.
- `Resource()` builds the OTel resource with `service.name`, `service.version`, `service.instance.id`, `host.os`.
- `EnsureInstanceID(dataDir) (string, error)`:
  - Reads `<dataDir>/telemetry/instance.id`. If exists, return content (validate ULID shape).
  - Else: generate fresh ULID, write atomically (tmp + fsync + rename).
- Tests: instance ID round-trip; missing dir → created; corrupt content → regenerate (don't crash).

**Dependencies**: none.

## Phase 2 — Local SQLite exporters

**Targets**: `core/telemetry/exporter_local.go` + tests, `core/session/migrations_telemetry.go`, `core/session/migrations.go`.

- Migration 0304 creates `telemetry_spans`, `telemetry_metrics`, `telemetry_logs` per spec FR-004.
- `LocalSpanExporter` implements `sdktrace.SpanExporter`:
  - Buffered channel (size 1024) of `sdktrace.ReadOnlySpan`.
  - Background goroutine batches up to 100 spans or 1 s tick → single transaction, multi-row INSERT.
  - On full buffer: drop oldest + increment `telemetry.dropped` counter (NFR-003).
  - Shutdown: drain remaining + close.
- `LocalMetricExporter` implements `sdkmetric.Exporter`:
  - Periodic reader pushes metric snapshots; we flatten to per-data-point rows.
- `LocalLogExporter` implements `log.Exporter`:
  - Buffered like spans.
- Tests: write-read round-trip; back-pressure drop; concurrent writers; race-detector clean; chunked sweep doesn't lock writes.

**Dependencies**: Phase 1.

## Phase 3 — OTLP exporter wrappers

**Targets**: `core/telemetry/exporter_otlp.go` + tests.

- `NewOTLPSpanExporter(endpoint, headers, insecure)` wraps `otlptracehttp`.
- Same for metrics + logs.
- Headers from settings; auth values resolved from keychain at construction.
- Tests with httptest receivers: assert correct OTLP envelope shape; auth header present; backoff on 5xx.

**Dependencies**: Phase 1.

## Phase 4 — Composite exporter

**Targets**: `core/telemetry/exporter_composite.go` + tests.

- `CompositeSpanExporter{local, otlp}` — fan-out. `local` always invoked; `otlp` only when non-nil.
- Same for metrics + logs.
- Errors: each exporter is independent; one failing doesn't stop the other. Local errors are loud (real bug); OTLP errors are silent + retried by the underlying SDK.
- **NFR-005 verification test**: instantiate composite with nil OTLP; spy any outbound TCP via a fake net dialer wrapper; assert zero connections.

**Dependencies**: Phases 2, 3.

## Phase 5 — Slog bridge + OTel Logger

**Targets**: `core/telemetry/logger.go` + `attrs.go`.

- Replace the existing `core/logging.L()` factory's underlying handler with a fan-out:
  - File handler (existing).
  - OTel handler (new).
- Fan-out via a thin `slog.Handler` impl that calls both.
- Telemetry logs include `trace_id` + `span_id` from the current span context (when present).

**Dependencies**: Phase 1.

## Phase 6 — Retention sweep

**Targets**: `core/telemetry/sweep.go` + tests.

- `Sweeper{db, retention, scheduler}` runs hourly via the existing scheduler. First tick fires 5 minutes after harness boot to give the user time to settle.
- `Run(ctx)` deletes from each of 3 tables in chunks of 1000 rows. Logs deletion counts via metric `telemetry.sweep.deleted{table=...}`.
- Manual trigger: `RunNow(ctx)` for the "Run sweep now" button.
- Tests: planted old rows + new rows; sweep removes only old; chunked delete doesn't lock; metric increments correctly.

**Dependencies**: Phase 2.

## Phase 7 — Instrumentation in the hot paths

**Targets**: `core/llm/`, `core/toolloop/`, `core/hooks/`, `core/mcp/stdio/`, `core/rpc/views/llm/impl.go`, `core/attrs.go`.

- `core/telemetry/attrs.go` defines per-span allowlist:
  ```go
  var allowList = map[string]struct {
      base    []string  // always included
      verbose []string  // included when Settings.Telemetry.VerboseAttributes
  }{
      "chat.turn": {
          base:    []string{"session_id", "project_id", "model_id", "provider_kind", "prompt_tokens", "completion_tokens", "status"},
          verbose: []string{"message_count", "system_prompt_hash", "last_user_message_preview"},
      },
      // ...one entry per span name from FR-010..FR-017
  }
  func RecordAttr(span trace.Span, spanName string, attrs map[string]any, verbose bool)
  ```
- Each instrumented site:
  ```go
  ctx, span := tracer.Start(ctx, "chat.turn")
  defer span.End()
  telemetry.RecordAttr(span, "chat.turn", map[string]any{
      "session_id": sessionID,
      "model_id": modelID,
      // ...
  }, settings.Telemetry.VerboseAttributes)
  ```
- Span sites per FR-010..FR-017.
- Metric sites: `chat.turn.duration` (histogram), `llm.tokens.*` (counters), `tool.invocations` (counter by name), `tool.duration` (histogram), `mcp.restarts` (counter), `tool.errors` (counter), `telemetry.dropped` (counter), `telemetry.sweep.deleted` (counter).

**Dependencies**: Phases 1-5.

## Phase 8 — Settings + RPC view

**Targets**: `core/rpc/views/settings/`, `core/rpc/views/telemetry/`, bindings.

- `Settings.Telemetry` sub-struct: `RetentionDays`, `OTLPEndpoint`, `OTLPHeadersLocator` (keychain ref), `OTLPInsecure`, `TracesEnabled`, `MetricsEnabled`, `LogsEnabled`, `VerboseAttributes`.
- `TelemetryAPI` impl per FR-023:
  - `Stats()` → row counts + last-export ts + last-sweep ts.
  - `RunSweep()` → triggers Sweeper.RunNow.
  - `TestConnection()` → emits a single `app.heartbeat` span; awaits OTLP response; returns ok/err.
  - `RecentSpans(n)`, `RecentMetrics(n)`, `RecentLogs(n)` → SELECT ... ORDER BY ts_ns DESC LIMIT n.
  - `ExportToFile(path)` → JSONL dump of last-N spans/metrics/logs.
- Bindings + accessor.

**Dependencies**: Phases 1-7.

## Phase 9 — Frontend Monitoring tab

**Targets**: `MonitoringTab.vue`, `TelemetryInspector.vue`, `FleetEndpointEditor.vue`, `lib/types.ts`, `lib/harnessClient.ts`.

- Sub-tab in `SettingsView.vue` (alongside General / Providers / Hooks / Bundles).
- Sections per FR-022 layout.
- Inspector renders the recent-N data with sortable columns; click-to-expand on spans.
- Fleet editor: URL + headers chip-list (value masked, replaceable); insecure toggle; test-connection button.
- Verbose-attributes toggle with the warning banner.

Tests for each component.

**Dependencies**: Phase 8.

## Phase 10 — Polish + docs

**Targets**: `docs/telemetry.md`, e2e tests.

- `docs/telemetry.md`: walkthrough (default local-first; configuring fleet endpoint; verbose mode warning; export-to-file; retention).
- E2E test `-tags=integration`: boot core; run a chat turn; assert spans landed in local SQLite + (with fake httptest receiver) OTLP envelope arrived.
- PII regression test (NFR-006): plant sentinel string in a chat turn with verbose=false; walk all telemetry tables; assert sentinel absent.
- Manual A1-A10 checklist.

**Dependencies**: all earlier.

## Work-package breakdown (proposed)

- **WP01 — SDK init + local exporters + migration 0304** (Phases 1, 2, 4 partial). Lands `core/telemetry/{telemetry,instance,exporter_local}.go` + migration. No instrumentation yet; exporters can be tested in isolation.
- **WP02 — OTLP exporter + composite + slog bridge** (Phases 3, 4, 5). Lands the export side end-to-end. After this WP, OTLP works against a stub receiver.
- **WP03 — Retention sweep + per-span attribute allowlist** (Phases 6, 7 partial — the `attrs.go` allowlist is here, but not yet instrumented in hot paths).
- **WP04 — Hot-path instrumentation** (Phase 7 main). Spans + metrics added to llm, toolloop, hooks, mcp, rpc/views/llm. Largest WP — touches lots of files.
- **WP05 — Settings + RPC view + bindings** (Phase 8). Backend surface for the Monitoring tab.
- **WP06 — Frontend Monitoring tab** (Phase 9).
- **WP07 — Polish + docs + PII regression test** (Phase 10).

DAG: WP01 → (WP02 ∥ WP03) → WP04 → WP05 → WP06 → WP07.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| OTel SDK version churn | 0 | Pin in `go.mod`; document upgrade procedure in `docs/telemetry.md`. SDK 1.x is stable; minor bumps shouldn't break us. |
| Telemetry blocks chat surface | 2 | Bounded buffer + drop-on-full + counter; never use blocking channels. Test asserts producer never blocks. |
| Local DB grows past 100 MB | 6 | Retention sweep + per-row size cap on attributes_json (4 KiB cap; truncate with `...[truncated]`). |
| OTLP traffic accidentally fires when endpoint unset | 4 | NFR-005 test: composite with nil OTLP exporter; assert zero outbound TCP. |
| PII leak via verbose-mode default-on | 7 | Default off; explicit toggle with banner; PII regression test prevents accidental change. |
| Slog bridge doubles log volume | 5 | File handler unchanged; OTel sink is additional. Telemetry retention bounds total. Document. |
| Span attribute names drift | 7 | Allowlist is the source of truth; instrumentation site test asserts every emitted span name has an allowlist entry. |
| service.instance.id rotation breaks fleet correlation | 1 | Persistent + immutable by default. Future "rotate" affordance gated behind a confirmation. |
| `data.db` schema changes break the in-flight retention sweep | 6 | Sweep reads tables it created; migration 0304 is idempotent; sweep tolerates "table missing" gracefully. |
| OTLP backoff blocks shutdown | 3 | Shutdown ctx with 5 s deadline; drop unsent events past deadline; log a warn. |

## Open questions

(Restated from spec.md §11.)

1. OTel SDK version pin — `v1.30+`; pin specific minor in `go.mod`.
2. Instance ID rotation — defer to v1.x.
3. Slog level mapping — Debug→5, Info→9, Warn→13, Error→17. Document.
4. First-run wizard for retention — no; settings editable from boot.
5. Per-signal retention — single global; per-signal a future enhancement.
6. **NEW** — Should `Telemetry_TestConnection` cost a real exported event? Default yes (`app.heartbeat`); this verifies the full chain end-to-end. Document so users know one event lands per click.
