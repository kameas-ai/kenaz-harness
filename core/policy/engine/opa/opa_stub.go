//go:build !opa
// +build !opa

package opa

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/policy/engine"
)

// ErrOPADisabled is returned by the stub backend when the build tag
// `opa` is not set. The default in-process engine.EmbeddedBackend is
// used in that configuration; the OPA adapter is the optional path
// for operators who want auditor-recognizable Rego.
var ErrOPADisabled = errors.New("policy/engine/opa: built without the `opa` build tag; embedded backend in use")

// Backend is the typed Backend the engine wires up. With the `opa`
// build tag the implementation in opa_real.go takes effect; without
// it, this stub returns ErrOPADisabled on Compile so misconfigured
// callers fail loudly rather than silently dropping to the embedded
// path.
type Backend struct{}

// New constructs the OPA-backed engine.Backend. Without the `opa`
// build tag, the returned backend errors on first Compile.
func New() *Backend { return &Backend{} }

// Name implements engine.Backend.
func (b *Backend) Name() string { return "opa(disabled)" }

// Compile implements engine.Backend.
func (b *Backend) Compile(_ context.Context, _ engine.CompiledBundle) (engine.PreparedSet, error) {
	return nil, ErrOPADisabled
}
