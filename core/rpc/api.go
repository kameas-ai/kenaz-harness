// Package rpc is the Wails-binding façade for the harness frontend.
//
// The substantive surface is the HarnessAPI defined by the
// frontend-foundations mission — view-scoped accessors mirroring the
// KenazAPI shape (Observer / VMSandbox / AuditViewer in Kenaz; LLMConnector
// / MCP / A2A / Workflow / Sessions / etc. here). This stub exists so
// main.go's `rpc.New(c)` keeps compiling while that mission lands.
package rpc

import "github.com/sigil-tech/kaneaz-harness/core"

// API is the placeholder Wails-bound struct. The real surface — typed
// accessors, slog instrumentation, and a streamBroker — is built by the
// frontend-foundations mission.
type API struct {
	core *core.Core
}

// New constructs a placeholder API.
func New(c *core.Core) *API { return &API{core: c} }

// Bindings returns the slice of Wails-bound objects. Empty until the
// frontend-foundations mission lands the HarnessAPI surface.
func (a *API) Bindings() []any { return nil }
