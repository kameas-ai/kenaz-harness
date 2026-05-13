# Spec: Typed port codegen

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Today the graph kernel exchanges port values as `PortValues = map[string]any`. The manifest declares port types (`text`, `tool_calls`, `json`, `messages`, …) and the runtime validator catches type mismatches, but consumers still pay an `any → typed` cast on every read. The cost shows up in three places:

1. **Test surface.** Every node-kind test fakes its own port reads with `v.(string)` / `v.([]ToolCall)` patterns. A misspelled key or a renamed port produces a runtime nil-deref, not a compile error.
2. **Refactor blast radius.** Renaming the `model` node's `response_text` output port today requires grepping for the string `"response_text"` across all consumers. The compiler does not help.
3. **Manifest-to-Go drift.** `*Attrs` structs are already codegenned from the manifest's `attrs:` block. Ports are not. The two halves of a node's contract are managed differently — one machine-checked, one stringly-typed.

At 29 node-kinds with first-party authorship this is annoying. The day we ship third-party node-kinds (post-1.0 plugin marketplace), it becomes a support burden.

## 2. Goals

- Generate `*Inputs` and `*Outputs` Go structs from `core/agentgraph/nodes/manifests/<kind>.yaml`'s `inputs:` / `outputs:` blocks alongside the existing `*Attrs` codegen.
- Provide typed accessors on `PortValues`: `pv.Get<KindName>Inputs() (KindInputs, error)`, `pv.SetKindOutputs(KindOutputs)`.
- Keep the runtime `PortValues = map[string]any` representation unchanged on the wire — typed structs are a Go-side ergonomic layer, not a protocol change.
- Migrate first-party node tests to read/write through the typed accessors.
- CI fails on manifest port edits without re-running codegen (extend `scripts/ci/check-codegen.sh`).

## 3. Non-goals

- Changing the manifest format. Existing `inputs:` / `outputs:` blocks are sufficient.
- Wire-format changes. The websocket / RPC representation stays JSON-with-untyped-values.
- Cross-language codegen. TypeScript port types are a follow-up if Wails bindings ever consume PortValues directly (they currently do not).
- Backwards compatibility with hand-rolled cast sites in third-party code (none exist).

## 4. Functional requirements

### Codegen

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `go generate ./core/agentgraph/...` produces `ports_gen.go` alongside `attrs_gen.go` and `wire_gen.go`. | proposed |
| FR-002 | For every manifest, emit a `<Kind>Inputs` and `<Kind>Outputs` struct. Field names = port names in PascalCase; types map from manifest attr types per the existing `attrs_gen.go` mapping. | proposed |
| FR-003 | Emit `func (pv PortValues) Read<Kind>Inputs() (<Kind>Inputs, error)` that pulls each declared port out of the map and converts; missing required ports return a structured error referencing the port name. | proposed |
| FR-004 | Emit `func (pv PortValues) Write<Kind>Outputs(<Kind>Outputs) PortValues` that writes each declared port into a (cloned) map. | proposed |
| FR-005 | `scripts/ci/check-codegen.sh` fails on uncommitted drift in `ports_gen.go`. | proposed |

### Migration

| ID | Requirement | Status |
|---|---|---|
| FR-006 | Migrate `core/agentgraph/nodes/<kind>/*_test.go` to use typed accessors instead of `v.(string)` casts. | proposed |
| FR-007 | Migrate `core/agentgraph/nodes/<kind>/node.go` consumers where the cast pattern dominates readability. (Not exhaustive — leave low-touch sites alone.) | proposed |
| FR-008 | Update `core/agentgraph/nodes/manifests/README.md` (or equivalent) to document the typed surface. | proposed |

## 5. Open questions

- **Required vs optional ports.** Manifests don't currently distinguish. Proposal: treat all declared ports as required on read; absence is an error. Optional semantics can be added later via a manifest tag.
- **Union types.** A port declared `json` could hold anything. Keep it as `any` in the typed struct? Or generate an empty `JSONValue` wrapper? Probably `any` for now; revisit when a use-case shows up.
- **Sentinel zero-values.** A `text` port holding empty string is ambiguous: missing vs. explicitly empty. Use `*string` for optional? Defer until a real consumer needs the distinction.

## 6. Acceptance criteria

- `go generate ./core/agentgraph/...` emits `ports_gen.go` and CI rejects drift.
- At least one node-kind test (suggest: `model`) is fully migrated to typed accessors and passes `-race -short`.
- The existing `PortValues` consumers in `core/rpc/views/agentgraph/` continue to compile without modification (Go-side ergonomic layer is additive).
