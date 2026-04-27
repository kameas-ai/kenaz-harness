// Package cedar is the Cedar-based policy engine for the
// agent-kernel-graph mission (WP14). It wraps the official Apache-2.0
// Cedar Go binding (`github.com/cedar-policy/cedar-go`) behind a small
// kaneaz-harness-shaped API, loads `.cedar` policy files from
// `<DataDir>/policy/`, and exposes a single Evaluate entry point that
// every gate hook in the harness calls before performing a gated
// action (tool execution, model selection, memory write, network
// request, file write).
//
// Architectural integrity (DIRECTIVE_001) — exemption rationale:
//
// The project charter's architectural-integrity directive frowns on
// pulling third-party SDKs into `core/`. Cedar-go is the documented
// recommendation in the agent-kernel-graph spec (FR-053) for action
// gating. The carve-out applies because Cedar-go is a configuration
// interpreter — not a service client. It opens no sockets, holds no
// credentials, performs no telemetry, and ships no cloud surface; it
// is a pure-Go evaluator over text policy documents written in the
// Cedar language. Treating it as a build-time vendored evaluator
// (analogous to how `core/storage/sqlite` vendors modernc.org/sqlite
// to read/write our own files) keeps DIRECTIVE_001 intact: the
// harness retains the option to swap Cedar for a hand-rolled rule
// evaluator without changing the policy gate-hook surface, since the
// public API in this package is engine-agnostic.
//
// See also: kitty-specs/agent-kernel-graph-01KQ6391/spec.md §4.10
// "Policy / safety" (FR-053) and the carve-out paragraph appended to
// docs/agent-kernel-graph.md.
//
// Package layout:
//
//   - engine.go     — Engine type, Evaluate / Reload, Decision.
//   - types.go      — kaneaz-harness ↔ Cedar entity-type mapping.
//   - decisions.go  — in-memory decision log + DecisionStore seam.
//   - hooks.go      — gate-hook adapter helpers consumers wire in.
//   - policies/default_policy.cedar — embedded starter policy bundle.
//
// The package is a leaf: it is allowed to import cedar-go and stdlib
// only. Other packages (core/llm, core/memory, core/tools/*) import
// this one to wire gate hooks; this package does NOT import them.
package cedar
