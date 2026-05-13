// Package agentgraph hosts the codegen directive for the manifest-driven
// node catalog (mission agent-kernel-graph-node-catalog-01KQ7JDZ, WP02;
// typed port surface added in mission typed-port-codegen-01NDFSEX01).
//
// Running `go generate ./...` from the repo root invokes the generator
// at core/agentgraph/nodes/cmd/gen which reads every shipped + user
// manifest, resolves inheritance chains, and emits three files alongside
// this one:
//
//   - attrs_gen.go  : per-callable-kind <KindPascal>Attrs structs,
//     each implementing NodeAttrs (nodeAttrsMarker + Validate).
//   - wire_gen.go   : NodeKind* constants, AllNodeKinds, decodeAttrs,
//     defaultAttrsFor/defaultPortsFor dispatchers, and the
//     ResolvedManifests registry populated at init.
//   - ports_gen.go  : typed port surface for every callable kind.
//     For kind K emits:
//       KInputs / KOutputs  — one field per port, typed by port type
//         (messages→[]Message, text/branch_id→string, bool→bool,
//          number→float64, everything else→any)
//       ReadKInputs(PortValues) (KInputs, error)
//         — extracts and type-asserts inputs; returns a descriptive
//           error for absent required ports or un-castable values.
//       (KOutputs).ToPortValues() PortValues
//         — converts back to the wire map[string]any format.
//     The wire format (PortValues = map[string]any) is never changed;
//     these are purely Go-side ergonomic helpers.
//
// The generator is byte-equal idempotent (NFR-004); CI runs
// `scripts/ci/check-codegen.sh` to fail any drift between the
// committed *_gen.go files and a fresh re-generation. The gate covers
// all three generated files.
package agentgraph

//go:generate go run ./nodes/cmd/gen
