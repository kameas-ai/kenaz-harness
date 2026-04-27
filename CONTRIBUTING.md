# Contributing to Kaneaz Harness

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
- Go 1.22+
- Node 20+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation): `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

```bash
git clone https://github.com/kameas-ai/kaneaz-harness.git
cd kaneaz-harness

# Live development
wails dev

# Production build
wails build

# Tests
go test ./core/... -race -count=1 -short
cd frontend && npm test
```

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

## License

By contributing, you agree your contributions will be licensed under the
project's [Apache 2.0 license](LICENSE).
