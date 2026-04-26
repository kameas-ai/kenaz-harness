# Spec: OpenTelemetry instrumentation — local store + fleet export

**Mission ID**: `telemetry-otel-01KQ5RW1`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The harness currently emits structured slog lines to `~/.kenaz/harness.log` and ad-hoc audit events through the in-flight emitter pattern. Useful for local debugging, but:

- **No structured spans** — request → LLM stream → tool dispatch → tool execution chain isn't a navigable trace.
- **No metrics** — token usage, tool latencies, error rates aren't aggregable without parsing log lines.
- **No off-machine path** — slog files stay on the user's machine. When the user wants to ship telemetry to a fleet-management or observability backend (their own OTel collector, Honeycomb, Datadog, Grafana Cloud, …), there's no wire.
- **No retention policy** — log files grow forever.

This mission instruments the harness with **OpenTelemetry** (traces + metrics + logs), stores all signals **locally first** in SQLite with a configurable retention window (default 30 days), and **optionally** exports to a user-configured OTLP endpoint ("fleet"). When the user has no fleet endpoint configured, telemetry stays local — that's the local-first default.

## 2. Goals

- **Instrument the harness** with OpenTelemetry primitives (`go.opentelemetry.io/otel` SDK):
  - **Traces**: spans for chat turn, LLM stream, tool dispatch, tool execution, hook runs, MCP server lifecycle, recipe install, search query, bash invocation.
  - **Metrics**: counters/histograms for tokens consumed, tool invocations by name, latencies (p50/p95/p99), error rates, recipe install events, retention-sweep effectiveness.
  - **Logs**: bridge slog → OTel logs so existing `~/.kenaz/harness.log` lines are also queryable as structured logs.
- **Local store** for all signals: SQLite tables (`telemetry_spans`, `telemetry_metrics`, `telemetry_logs`) under the existing unified `data.db`. New migration 0304.
- **Configurable retention** — default 30 days, settable in Settings. A periodic sweep (default hourly) deletes rows older than the cutoff.
- **Optional OTLP export**: user configures an HTTP endpoint + optional auth header. Composite exporter writes to BOTH local SQLite and OTLP when configured. When not configured, only local. The user's privacy default is "no export."
- **Settings tab** "Monitoring" with: retention slider (1-365 days, default 30), fleet endpoint URL + auth header, signal toggles (traces / metrics / logs), inspector view (last N spans/metrics/logs), export-to-file affordance.
- **PII guardrails**: telemetry attributes carry only metadata by default — provider id, model, token count, latency, status. **NEVER message text, tool args, prompt content.** A separate "Verbose mode" opt-in toggle (off by default) widens the attribute set; a banner warns the user before enabling.

## 3. Non-goals

- **Distributed tracing across machines** — v1 traces are single-host.
- **Sampling tuned for high-volume production** — local-first means traffic is bounded by the user's chat usage. Always-sample for v1; revisit at v1.x.
- **Pluggable exporters** beyond OTLP HTTP — no Datadog SDK, no Honeycomb SDK, no Jaeger UDP. OTLP is the lingua franca; downstream backends accept it.
- **Custom telemetry visualization in-harness** — Settings → Monitoring shows a basic inspector (last 100 events table). Real dashboards live in the user's OTel collector / observability backend.
- **Automatic redaction of free-form attributes** — verbose-mode is off by default; when on, the user accepts PII risk. We don't try to pattern-match secrets out of attributes.
- **Real-time streaming export** — local SQLite is the buffer. OTLP export runs in a background goroutine batched every 5 s.
- **Browser-side telemetry** — frontend events flow through the existing rpc audit channel; this mission instruments core/.

## 4. User stories

