# testdata/sync — provenance

Fixtures for provider-credentials-client-01PCCLI01 A2: the settings-sync wire
(`PUT`/`GET /api/v1/sync/{category}`), decoded and encoded by
`core/fleet/sync.go` and checked in `sync_wire_test.go`.

**Source.** kenaz-fleet `origin/main` at
`a528ff4b65ded86c5dc3b2ea976226ee6a43204b` (2026-10-15). The last commit to
touch the files below is `42012d53c3c781839d5ea555818f202ec28d9510`.

- `service/handlers_sync.go`: `PutSyncRequest` (`lww_ts` string, `payload`
  raw JSON), `SyncStateResponse` (`category`, `lww_ts`, `payload`,
  `updated_at`, with timestamps formatted `time.RFC3339Nano` in UTC), the
  absent-row answer (zero time and `{}`), and every error code and message.
- `service/httpcore/httpcore.go`: the `ErrorResponse` envelope
  (`code`, `message`, `details` omitempty).
- `service/sync_schema.go`: the five accepted categories and the
  `schema_violation` path/reason form (`$[key#N]`, "unknown field (not in
  the category allowlist)").
- `service/db/migrations/0062_user_sync_state.up.sql`: `lww_ts TIMESTAMPTZ`,
  which is microsecond resolution (hence the six-digit fractions).
- `docs/contract-harness-sync.md` §2b.

**How they were produced.** By hand, from those structs and handlers: same
field names, same field order as Go's encoder writes them, same codes and
messages. They were **not** recorded from a live Fleet. Recording needs a
signed-in Pro account's bearer token, which the implementing agent must not
handle. Re-record from dev and diff against these when an operator can.

**Files.**

| File | What |
|---|---|
| `put_request_ui_theme.json` | The body the harness must send on PUT |
| `put_response_ui_theme.json` | Fleet's 200 answer to that PUT |
| `get_response_model_prefs.json` | A GET 200 with a stored row |
| `get_response_absent.json` | A GET 200 when nothing is stored yet |
| `error_409_stale_lww_ts.json` | PUT with `lww_ts` <= stored |
| `error_422_lww_ts_in_future.json` | PUT with `lww_ts` > server now + 5m |
| `error_422_unknown_category.json` | PUT to a category outside the five |
| `error_422_schema_violation.json` | PUT with a key not in the allowlist |
| `error_400_invalid_lww_ts.json` | PUT whose `lww_ts` is not RFC 3339 |
