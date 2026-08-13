// Package toolloop is the tool-invocation POLICY layer: everything that
// decides whether, and under what terms, a tool call is allowed to run.
// It does not contain a loop, and has not since the agent-kernel-graph
// chat migration moved iteration into the kernel.
//
// # There is no loop in this package
//
// The name is a fossil. The original package really was a loop — a
// detect-tools / dispatch / feed-results cycle driven from the chassis.
// That cycle is gone: the `agent_loop` node in
// core/rpc/views/agentgraph/library/chat_default.yaml is the only loop
// in the chat path, and core/agentgraph's Kernel drives it.
//
// What remained after the migration was not a remnant — it was the part
// that never belonged to the loop in the first place. Subsequent
// missions then deliberately grew it. What lives here today is a single
// coherent thing: the answer to "may this tool call proceed, on whose
// authority, and does it cost the caller anything?"
//
//	perms.go       — the three-valued permission verdict
//	                 (auto_allow | confirm_each | deny) and the resolvers
//	                 that compute it from static config + session overrides.
//	confirm.go     — ConfirmBus: the deadline-free pause primitive that
//	                 makes a `confirm_each` verdict real
//	                 (confirm-each-enforcement-01PMAG05 WP01).
//	grants.go      — how an answer outlives the question
//	                 (SessionGrantCache, PersistentGrantStore) and what a
//	                 `confirm_each` means with nobody to ask
//	                 (HeadlessConfirmPolicy).
//	promptskip.go  — the autonomy prompt-skip set and the tool-family
//	                 classifier that makes `autoApproveFamilies` per-family
//	                 rather than all-or-nothing.
//	builtins.go    — BuiltinRegistry / BuiltinPool: the map from a
//	                 namespaced kenaz__<tool> name to a concrete in-binary
//	                 implementation, which is what makes a permission
//	                 verdict addressable to something real.
//	iteration.go   — the passive-tool set and ShouldCountIteration: whether
//	                 a dispatch charges the KnobMaxIterations budget (FR-010).
//	context.go     — session-ID context plumbing for built-in tools.
//	types.go       — the narrow projections (Resolution, MCPPool, Tool) the
//	                 resolver and the chassis-side discoverer share.
//
// # Why the name has not been changed
//
// agentgraph-total-convergence-01PMGX01 WP14 considered renaming this
// package to what it is (`toolpolicy`) and DEFERRED the rename
// deliberately, on 2026-08-13. The reasoning, recorded here so the next
// reader does not have to re-derive it:
//
//   - The rename is mechanical but wide: 18 non-test Go files import this
//     package and ~500 `toolloop` occurrences span ~180 files.
//   - It is not confined to Go. ConfirmRequest crosses the Wails binding
//     boundary, so the package name is also a generated TypeScript
//     namespace (frontend/wailsjs/go/models.ts `export namespace
//     toolloop`, imported by Bindings.d.ts). Renaming requires
//     `wails generate module`, whose toolchain is NOT available on the
//     self-hosted kameas-ci-* pool — CI cannot reproduce or verify the
//     regenerated output, it can only compare the committed
//     scripts/ci/wailsjs-bindings.sha256 (see check-codegen.sh). A
//     binding-surface regeneration that CI cannot re-derive is the wrong
//     thing to land in the closing sweep of a 15-branch release.
//   - The honesty defect was entirely in the documentation. Every doc
//     comment in this package that described a loop asserted something
//     false about the running system; a package IDENTIFIER that is merely
//     badly named does not mislead a reader who opens the file. WP14
//     fixed the former in full.
//
// I7 (spec §6) is already satisfied: this package has 18 non-test
// importers and is not on scripts/ci/allowlists/i7-orphan-packages.txt.
// WP14's original acceptance criterion ("I7 loses its core/toolloop
// line") was written before the campaign refilled the package and is
// moot.
//
// The rename remains a good idea. It should ride a change that already
// regenerates the Wails binding surface for its own reasons, so the
// regen is verified rather than taken on faith.
package toolloop
