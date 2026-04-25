// Package bundlekind exposes the context-pack artifact-kind handler that
// the bundle resolver (mission bundle-format-resolver-01KQ1A3J) consumes
// through its public artifact-kind registry.
//
// Per C-001 / DIRECTIVE_001 this package is the only place the
// shared-context system reaches across to core/bundle/. The bundle
// resolver's registry lives in its own package; until that public API is
// stable, this package consumes a minimally-typed contract surface
// declared here as [Registry] / [Handler]. When the bundle mission lands,
// these adapter types collapse onto its real interfaces by implementing
// the same methods.
package bundlekind
