// Package context implements the shared-context-distribution mission
// (mission shared-context-distribution-01KQ18PA): org/team/personal context
// packs that layer over each other and are injected into every agent
// session through a single declarative hook.
//
// The package is organised into focused sub-packages per
// DIRECTIVE_001 (architectural integrity):
//
//   - pack/      on-disk pack format parser + validator (YAML+Markdown)
//   - merge/     three-tier merge engine + override registry
//   - scope/     workflow / agent / role access scoping
//   - snapshot/  content-addressable resolution snapshot
//   - inject/    session-time injection hook
//   - audit/     event-log emission shapes (consumes core/event)
//   - replay/    snapshot reconstruction from event-log entries
//   - policy/    conflict / size / fail-closed policy substrate
//   - cache/     local pack cache + offline operation
//
// External integrations (bundle resolver, trust verifier, event log,
// secrets keychain, storage) are reached only through their public
// APIs. This package never reaches around them.
package context
