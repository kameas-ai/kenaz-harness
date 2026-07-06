# Contributing to Kenaz Harness

Thanks for your interest in contributing.

## Before you start

1. **Open an issue first.** For anything beyond a typo or a docs tweak, open
   an issue describing what you want to change and why. Saves time if the
   change conflicts with project direction.
2. **One logical change per PR.** Don't bundle unrelated fixes. Each PR should
   be reviewable in isolation.
3. **Read the charter.** Project policy lives in `.kittify/charter/charter.md`
   (local; not in the public repo). The load-bearing rules:
   - **C-001** — no third-party SDKs in `core/`. Stdlib + already-vendored
     deps only. Configuration interpreters (Cedar) carry an explicit
     carve-out documented in `docs/adr/`.
   - **DIRECTIVE-001** — no cyclic imports. The dependency direction is
     fixed; consult `core/` doc.go files before adding a new import edge.
   - **Privacy CI invariants** — credentials never touch disk in plaintext,
     telemetry verbose-attribute is off by default, no outbound traffic
     except authorized model + MCP calls.

## Development setup

Requires:
- Go 1.25+
- Node 20+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

```bash
git clone https://github.com/kameas-ai/kenaz-harness.git
cd kenaz-harness

# Live development
wails dev

# Production build
wails build

# Tests
go test ./core/... -race -count=1 -short
cd frontend && npm test
```

## Fleet telemetry in dev

When running `wails dev` with a Kameas account, the harness can export OTel
spans, metrics, and log records to the dev fleet server so you can observe
real harness telemetry in the fleet analytics dashboard.

### Quick start

```bash
# Preferred: use the wrapper script (sets KENAZ_HARNESS_ENV=dev automatically)
bash scripts/dev.sh

# Or set the env var manually
KENAZ_HARNESS_ENV=dev wails dev
```

`KENAZ_HARNESS_ENV=dev` makes `core/fleet.ResolveProfile()` return the dev
`EnvProfile`, which points the fleet OTLP pipeline at
`https://dev.fleet.kameas.ai`. The harness must be signed in for the
`tokenRoundTripper` to inject a Bearer token; pre-login spans accumulate in
the 10 000-span ring buffer and are flushed after the first successful login.

### Opt-in required

Fleet telemetry export only runs when the user has set consent to "aggregate"
or "full" in **Settings → Privacy → Fleet telemetry**. The default is "none".
No outbound network traffic occurs at consent = "none".

### Disabling fleet export

Set `KENAZ_HARNESS_ENV=` (empty string) or leave consent at "none":

```bash
KENAZ_HARNESS_ENV= wails dev   # no fleet exporters constructed
```

An empty `KENAZ_HARNESS_ENV` resolves to the prod OSS profile (no
`FleetBaseURL`), so no fleet exporter is ever constructed and no outbound
traffic occurs — consistent with the OSS binary distribution (NFR-001).

### Observing telemetry

After sign-in with a valid dev-fleet JWT, spans should reach the fleet
receiver within one 30-second flush cycle. You can verify with:

```bash
# Watch fleet access logs on the dev ALB (requires Tailscale + fleet-admin role)
# Or check the fleet analytics dashboard at https://dev.fleet.kameas.ai/dashboard
```

### Relevant files

| File | Purpose |
|---|---|
| `core/fleet/otlp_pipeline.go` | `FleetOTLPPipeline` — lazy exporter wiring; `Activate` post-login |
| `core/fleet/otlp_transport.go` | `tokenRoundTripper` — per-request Bearer injection |
| `core/fleet/otel_exporter.go` | `FleetSpanExporter` — ring buffer + JSON batch flush |
| `core/fleet/telemetry_redactor.go` | `DefaultRedactor` — scrubs secrets at enqueue |
| `core/fleet/env.go` | `ResolveProfile` / `OTLPBaseURL` — env-to-endpoint mapping |

---

## Code standards

### Go

- **Effective Go** is the style authority.
- Interfaces at package boundaries. Consumers depend on interfaces, not
  concrete types. See `core/agentgraph/seams.go` for the canonical pattern.
- Table-driven tests with `t.Run`. No hand-written mocks where a fake is
  cheaper.
- Errors wrapped with context: `fmt.Errorf("operation: %w", err)`.
- `go vet ./core/...` and `go test ./core/... -race` must pass.
- Charter C-001: no third-party SDKs in `core/`. Vendor the value, not the
  client.

### Vue + TypeScript

