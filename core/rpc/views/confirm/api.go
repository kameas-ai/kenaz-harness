// Package confirm is the view-scoped RPC surface for the confirm-each
// tool-confirmation modal (confirm-each-enforcement-01PMAG05 WP02/WP03).
//
// It is the RETURN leg of the pause. The forward leg is the chat
// runner's kernelToolAdapter, which parks a `confirm_each` tool call on
// a toolloop.ConfirmBus and publishes a ConfirmRequest on the
// "tool:confirm-pending" broker topic; this package is how the user's
// answer gets back to the parked goroutine.
//
// Wire flow:
//
//  1. Model emits N tool calls; the kernel's tool_dispatch stamps one
//     batch id on the dispatch context.
//  2. Each call whose verdict is confirm_each parks on the bus and
//     publishes a ConfirmRequest carrying (session_id, call_id,
//     batch_id, server, tool, args_summary). The goroutine blocks — with
//     no deadline (owner decision 1).
//  3. The frontend renders ONE modal per batch_id with one row per
//     pending call.
//  4. Per row the user picks approve / deny, optionally ticking "allow
//     for this session"; or hits approve-all; or dismisses the dialog.
//     Each maps to a call below.
//  5. Resolve delivers the decision, the parked goroutine wakes, and the
//     call dispatches or returns a model-readable tool error.
//
// # Why dismissal is deny and absence is nothing
//
// CancelBatch is the dismissal path and it denies every remaining row
// (FR-003). What has no path here is a clock: nothing in this package or
// in the bus converts elapsed time into an outcome. A user who walks
// away leaves the run parked, and ListPending is how the modal comes
// back after a reload — the pause lives in the harness process, not in
// the dialog.
package confirm

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// ErrBusUnavailable is returned when no ConfirmBus is wired — a chassis
// built without the chat stack, or a test double. Distinct from
// ErrUnknownConfirmation ("the bus is here, that row is not") so the
// frontend can tell a misconfigured harness from a stale dialog.
var ErrBusUnavailable = errors.New("confirm: no confirmation bus is wired")

// ErrUnknownConfirmation re-exports the bus's sentinel so callers of
// this view do not need to import toolloop to classify the error. It is
// the same value, so errors.Is matches across both.
var ErrUnknownConfirmation = toolloop.ErrUnknownConfirmation

// PendingConfirmation is one parked row as the frontend sees it. It is
// toolloop.ConfirmRequest by alias: the payload published on
// "tool:confirm-pending" and the payload returned by ListPending are the
// SAME shape, deliberately, so the modal has one row type whether it
// learned about a row from an event or from a reconnect snapshot.
type PendingConfirmation = toolloop.ConfirmRequest

// ConfirmAPI is the view-scoped accessor the Bindings layer consumes.
type ConfirmAPI interface {
	// Resolve delivers one row's answer.
	//
	// approved=false is a denial: the parked call returns a tool error
	// the model can read. rememberSession is honoured only on an
	// approval — "deny, but remember to allow" is not a thing a user can
	// mean, and the dispatcher enforces that too.
	//
	// Returns ErrUnknownConfirmation when the row has already been
	// answered (double-click, a stale dialog, a batch cancelled from
	// another window).
	Resolve(ctx context.Context, sessionID, callID string, approved bool, reason string, rememberSession bool) error

	// ResolveAlways approves one row AND asks the dispatcher to write a
	// durable allow rule for its (server, tool).
	//
	// A separate method rather than a fourth boolean on Resolve because
	// it is a separate act with a different blast radius (owner decision
	// 2: the control is visually separated so it cannot be hit by
	// reflex). Keeping the RPC surfaces distinct means a UI bug in the
	// checkbox wiring cannot silently persist a rule.
	ResolveAlways(ctx context.Context, sessionID, callID, reason string) error

	// ApproveBatch approves every row still parked under batchID and
	// returns how many it resolved. Backs the modal's approve-all.
	// Rows already answered individually are untouched.
	ApproveBatch(ctx context.Context, batchID string, rememberSession bool) (int, error)

	// CancelBatch DENIES every row still parked under batchID and
	// returns how many it resolved. This is the dismissal / window-close
	// path (FR-003). Dismissal is deny-all, never allow-all.
	CancelBatch(ctx context.Context, batchID, reason string) (int, error)

	// ListPending returns the rows currently parked, so the frontend can
	// re-render a modal that was open before a reload or a dropped
	// connection. sessionID scopes the result; empty returns every
	// session's rows.
	ListPending(ctx context.Context, sessionID string) ([]PendingConfirmation, error)
}

