// Package context implements the shared-context-distribution mission
// (mission shared-context-distribution-01KQ18PA): org/team/personal context
// packs that layer over each other and are injected into every agent
// session through a single declarative hook.
//
// The package is organised into focused sub-packages per
// DIRECTIVE_001 (architectural integrity):
//
//   - audit/      event-log emission shapes (consumes core/event)
//   - bundlekind/ context-pack artifact-kind handler for the bundle resolver
//   - merge/      three-tier merge engine + override registry
//   - pack/       on-disk pack format parser + validator (YAML+Markdown)
//   - policy/     conflict / size / fail-closed policy substrate
//   - scope/      workflow / agent / role access scoping
//   - snapshot/   content-addressable resolution snapshot
//   - verify/     provenance-verification facade over the trust API
//
// External integrations (bundle resolver, trust verifier, event log,
// secrets keychain, storage) are reached only through their public
// APIs. This package never reaches around them.
package context