- TypeScript strict mode. No `any` (privacy CI invariant + ESLint enforce).
- Wire types are hand-curated in `frontend/src/lib/types.ts`. The frontend
  doesn't import from `frontend/wailsjs/go/models` directly — consult the
  existing pattern.
- Vitest for component tests; mirror the harnessClient fake pattern.

### Manifests + codegen

If you add a node-kind to `core/agentgraph/nodes/manifests/<kind>.yaml`:

```bash
go generate ./core/agentgraph/...
```

Then commit both the manifest and the regenerated `core/agentgraph/{attrs,wire}_gen.go`.
CI runs `scripts/ci/check-codegen.sh` and fails on drift.

## Credential Hygiene

Never pass raw API keys or credential bytes through RPC or handler layers. The
project enforces this with `scripts/ci/check-no-cred-bytes-in-rpc.sh`, which
runs before `golangci-lint` on every PR and fails the build if either condition
is violated:

1. **`cred []byte` in non-credstore packages** — the pattern `cred []byte` is
   reserved for `core/credstore/` (the canonical credential store),
   `core/secrets/` (opaque secret primitives), and the pre-existing
   `core/llm/` adapter boundary (being migrated by the `credential-store`
   mission). Everywhere else, pass credentials via `credstore.Use` or
   `credstore.RoundTrip` — helpers provided by `core/credstore/`. These keep
   secrets opaque and never let the raw bytes escape into logs or RPC payloads.

2. **Hard-coded API key literals** — literals matching `sk-ant-[A-Za-z0-9]{16,}`
   (Anthropic) or `sk-[A-Za-z0-9]{20,}` (OpenAI-style) outside test files,
   `testdata/`, `vendor/`, or `kitty-specs/` fail the check. Load credentials
   at runtime from the OS keychain via `credstore.Use`, or from environment
   variables in local dev.

Run the check locally before pushing:

```bash
bash scripts/ci/check-no-cred-bytes-in-rpc.sh
```

## Areas where help is welcome

- New MCP recipes (`core/mcp/recipes/`)
- New compaction strategies (`core/agentgraph/compaction/`)
- Provider integrations (`core/llm/`)
- Frontend polish — node palette UX, branch sidebar, memory inspector
- Documentation under `docs/`
- Cookbook examples — graph YAML files showing real-world compositions

## Pull request checklist

- [ ] Issue opened and linked
- [ ] Tests added (Go + frontend where relevant)
- [ ] `go test ./core/... -race -count=1 -short` passes
- [ ] `go vet ./core/...` passes
- [ ] `go generate ./core/agentgraph/...` produces no drift (`scripts/ci/check-codegen.sh`)
- [ ] Frontend tests pass
- [ ] No new third-party SDKs in `core/` (or carve-out documented in `docs/adr/`)
- [ ] No imports that violate DIRECTIVE-001
- [ ] `bash scripts/ci/check-no-cred-bytes-in-rpc.sh` exits 0 (credential hygiene)
- [ ] CLA signed (CLA-checking bot will prompt on first PR)

## Contributor License Agreement

Before your first pull request can be merged, you must sign the
[Kameas Contributor License Agreement](https://kameas.ai/cla.html).
The CLA-checking bot will post a one-time signing link on your first PR;
click it, sign in with your GitHub account, and accept. After that, all
your future contributions to any Kameas open-source repository are
covered &mdash; you only sign once across the whole org.

If you are contributing on behalf of an employer or any other legal
entity, your employer should execute the Corporate CLA (Part B of the
linked CLA document) and notify <legal@kameas.ai> of the list of
authorized contributors. Until that's done, you can still sign as an
individual (Part A) provided you have the necessary rights from your
employer to do so &mdash; the CLA includes the standard representations.

By contributing, you also agree that your contributions are licensed
under the project's [Apache 2.0 license](LICENSE) for distribution in
this repository, and that Kameas AI, Inc. may relicense them as
described in the CLA (so the open-core commercial offering can
incorporate community work).

Questions about the CLA: <legal@kameas.ai>.

## Third-party attributions

If you add a new Go or Node dependency, you don't need to update `NOTICES`
by hand &mdash; the `oss-attributions` workflow regenerates it on each
release and opens a PR with the diff. If your PR adds a dependency, please
flag it in the PR description so reviewers can think about license
compatibility (in particular, avoid LGPL / MPL / EPL dependencies without
checking with maintainers first, since those add source-disclosure
obligations).
