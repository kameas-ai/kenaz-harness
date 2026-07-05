package rpc

// RPCError is the structured error envelope that the harness uses to carry
// typed failure information across the Wails RPC boundary (FR-008 /
// agent-loop-robustness-parity WP08). Wails serialises Go errors as plain
// strings, so callers that need richer error context return *RPCError as a
// first-class result field alongside the normal return value.
//
// The Code field uses a small stable vocabulary:
//
//	"auth"                 — authentication / API-key failure (ErrAuth)
//	"transient"            — recoverable transient failure (ErrTransient)
//	"invalid_request"      — the request was malformed (ErrInvalidRequest)
//	"budget_exhausted"     — retry budget consumed (ErrRetryBudgetExhausted)
//	"context_overflow"     — context window exceeded (ErrInvalidRequest + overflow keywords)
//	"internal"             — unexpected internal error (catch-all)
//
// When Retryable is true the frontend may offer a "Retry" affordance; when
// false the user must change something (rotate key, edit message, etc.)
// before retrying will help.
//
// Hint is optional copy the frontend can display verbatim as a next-step
// suggestion. Empty when no actionable guidance is available.
type RPCError struct {
	// Code is the machine-readable error category.
	Code string `json:"code"`
	// Message is the human-readable primary error description.
	Message string `json:"message"`
	// Hint is an optional action-oriented suggestion for the user.
	// Empty when no specific action is available.
	Hint string `json:"hint,omitempty"`
	// Retryable is true when the operation may succeed if retried
	// without user intervention (e.g. a transient 5xx).
	Retryable bool `json:"retryable"`
}

// BootHealthReport is returned by BootHealth_Get. Each field is the
// human-readable init-phase error string from the named subsystem, or
// empty when that subsystem initialised without errors (FR-008 WP08).
type BootHealthReport struct {
	// MCPInitError is non-empty when MCP pool startup failed. The string
	// is suitable for display in a "MCP init failed" banner.
	MCPInitError string `json:"mcpInitError,omitempty"`
	// SkillsInitError is non-empty when slash-command / skills wiring failed.
	SkillsInitError string `json:"skillsInitError,omitempty"`
	// FleetInitError is non-empty when fleet telemetry / sync startup failed.
	FleetInitError string `json:"fleetInitError,omitempty"`
}

// IsHealthy reports true when no subsystem has a recorded init error.
func (r BootHealthReport) IsHealthy() bool {
	return r.MCPInitError == "" && r.SkillsInitError == "" && r.FleetInitError == ""
}
