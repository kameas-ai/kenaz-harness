//go:build opa
// +build opa

package opa

// This file is the only place in the codebase that may import the
// OPA SDK (DIRECTIVE_001 / spec C-001). It is built only when the
// caller explicitly opts in with `-tags=opa`.
//
// Implementation outline (pulled into a separate file so the import
// surface is reviewable in one place):
//
//   - Compile takes a CompiledBundle and walks Modules into
//     ast.ParseModule calls, then composes them into a single
//     rego.Rego compiled query per Action.Kind.
//   - The PreparedSet implementation evaluates the prepared query
//     with action.Inputs() supplied as the rego input document.
//   - Decisions decode from the Rego result: a typed `decision`
//     object containing { outcome, reason_code, policy_id,
//     clause_id, message }. The lowering pipeline emits Rego that
//     produces exactly this shape.
//
// The implementation depends on github.com/open-policy-agent/opa
// which must be added to go.mod (already cached locally at
// v0.70.0). Run `go mod tidy` after enabling the build tag to lock
// the dependency tree; the embedded backend in
// core/policy/engine/embedded.go satisfies the same contract for
// the default build and for hermetic tests.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/policy"
	"github.com/kameas-ai/kenaz-harness/core/policy/engine"
)

// Backend is the OPA-backed engine.Backend. Compile parses every
// Rego module in the CompiledBundle and prepares per-Action.Kind
// queries for the hot path.
type Backend struct {
	// PackagePrefix is the Rego package prefix the lowering pipeline
	// uses, e.g. "kenaz.policy". Defaults to "kenaz.policy".
	PackagePrefix string
}

// New constructs an OPA-backed Backend.
func New() *Backend { return &Backend{PackagePrefix: "kenaz.policy"} }

// Name implements engine.Backend.
func (b *Backend) Name() string { return "opa" }

// Compile implements engine.Backend. The implementation is
// intentionally minimal at this WP — it errors with a clear note so
// adopters can incrementally expand it without losing the
// architectural-boundary guarantee. Wiring full OPA evaluation
// happens in a follow-up that lands the lowering pipeline's exact
// Rego output (WP03 produces deterministic modules; here we accept
// them).
func (b *Backend) Compile(_ context.Context, bundle engine.CompiledBundle) (engine.PreparedSet, error) {
	if len(bundle.Modules) == 0 {
		return emptyPrepared{}, nil
	}
	// Real OPA wiring lives behind this seam. The contract: parse
	// each module via ast.ParseModule, build a rego.Rego with the
	// per-kind query rule, and call PrepareForEval to produce a
	// prepared query held in the returned PreparedSet.
	//
	// Until the lowering pipeline emits the final Rego shape, return
	// a typed sentinel so misconfiguration fails loudly rather than
	// silently dropping to a no-op evaluator (FR-007).
	return nil, errors.New("policy/engine/opa: OPA backend wiring deferred; rebuild without -tags=opa to use the embedded backend")
}

type emptyPrepared struct{}

// EvaluateAction implements PreparedSet for an empty bundle.
func (emptyPrepared) EvaluateAction(_ context.Context, _ policy.Action) (*engine.BackendDecision, bool) {
	return nil, false
}