- **US1** As a user new to the harness, I install + use it for a week. I open Settings → Monitoring and see "telemetry stored locally; 0 events exported (no fleet configured)". I can see token usage, tool counts, error rates from the inspector — without sending anything off-machine.
- **US2** As a user with my own OTel collector at `http://otel.local:4318`, I paste the URL into Settings → Monitoring → Fleet endpoint, click Save. Subsequent telemetry is sent to my collector AND kept locally for 30 days.
- **US3** As a privacy-conscious user, I see "Verbose attributes (off by default — includes message metadata, never content)" and leave it off. My telemetry still has provider_id, model, token_count, latency — useful — but no chat content.
- **US4** As a debugger, I hit a slow chat turn. I open the Monitoring inspector → filter by error → see the offending span with a 12s LLM stream + a tool call that timed out at 30s.
- **US5** As a user concerned about disk usage, I lower retention to 7 days. The next sweep deletes everything older than 7 days. The retention metric shows current row count.
- **US6** As a user wanting to share telemetry from a bug repro with the harness's authors, I click "Export to file" → get a JSONL file with the last N events → attach to a bug report.
- **US7** As a fleet operator, my OTel collector receives spans tagged with `service.name=kaneaz-harness, service.version=<build>` and a stable `service.instance.id` per install (random ULID stored at first boot, persisted in `<DataDir>/telemetry/instance.id`).

## 5. Functional requirements

### 5.1 SDK + initialization

- **FR-001** Use `go.opentelemetry.io/otel` SDK + `go.opentelemetry.io/otel/sdk/{trace,metric,log}`. Vendor via `go get`.
- **FR-002** Telemetry SDK initializes in `core.New` (alongside the storage init so spans can record the boot sequence). Provider construction:
  - `TracerProvider` with the composite exporter (local + optional OTLP).
  - `MeterProvider` with the same composite shape.
  - `LoggerProvider` with the same.
  - Resource: `service.name="kaneaz-harness"`, `service.version=<build version from AppInfo>`, `service.instance.id=<persistent ULID from <DataDir>/telemetry/instance.id>`, `host.os=runtime.GOOS`.
- **FR-003** Slog bridge: install an `slog.Handler` that fans out to BOTH the existing file logger AND the OTel logger. New log lines hit both.

### 5.2 Local store

- **FR-004** Migration 0304 creates three tables in `data.db`:
  ```sql
  CREATE TABLE telemetry_spans (
      trace_id        TEXT NOT NULL,
      span_id         TEXT PRIMARY KEY,
      parent_span_id  TEXT,
      name            TEXT NOT NULL,
      kind            TEXT NOT NULL,         -- "internal"|"server"|"client"|"producer"|"consumer"
      start_ns        INTEGER NOT NULL,
      end_ns          INTEGER NOT NULL,
      status_code     INTEGER NOT NULL,      -- 0=unset, 1=ok, 2=error
      status_message  TEXT NOT NULL DEFAULT '',
      attributes_json TEXT NOT NULL DEFAULT '{}',
      events_json     TEXT NOT NULL DEFAULT '[]'
  );
  CREATE INDEX idx_telemetry_spans_trace ON telemetry_spans (trace_id);
  CREATE INDEX idx_telemetry_spans_start ON telemetry_spans (start_ns);

  CREATE TABLE telemetry_metrics (
      name            TEXT NOT NULL,
      kind            TEXT NOT NULL,         -- "counter"|"updown"|"histogram"|"gauge"
      attributes_json TEXT NOT NULL DEFAULT '{}',
      value           REAL NOT NULL,
      ts_ns           INTEGER NOT NULL
  );
  CREATE INDEX idx_telemetry_metrics_name ON telemetry_metrics (name, ts_ns);

  CREATE TABLE telemetry_logs (
      severity        INTEGER NOT NULL,      -- per OTel SeverityNumber
      body            TEXT NOT NULL,
      attributes_json TEXT NOT NULL DEFAULT '{}',
      trace_id        TEXT,
      span_id         TEXT,
      ts_ns           INTEGER NOT NULL
  );
  CREATE INDEX idx_telemetry_logs_ts ON telemetry_logs (ts_ns);
  CREATE INDEX idx_telemetry_logs_trace ON telemetry_logs (trace_id) WHERE trace_id IS NOT NULL;
  ```
- **FR-005** The local exporter is an OTel `SpanExporter` / `MetricExporter` / `LogExporter` that batches inserts (default 100 events or 1 second flush). Failures degrade silently — telemetry never blocks the chat surface.
- **FR-006** Retention sweep: `core/telemetry/sweep.go` runs hourly via the existing scheduler infrastructure. Deletes rows where `ts_ns < now - retention_days * 24h`. Counter metric `telemetry.sweep.deleted` records per-run delete count.

### 5.3 OTLP export

