# Envelope SDK upgrade procedure

The `core/acp/envelope` package is the SOLE Go package permitted to import
`github.com/a2aproject/a2a-go`. This document records the procedure for
upgrading that pin (FR-015, NFR-010, plan §8 R1).

## Pin policy

- Direct module dependency, pinned to a specific minor version (plan
  §9 Q2).
- Major-version bumps require an ADR documenting the diff scope and
  the migration plan for envelope converters.
- A `golangci-lint` `depguard` rule enforces that no package outside
  `core/acp/envelope/...` imports `github.com/a2aproject/a2a-go`. CI
  fails on any violation.

## Non-breaking minor / patch upgrade

1. Bump the version in `go.mod` and run `go mod tidy`.
2. Run the full envelope test suite: `go test -race ./core/acp/envelope/...`.
3. If converter tests fail, update `convert.go` to track any SDK
   field renames; the public `Envelope.*` API surface MUST NOT
   change.
4. Run `go test -race ./core/acp/...` to confirm downstream packages
   are unaffected.
5. Open a PR titled `chore(acp): bump a2a-go to <version>` summarising
   the SDK release notes.

## Breaking major upgrade

1. Open an ADR under `docs/adr/` titled
   `acp-envelope-a2a-go-<from>-to-<to>.md` capturing the wire-protocol
   diff, the converter changes, and the rollout plan.
2. Update `convert.go` to map between old and new SDK shapes. Add a
   transitional test that round-trips a fixture Task through both
   versions.
3. Verify the depguard rule still passes — no consumer outside
   envelope should need editing.
4. Update `go.mod`; run `go test -race ./core/acp/...`.
5. Tag a release-candidate harness build and run the cross-platform
   conformance suite before merging.

## What never changes on upgrade

- `Envelope.Dispatch`, `Envelope.FetchCard`, `Envelope.CancelTask`,
  `Envelope.AcceptTask`, `Envelope.Respond` signatures (all return
  `core/acp.*` types and never SDK types).
- The `Dialer` / `Listener` / `Conn` / `TransportListener` interfaces
  consumed by `core/acp/transports/<kind>/`.
- The `core/acp.AgentCard`, `acp.Task`, `acp.Message`, `acp.Skill`
  shapes downstream packages depend on.

If a non-trivial breaking change forces a public-API edit, it is no
longer a contained envelope upgrade — it is an architecture change
and warrants a separate spec-kitty mission.