// API is the concrete ConfirmAPI. It is a thin adapter over the bus:
// every method is a translation of an RPC-shaped call into a bus
// operation, and the policy (what a grant means, what gets audited) lives
// with the dispatcher that owns the (session, server, tool) context.
type API struct {
	bus *toolloop.ConfirmBus
}

// Config holds construction parameters.
type Config struct {
	// Bus is the confirmation registry shared with the chat runner's
	// tool adapter. It MUST be the same instance — a second bus would
	// accept resolutions for rows nothing is waiting on.
	Bus *toolloop.ConfirmBus
}

// New constructs an API. A nil Bus yields a surface whose mutating
// methods return ErrBusUnavailable and whose ListPending returns an
// empty slice — an honest "nothing is wired" rather than a nil panic.
func New(cfg Config) *API {
	return &API{bus: cfg.Bus}
}

// Resolve implements ConfirmAPI.
func (a *API) Resolve(_ context.Context, sessionID, callID string, approved bool, reason string, rememberSession bool) error {
	if a == nil || a.bus == nil {
		return ErrBusUnavailable
	}
	if callID == "" {
		return ErrUnknownConfirmation
	}
	return a.bus.Resolve(sessionID, callID, toolloop.ConfirmDecision{
		Approved: approved,
		Reason:   reason,
		// Guarded here as well as in the dispatcher: a "remember" flag
		// riding a denial should never reach the grant cache, and the
		// cheapest place to be sure is the boundary the frontend calls.
		RememberSession: approved && rememberSession,
	})
}

// ResolveAlways implements ConfirmAPI.
func (a *API) ResolveAlways(_ context.Context, sessionID, callID, reason string) error {
	if a == nil || a.bus == nil {
		return ErrBusUnavailable
	}
	if callID == "" {
		return ErrUnknownConfirmation
	}
	return a.bus.Resolve(sessionID, callID, toolloop.ConfirmDecision{
		Approved: true,
		Reason:   reason,
		Persist:  true,
	})
}

// ApproveBatch implements ConfirmAPI.
func (a *API) ApproveBatch(_ context.Context, batchID string, rememberSession bool) (int, error) {
	if a == nil || a.bus == nil {
		return 0, ErrBusUnavailable
	}
	if batchID == "" {
		return 0, ErrUnknownConfirmation
	}
	n := a.bus.ResolveBatch(batchID, toolloop.ConfirmDecision{
		Approved:        true,
		Reason:          "approved all",
		RememberSession: rememberSession,
	})
	return n, nil
}

// CancelBatch implements ConfirmAPI.
func (a *API) CancelBatch(_ context.Context, batchID, reason string) (int, error) {
	if a == nil || a.bus == nil {
		return 0, ErrBusUnavailable
	}
	if batchID == "" {
		return 0, ErrUnknownConfirmation
	}
	if reason == "" {
		reason = "dismissed by user"
	}
	return a.bus.CancelBatch(batchID, reason), nil
}

// ListPending implements ConfirmAPI.
func (a *API) ListPending(_ context.Context, sessionID string) ([]PendingConfirmation, error) {
	if a == nil || a.bus == nil {
		return []PendingConfirmation{}, nil
	}
	var out []PendingConfirmation
	if sessionID == "" {
		out = a.bus.List()
	} else {
		out = a.bus.ListSession(sessionID)
	}
	if out == nil {
		return []PendingConfirmation{}, nil
	}
	return out, nil
}