- **FR-007** When `Settings.Telemetry.OTLPEndpoint` is non-empty, the composite exporter ALSO sends to that endpoint via OTLP HTTP. Config:
  - `OTLPEndpoint string` — e.g. `http://localhost:4318` (path `/v1/traces`, `/v1/metrics`, `/v1/logs` standard).
  - `OTLPHeaders map[string]string` — optional, e.g. `{"Authorization": "Bearer <token>"}`. Auth tokens stored in keychain via locator `telemetry/otlp_auth/<name>`; the settings struct only references the locator.
  - `OTLPInsecure bool` — allow http (no TLS); default false; required true when endpoint is `http://`.
- **FR-008** Export failures (network down, 5xx) are retried with exponential backoff (1s, 2s, 4s, 8s, capped at 60s); after 10 consecutive failures, the exporter pauses for the retention period to avoid hammering. Local writes continue regardless.
- **FR-009** Headers under config keys are surfaced in the Monitoring tab as masked rows (e.g. "Authorization: Bearer ***************"); user can edit + re-save (replaces the keychain entry).

### 5.4 Instrumented surfaces

Spans + metrics covering at minimum:

- **FR-010** **Chat turn**: `chat.turn` span, parent of the assistant-stream span. Attributes: session_id, project_id, model_id, provider_kind, prompt_tokens, completion_tokens, status. Metric: `chat.turn.duration` histogram.
- **FR-011** **LLM stream**: `llm.stream` span. Attributes: model_id, finish_reason, tool_call_count. Metric: `llm.stream.duration`, `llm.tokens.prompt`, `llm.tokens.completion`.
- **FR-012** **Tool dispatch + execution**: `tool.dispatch` span (parent), `tool.execute` (child, per call). Attributes: tool_name, server_id, policy (auto_allow / confirm_each / deny), result_status. Metric: `tool.invocations` counter by name, `tool.duration` histogram, `tool.errors` counter.
- **FR-013** **Hook runs**: `hook.run` span. Attributes: hook_name (memory.persist, memory.retrieve, ...), stage (pre/post). Metric: `hook.runs` counter, `hook.errors` counter.
- **FR-014** **MCP server lifecycle**: `mcp.spawn`, `mcp.initialize`, `mcp.restart`, `mcp.close` spans. Attributes: recipe_id, exit_code (close), restart_attempt (restart). Metric: `mcp.restarts` counter, `mcp.spawn.duration` histogram.
- **FR-015** **Search query** (when local-first-tools mission lands): `tool.web_search` span. Attributes: backend_used, result_count, status.
- **FR-016** **Bash execution** (when local-first-tools mission lands): `tool.bash` span. Attributes: command (BASENAME ONLY when verbose=false; full when verbose=true), exit_code, truncated, duration. Metric: `bash.invocations` counter.
- **FR-017** **Recipe install / uninstall**: `recipe.installed`, `recipe.uninstalled` events on a parent `chat.turn` span (or rooted under `app.action` when invoked from the Tools panel).

### 5.5 PII guardrails

- **FR-018** Default attribute allowlist per span name. e.g. `chat.turn` allows `{session_id, project_id, model_id, provider_kind, prompt_tokens, completion_tokens, status}` — **NOT** the message content.
- **FR-019** `Settings.Telemetry.VerboseAttributes bool` — default false. When true, expands the allowlist per span name. Documented per-span:
  - `chat.turn` verbose adds: `message_count`, `system_prompt_hash` (sha256 truncated), `last_user_message_preview` (first 60 chars).
  - `llm.stream` verbose adds: `tool_calls` (names only).
  - `tool.execute` verbose adds: `args_summary` (first 60 chars), `result_summary` (first 200 chars).
  - `tool.bash` verbose adds: full `command`, full `stdout` first 200 chars, full `stderr` first 200 chars.
- **FR-020** Settings tab shows a "Verbose attributes" toggle with a clear warning banner: "Verbose mode includes message previews and tool args/results in telemetry. If you have OTLP export configured, this content leaves your machine."

### 5.6 Settings UI ("Monitoring" tab)

