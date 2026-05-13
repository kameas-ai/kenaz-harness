# Spec: Manifest versioning + behavioral fingerprints

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The node-kind manifest is the contract a graph is authored against. We already handle **name** changes well — the alias system maps `llm → model` and `plan → planner` silently — but we have no handle on **behavioral** changes:

- If `model.yaml`'s default temperature changes from 0.7 to 0.3, graphs authored against the old default get new outputs silently.
- If a port's semantics change (e.g. `tool_calls` starts emitting empty arrays where it previously omitted the port), downstream consumers can fail in subtle ways.
- If a builtin tool's argument schema gains a required field, plans referencing the tool may need to be re-authored.

The fact that we keep finding these post-hoc — and have to backfill regression tests — is the symptom. The fix is a **manifest version stamp + behavior fingerprint** that travels with the graph at author-time and is verified at run-time.

## 2. Goals

- Every manifest carries a `manifest_version: <semver>` field, bumped manually on behavioral change.
- Every manifest carries a `behavior_fingerprint: <sha256>` field, computed from the manifest's behavior-affecting subset (defaults, ports, attrs, but not `description` / `display_label`).
- Graphs persist the `{kind, manifest_version, behavior_fingerprint}` per node at author-time.
- At graph load, mismatch between stored fingerprint and current manifest fingerprint surfaces a `manifest_drift` warning on the node (non-blocking by default; blocking under a strict-mode dial).
- Migration tool offers to upgrade-in-place: "this graph was authored against `model@1.2` (temp=0.7); current is `model@1.3` (temp=0.3). Pin to 0.7, accept 0.3, or review the diff."

## 3. Non-goals

- Multi-version-coexistence at runtime. Only one version of each manifest ships per binary.
- Manifest backwards-compatibility shims. If a graph author wants the old behavior they explicitly pin the attr (`temperature: 0.7`) in the graph.
- SemVer-driven feature flags. Versioning here is observational, not behavioral-branching.

## 4. Functional requirements

### Manifest schema

| ID | Requirement | Status |
|---|---|---|
| FR-001 | All `core/agentgraph/nodes/manifests/*.yaml` carry `manifest_version: "<semver>"` (initial value: `1.0.0`). | proposed |
| FR-002 | `core/agentgraph/manifest_fingerprint.go` computes `sha256` over a canonicalized subset: ports, attr names + types + defaults, declared invariants. Excludes display strings and documentation. | proposed |
| FR-003 | Manifest fingerprint is emitted into `attrs_gen.go` as a generated constant per kind: `const ModelManifestFingerprint = "sha256:…"`. | proposed |
| FR-004 | CI step computes the fingerprint at build-time and fails if the manifest_version was not bumped when the fingerprint changed. | proposed |

### Graph persistence

| ID | Requirement | Status |
|---|---|---|
| FR-005 | Graph persistence layer (`storage/graphs.go`) stores `{kind, manifest_version_at_author, fingerprint_at_author}` per node. Schema migration adds two columns to the node table. | proposed |
| FR-006 | On graph load, the runtime compares the stored fingerprint to the current binary's fingerprint and emits a `manifest_drift` warning on the node entity. | proposed |
| FR-007 | The frontend graph editor surfaces a drift badge on affected nodes with an action: "Open drift inspector". | proposed |

### Strict mode

| ID | Requirement | Status |
|---|---|---|
| FR-008 | New autonomy/posture dial: `manifest_drift: warn | block`. Defaults to `warn`. `block` refuses to run a graph with drifted nodes until the user resolves. | proposed |
| FR-009 | Drift inspector UI offers three actions per drifted node: (a) accept new behavior + restamp, (b) pin old behavior by inlining the old defaults into the node's attrs, (c) skip / continue with warning. | proposed |

## 5. Open questions

- **Does manifest_version live in the manifest file or in a sibling `CHANGELOG.md` per kind?** Proposal: in the YAML. One source of truth; CI cross-checks.
- **Behavioral fingerprint canonicalization.** Map order matters in YAML serialization; need a deterministic JSON re-encode before hashing.
- **Inherited fingerprints.** If a node-kind embeds another (e.g. composite manifests), should fingerprints chain? Defer until composite manifests exist.

## 6. Acceptance criteria

- All 29 first-party manifests carry `manifest_version` and generate a non-empty `*ManifestFingerprint` constant.
- CI rejects a manifest semantic change without a version bump.
- A graph authored before the schema change loads cleanly (stored fingerprint = empty → no drift warning, just an "unknown provenance" badge).
- A manually-induced manifest tweak in dev reliably surfaces a drift badge on the affected node in the editor.
