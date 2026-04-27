// Package agentgraph hosts the codegen directive for the manifest-driven
// node catalog (mission agent-kernel-graph-node-catalog-01KQ7JDZ, WP02).
//
// Running `go generate ./...` from the repo root invokes the generator
// at core/agentgraph/nodes/cmd/gen which reads every shipped + user
// manifest, resolves inheritance chains, and emits two files alongside
// this one:
//
//   - attrs_gen.go : per-callable-kind <KindPascal>Attrs structs
//   - wire_gen.go  : NodeKind* constants, decoder switch, ResolvedManifests registry
//
// The generator is byte-equal idempotent (NFR-004); CI runs
// `scripts/ci/check-codegen.sh` to fail any drift between the
// committed _gen.go files and a fresh re-generation.
//
// In WP02 the shipped catalog contains only archetype manifests, so
// the generated files are intentionally near-empty. WP04 lands the
// kind manifests and the output populates with the full reconciled
// taxonomy (29 kinds per spec §4.8).
package agentgraph

//go:generate go run ./nodes/cmd/gen
