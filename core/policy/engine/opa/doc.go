// Package opa contains the OPA-Rego adapter for the policy engine.
// Per DIRECTIVE_001 / spec C-001, this package is the only place in
// the codebase allowed to import github.com/open-policy-agent/opa/...
//
// The adapter is gated by the `opa` build tag. When the tag is not
// set, the package compiles to a stub that documents how to enable
// the real backend; the engine's default EmbeddedBackend is used
// instead.
//
// To enable: build with `-tags=opa` after running `go mod tidy` to
// pull github.com/open-policy-agent/opa@v0.70.0 (cached locally).
//
//	go test -tags=opa ./core/policy/engine/opa/...
//
// The stub-vs-real split keeps the engine's open-source-by-default
// build dependency-light while preserving the architectural seam the
// charter mandates.
package opa
