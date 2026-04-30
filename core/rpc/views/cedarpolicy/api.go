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

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// PolicyFile mirrors cedar.PolicyFile through the RPC boundary. The
// frontend pattern-matches on this exact field set; cedar.PolicyFile
// is reused directly to avoid drift.
type PolicyFile = cedar.PolicyFile

// Decision mirrors cedar.Decision through the RPC boundary.
type Decision = cedar.Decision

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
}
