// Package logs defines LogsAPI — the view-scoped accessor for the in-app
// runtime log surface (mission 01NLOGS01). Backed by core/logstore.Store;
// the binding layer wraps it with sentry.WrapBinding.
//
// Security contract: rows returned here are already redacted (see
// core/logstore.RedactMessage). No raw secret bytes ever cross this
// boundary.
package logs

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/logstore"
)

// Row is the wire-safe log row returned to the frontend. Identical to
// logstore.Row; re-exported so the view package is self-contained and the
// frontend only imports the view's type, not core/logstore directly.
type Row = logstore.Row

// Filter is the wire-safe filter struct passed from the frontend.
type Filter = logstore.Filter

// Level mirrors logstore.Level for callers that want to name a level.
type Level = logstore.Level

// LogsAPI is the read-only RPC surface for in-app runtime logs.
//
// Wails-bound methods are added in core/rpc/bindings.go with the
// naming convention Logs_<Operation>. The frontend's LogsClient
// delegates to them.
type LogsAPI interface {
	// Tail returns up to limit rows (0 = all) from the ring buffer,
	// newest first, filtered by f. This is the primary "follow-tail"
	// path for the Logs panel.
	Tail(ctx context.Context, f Filter) ([]Row, error)
}

// API is the concrete LogsAPI implementation backed by a logstore.Store.
type API struct {
	store *logstore.Store
}

// New returns an API backed by store.
func New(store *logstore.Store) *API {
	return &API{store: store}
}

// Tail satisfies LogsAPI. Always returns a non-nil slice.
func (a *API) Tail(ctx context.Context, f Filter) ([]Row, error) {
	rows := a.store.List(f)
	if rows == nil {
		rows = []Row{}
	}
	return rows, nil
}
