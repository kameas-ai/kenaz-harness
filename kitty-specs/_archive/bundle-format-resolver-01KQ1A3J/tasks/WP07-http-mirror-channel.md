---
work_package_id: "WP07"
title: "HTTP mirror distribution channel"
dependencies:
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 7 - Channels (http_mirror)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – HTTP mirror distribution channel

## Goal

Implement the `http_mirror` channel: fetch bundle artifacts from an HTTP(S) URL with hash verification at the pipeline boundary. This is the lightest-weight channel for static mirrors (CDNs, S3 static-site, simple file servers).

## Spec references

- FR-003 Distribution-channel abstraction (http_mirror is a day-one kind)
- FR-006 Content-hash integrity verification
- FR-012 Pre-flight validation
- C-002 No covert network egress
- NFR-005 Local-first (cache hit short-circuits the fetch)

## Plan references

- Plan §2 `core/bundle/channels/http/`
- Plan §3.4 Channel interface
- Plan §4.3 Fetch pipeline (this channel just delivers bytes; verification is the resolver's job)
- Plan §6.2 secrets-keychain (optional bearer / basic auth via creds resolver)

## Cross-mission dependencies

- **secrets-keychain**: optional bearer token or basic auth header from credential refs.

## Subtasks

- T001 Implement `http.Channel.Reachable(ctx)`: HEAD request to the base URL; return `ErrChannelUnreachable` on non-2xx or network error.
- T002 Implement `http.Channel.Fetch(ctx, coord, sink)`: GET `<base>/<coord.Path>` with optional Authorization header from `secrets.Resolver`; stream the response body to sink with `io.Copy`. Honor `ctx.Done()`.
- T003 Implement `http.Channel.LookupSignatures(ctx, coord)`: GET `<base>/<coord.Path>.sig` (404 → empty list, not error).
- T004 Default HTTP client: TLS-only by default; allow `insecure: true` only via explicit `ChannelSpec` option (logged as a warning event).
- T005 Redirect policy: follow up to N redirects within the same host; cross-host redirects emit a warning event and may be rejected by spec option.
- T006 Cancellation: in-flight body reads abort on `ctx.Done()` and the partial sink contents are discarded by the resolver layer (CAS staging).

## Acceptance criteria

- Integration test against a `httptest.Server` fixture successfully fetches an artifact and a sibling `.sig` file.
- HTTPS-only is the default; HTTP requires explicit opt-in.
- 5xx response yields `ErrChannelUnreachable`; 404 on the artifact yields a typed not-found error.
- Cancellation mid-stream returns `ErrCancelled`.
- Authorization header is set only when a creds ref is configured; never logged.

## Files to create/modify

- `core/bundle/channels/http/http.go` (new — Channel impl)
- `core/bundle/channels/http/auth.go` (new — credential header helpers)

## Definition of done

- All acceptance criteria pass via `httptest`-backed unit/integration tests.
- Default policy is TLS-only; opt-in HTTP is explicit and logged.
- Package imports only `channels`, `cache`, `errors`, `secrets` interface, and `net/http`.
- Cross-host redirect behavior documented and tested.