- **FR-021** New `frontend/src/views/settings/MonitoringTab.vue` rendered as a sub-tab in `SettingsView.vue` (alongside General / Providers / Hooks / Bundles).
- **FR-022** Sections in the Monitoring tab:
  - **Local storage**:
    - Retention days slider (1-365, default 30).
    - Current row count per table (live; refreshes on tab focus).
    - "Run sweep now" button.
    - "Export to file (JSONL)" button.
  - **Fleet export (optional)**:
    - OTLP endpoint URL input.
    - Headers editor (chip list: Name + Value, value masked).
    - Insecure-HTTP toggle (required for http:// endpoints).
    - "Test connection" button (sends one harmless `app.heartbeat` span).
    - Last successful export timestamp + last error if any.
  - **Signals**:
    - Traces enabled (default true).
    - Metrics enabled (default true).
    - Logs enabled (default true).
  - **Verbose attributes** (default off, with warning banner).
  - **Inspector**:
    - Last 100 spans / metrics / logs in a sortable table.
    - Click a span → expand attributes + events.
- **FR-023** Bindings: `Telemetry_Stats() → {SpansCount, MetricsCount, LogsCount, LastExport, LastSweep}`, `Telemetry_RunSweep()`, `Telemetry_TestConnection()`, `Telemetry_RecentSpans(n)`, `Telemetry_RecentMetrics(n)`, `Telemetry_RecentLogs(n)`, `Telemetry_ExportToFile(path)`.

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** **Telemetry never blocks the chat surface.** Local insert and OTLP export run on background goroutines with bounded buffers; full buffer drops oldest events with a counter (`telemetry.dropped`) — never blocks the producer.
- **NFR-004** **Retention sweep doesn't lock the DB for chat operations.** Use chunked deletes (1000 rows at a time) under separate write transactions.
- **NFR-005** **Local-first invariant**: when `OTLPEndpoint == ""`, no outbound telemetry traffic. Verified by a test that hits a fake net-listener via httptest and asserts zero connections when endpoint is unset.
- **NFR-006** **PII guardrail invariant**: when `VerboseAttributes == false`, no chat content text appears in any persisted telemetry row. Verified by a test that runs a chat turn with a known sentinel string and walks `telemetry_*` tables for that string.
- **NFR-007** Telemetry adds < 5 ms p99 overhead per chat turn (measured by a benchmark).
- **NFR-008** Local DB growth bounded: at default 30-day retention with typical usage (~100 chat turns/day), telemetry tables stay under 100 MB.

## 7. Acceptance criteria

- **A1** US1 — fresh install with no fleet endpoint → telemetry visible in inspector, no outbound traffic.
- **A2** US2 — fleet endpoint configured → spans + metrics + logs reach a fake httptest OTLP receiver.
- **A3** US3 — verbose=false → no message content in any persisted span attribute (NFR-006 test).
- **A4** US4 — error span on a tool timeout is queryable via the inspector, with `status_code=2` and the error message.
- **A5** US5 — retention slider change persisted; next sweep deletes accordingly.
- **A6** US6 — "Export to file" produces a valid JSONL file with the last N events.
- **A7** US7 — OTLP traffic carries `service.name=kaneaz-harness, service.version=<v>, service.instance.id=<persisted-ulid>`.
- **A8** Slog bridge — every existing `logging.L().Info(...)` call also lands in `telemetry_logs` (when logs enabled).
- **A9** Metric latency — `chat.turn.duration` records per-turn latencies; histogram buckets are reasonable defaults (5ms, 10ms, ..., 30s).
- **A10** Telemetry dropped on full buffer is counted via `telemetry.dropped` counter; chat surface unaffected.

## 8. Architecture

```
core/telemetry/
├── telemetry.go                 # Init + Shutdown + Resource builder
├── tracer.go                    # Tracer wrapper for harness-internal spans
├── meter.go                     # Meter wrapper
├── logger.go                    # Logger + slog bridge
├── exporter_local.go            # SQLite-backed SpanExporter / MetricExporter / LogExporter
├── exporter_local_test.go
├── exporter_otlp.go             # OTLP HTTP exporter wrapper (wraps the SDK's otlphttp)
├── exporter_otlp_test.go
├── exporter_composite.go        # Fan-out: local + optional OTLP
├── exporter_composite_test.go
├── sweep.go                     # Retention sweep
├── sweep_test.go
├── instance.go                  # Persistent service.instance.id at <DataDir>/telemetry/instance.id
└── instance_test.go

core/session/migrations.go       # MODIFIED: register migration 0304
core/session/migrations_telemetry.go  # NEW: migration body

core/rpc/views/telemetry/
├── api.go                       # TelemetryAPI: Stats, RunSweep, TestConnection, Recent*, ExportToFile
├── impl.go
└── impl_test.go

core/rpc/views/settings/
├── api.go                       # MODIFIED: Settings.Telemetry sub-struct
└── impl.go

core/rpc/api.go                  # MODIFIED: telemetry init + accessor + bindings wire-up
core/rpc/bindings.go             # MODIFIED: Telemetry_* bindings
core/rpc/stubs.go                # MODIFIED

core/llm/...                     # MODIFIED: instrument LLM stream span; metrics
core/toolloop/...                # MODIFIED: instrument tool.dispatch + tool.execute spans
core/hooks/...                   # MODIFIED: instrument hook.run spans
core/mcp/stdio/...               # MODIFIED: instrument mcp.* spans
core/rpc/views/llm/impl.go       # MODIFIED: chat.turn span as parent of llm.stream

frontend/src/views/settings/
├── MonitoringTab.vue            # NEW: Settings → Monitoring sub-tab
├── TelemetryInspector.vue       # NEW: last-100 events table with filters
├── FleetEndpointEditor.vue      # NEW: URL + headers chip-list + test button
└── __tests__/

frontend/src/lib/types.ts        # MODIFIED: TelemetrySettings, TelemetryStats, TelemetryEvent
frontend/src/lib/harnessClient.ts  # MODIFIED: telemetry namespace

docs/telemetry.md                # NEW
```

## 9. Edge cases

1. **OTLP endpoint URL malformed** → save validation rejects; UI shows specific parse error.
2. **OTLP endpoint unreachable for hours** → backoff caps at 60 s; UI shows `last_error` + `retry_in`.
3. **Retention sweep encounters DB lock contention** → exponential backoff at the sweep level; document; doesn't block chat operations.
4. **`service.instance.id` file deleted out-of-band** → next boot generates a fresh ULID. Document.
5. **Verbose toggle flipped on, OTLP configured** → existing un-exported events are NOT retroactively re-attributed (they were captured under the old verbose flag); only NEW events use the new attribute set.
6. **Telemetry SDK init fails** (e.g. malformed OTLP URL at startup) → log error, harness continues without telemetry. Chat surface unaffected.
7. **DB grows past 100 MB despite retention** (heavy usage) → user sees a "Telemetry size: N MB" stat with a "Clear all" button.
8. **Span exported AFTER local insert succeeds but BEFORE OTLP confirm** — local copy is canonical; OTLP is best-effort. Eventual consistency model.
9. **Slog handler fan-out failure** (one of file/OTel sinks errors) → other sink still receives. Errors logged at warn but not propagated.
10. **Privacy invariant violation in tests** — a regression test plants a sentinel in chat content + asserts no telemetry row contains it. Fail-loud on regression.

## 10. Out of scope

- Distributed tracing across machines.
- Sampling strategies beyond always-on.
- Pluggable exporters beyond OTLP.
- In-harness dashboards / charts (basic table inspector only).
- Auto-redaction of free-form attributes.
- Real-time streaming export.
- Browser-side telemetry instrumentation (the rpc audit channel covers UI events).
- Span attributes carrying secrets — the redactor mission's pipeline is invoked for free-form fields, but verbose-mode users accept residual risk.

## 11. Open questions

1. **OTel SDK version pin** — `go.opentelemetry.io/otel v1.30+` is current stable. Pin in `go.mod` and document upgrade procedure.
2. **Instance ID rotation** — should the user be able to rotate the `service.instance.id`? Useful when sharing the same harness binary across sandboxes. Default: never rotate (stable per install). UI affordance: "Rotate instance ID" button under "Monitoring → Advanced". Defer to v1.x.
3. **Log levels in OTel** — slog levels (Debug/Info/Warn/Error) map to OTel SeverityNumber 5/9/13/17. Document.
4. **Default retention 30 days vs configurable on first run** — 30 days is the default; settings are immediately editable. No first-run wizard for v1.
5. **Per-signal retention** — single retention setting applies to all three tables. Per-signal retention is a future enhancement.

## 12. Out-of-band dependencies

- `go.opentelemetry.io/otel` (v1.30+ stable).
- `go.opentelemetry.io/otel/sdk` (traces, metrics, logs).
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`.
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`.
- `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp`.
- All MIT/Apache-2.0; standard fare.
