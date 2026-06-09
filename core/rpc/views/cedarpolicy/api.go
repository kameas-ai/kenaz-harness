// Package cedarpolicy is the view-scoped RPC surface for the
// agent-kernel-graph mission's Cedar policy engine (WP14). It exposes
// the policy-file listing, the manual reload trigger, and the
// recent-decisions audit feed to the frontend Policy panel.
//
// The package name is "cedarpolicy" rather than "policy" to avoid
// collision with core/rpc/views/policy (a parallel mission's surface).
// Each mission owns its own RPC namespace; the frontend chooses which
// to render based on which subsystem is wired in.
package cedarpolicy

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// PolicyFile mirrors cedar.PolicyFile through the RPC boundary. The
// frontend pattern-matches on this exact field set; cedar.PolicyFile
// is reused directly to avoid drift.
type PolicyFile = cedar.PolicyFile

// Decision mirrors cedar.Decision through the RPC boundary.
type Decision = cedar.Decision

// PolicyFileDetail extends PolicyFile with the full source text.
// Source is populated only by GetPolicy — ListPolicies never returns
// source to keep list payloads compact.
type PolicyFileDetail struct {
	PolicyFile
	// Source is the raw Cedar source text. Only populated by GetPolicy.
	Source string `json:"source"`
	// ReadOnly is true for embedded default policies that cannot be
	// modified or deleted through the editor UI.
	ReadOnly bool `json:"read_only"`
}

// ParseError carries a single parse diagnostic with line/column.
// Line and Column are 1-based; zero means "not available" (the
// cedar-go error format is opaque and we parse it best-effort).
type ParseError struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
}

// ParseResult is the outcome of a SavePolicy or ValidatePolicy call.
// OK is true when the source parsed cleanly. Errors is non-empty only
// when OK is false.
type ParseResult struct {
	OK     bool         `json:"ok"`
	Errors []ParseError `json:"errors,omitempty"`
}

// ErrPolicyExists is returned by InstallTemplate when the destination
// file already exists on disk.
var ErrPolicyExists = newSentinelError("cedarpolicy: policy file already exists")

// ErrPolicyEditorDisabled is returned by write-path methods when the
// HARNESS_POLICY_EDITOR_UI feature flag is off.
var ErrPolicyEditorDisabled = newSentinelError("policy editor disabled")

// ErrPolicyFileModified is returned by SavePolicy when expectedModified
// is non-zero and does not match the file's current modification time
// (optimistic concurrency check).
var ErrPolicyFileModified = newSentinelError("cedarpolicy: policy file was modified on disk since last read")

// sentinelError is an immutable error value comparable by identity.
type sentinelError struct{ msg string }

func (e *sentinelError) Error() string { return e.msg }
func newSentinelError(msg string) error { return &sentinelError{msg: msg} }

// CedarPolicyAPI is the view-scoped accessor consumed by the
// frontend's PolicyView. Implementations live in this package; the
// rpc layer wires one and exposes it through Bindings.
type CedarPolicyAPI interface {
	// ListPolicies returns the per-file parse status for every
	// .cedar source the engine has loaded, including the embedded
	// default and any on-disk file under <DataDir>/policy/.
	ListPolicies(ctx context.Context) ([]PolicyFile, error)

	// ReloadPolicies re-walks <DataDir>/policy/ and rebuilds the
	// active policy bundle. Per-file parse failures do not abort the
	// reload; the per-file status is reported via ListPolicies.
	ReloadPolicies(ctx context.Context) error

	// RecentDecisions returns up to limit most-recent gate
	// decisions, newest first. Used by the audit panel.
	RecentDecisions(ctx context.Context, limit int) ([]Decision, error)

	// WritePolicySnippet writes body to <DataDir>/policy/<name>
	// atomically (write-to-tmp then rename). name must satisfy the
	// filename safety regex `^[a-z][a-z0-9_]{0,127}\.cedar$` — the
	// validator rejects path traversal, uppercase, control chars, and
	// lengths that exceed the 133-char total (128 stem + 5 suffix).
	// Body is written verbatim; Cedar syntax errors are caught by the
	// engine on reload, not here. Engine.Reload is triggered
	// best-effort after the write; failure is logged as a warning.
	WritePolicySnippet(ctx context.Context, name string, body string) error

	// RevokePolicySnippet deletes <DataDir>/policy/<name>. name is
	// subject to the same filename safety validation as
	// WritePolicySnippet. Engine.Reload is triggered best-effort.
	RevokePolicySnippet(ctx context.Context, name string) error

	// ── Editor methods (cedar-policy-editor-ui-01KQ8TD6 WP01) ────────

	// GetPolicy reads the source of a single policy file. For embedded
	// default policies (ReadOnly=true), it reads from the embedded FS.
	// For on-disk policies it reads from <DataDir>/policy/<name>.
	GetPolicy(ctx context.Context, name string) (PolicyFileDetail, error)

	// SavePolicy validates source via the cedar parser and, on success,
	// writes it atomically to <DataDir>/policy/<name>. Returns ParseResult
	// with OK=false and diagnostics when the source does not parse.
	// Never writes a file when the parse fails. Triggers Engine.Reload and
	// emits a policy.file_saved audit event on success.
	SavePolicy(ctx context.Context, name string, source string) (ParseResult, error)

	// DeletePolicy removes <DataDir>/policy/<name>. Returns an error if
	// name is a protected embedded default. Triggers Engine.Reload and
	// emits a policy.file_deleted audit event on success.
	DeletePolicy(ctx context.Context, name string) error

	// ValidatePolicy parses source and returns diagnostics. Never touches
	// disk. Used for the editor's debounced live-validation indicator.
	ValidatePolicy(ctx context.Context, source string) (ParseResult, error)

	// InstallTemplate copies the shipped template identified by templateName
	// from the embedded cedar/policies/ directory to <DataDir>/policy/<destName>.
	// Returns ErrPolicyExists if destName already exists on disk.
	// Triggers Engine.Reload and emits a policy.template_installed audit event
	// on success.
	InstallTemplate(ctx context.Context, templateName string, destName string) (PolicyFileDetail, error)

	// ListPlanModeActions returns the canonical set of Cedar action strings
	// that are denied while the session's plan_mode posture is active.
	// These are rendered in the Cedar editor UI's plan-mode reference panel
	// so policy authors know which actions the plan_mode gate blocks.
	// (plan-mode-posture-01KZNP3F WP08)
	ListPlanModeActions(ctx context.Context) ([]string, error)
}
