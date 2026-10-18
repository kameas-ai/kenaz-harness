package blockedrequests

import (
	"context"
	"errors"
	"fmt"
	"time"

	storepkg "github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// ErrStoreUnavailable is returned when no storepkg.Store is wired.
var ErrStoreUnavailable = errors.New("blockedrequests: store unavailable")

// ErrNotFound is returned when the requested row does not exist.
var ErrNotFound = errors.New("blockedrequests: not found")

// ErrAlreadyResolved is returned when Grant or Dismiss is called on a
// row that is not status="pending".
var ErrAlreadyResolved = errors.New("blockedrequests: already resolved")

// ErrUnsupportedFamily is returned by Grant for any family other than
// "fs" — see BlockedRequestsAPI.Grant's doc for why.
var ErrUnsupportedFamily = errors.New("blockedrequests: grant is only implemented for family=fs")

// Config bundles the dependencies the impl needs.
type Config struct {
	// Store is the durable backing store. nil causes every method to
	// return ErrStoreUnavailable (ListPending returns an empty slice
	// instead, matching scheduledchat's graceful-empty convention).
	Store storepkg.Store
	// CedarPolicy writes the Grant snippet through the same
	// WritePolicySnippet path (write-to-tmp-then-rename, best-effort
	// engine reload) every other persisted grant in the tree uses. nil
	// makes Grant return ErrStoreUnavailable — a grant that only flips
	// the row's status without ever writing a policy would tell the user
	// their write is now permitted when it is not, the exact
	// docs/unwired-ledger.md:934-940 lie AC-008 exists to prevent.
	CedarPolicy cedarpolicy.CedarPolicyAPI
	// Now overrides time.Now for tests. nil uses time.Now.
	Now func() time.Time
}

// API is the concrete BlockedRequestsAPI.
type API struct {
	cfg Config
}

// New returns a BlockedRequestsAPI backed by cfg.
func New(cfg Config) *API {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &API{cfg: cfg}
}

// ListPending implements BlockedRequestsAPI.
func (a *API) ListPending(ctx context.Context) ([]PendingRequest, error) {
	if a.cfg.Store == nil {
		return nil, nil
	}
	recs, err := a.cfg.Store.ListByStatus(ctx, storepkg.StatusPending)
	if err != nil {
		return nil, fmt.Errorf("blockedrequests: list pending: %w", err)
	}
	out := make([]PendingRequest, 0, len(recs))
	for _, r := range recs {
		out = append(out, pendingRequestFromRecord(r))
	}
	return out, nil
}

// Grant implements BlockedRequestsAPI.
func (a *API) Grant(ctx context.Context, id string) error {
	if a.cfg.Store == nil {
		return ErrStoreUnavailable
	}
	rec, err := a.cfg.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, storepkg.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("blockedrequests: grant get: %w", err)
	}
	if rec.Status != storepkg.StatusPending {
		return ErrAlreadyResolved
	}
	if rec.Family != "fs" {
		return ErrUnsupportedFamily
	}
	if a.cfg.CedarPolicy == nil {
		return ErrStoreUnavailable
	}

	op := corefs.OpRead
	if rec.Action == "write_filesystem" {
		op = corefs.OpWrite
	}
	filename, body := corefs.BuildFilesystemAllowSnippet(op, rec.Resource)
	if err := a.cfg.CedarPolicy.WritePolicySnippet(ctx, filename, body); err != nil {
		return fmt.Errorf("blockedrequests: write policy snippet: %w", err)
	}

	if err := a.cfg.Store.SetStatus(ctx, id, storepkg.StatusGranted, a.cfg.Now().UTC()); err != nil {
		if errors.Is(err, storepkg.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("blockedrequests: grant set-status: %w", err)
	}
	return nil
}

// Dismiss implements BlockedRequestsAPI.
func (a *API) Dismiss(ctx context.Context, id string) error {
	if a.cfg.Store == nil {
		return ErrStoreUnavailable
	}
	rec, err := a.cfg.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, storepkg.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("blockedrequests: dismiss get: %w", err)
	}
	if rec.Status != storepkg.StatusPending {
		return ErrAlreadyResolved
	}
	if err := a.cfg.Store.SetStatus(ctx, id, storepkg.StatusDismissed, a.cfg.Now().UTC()); err != nil {
		if errors.Is(err, storepkg.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("blockedrequests: dismiss set-status: %w", err)
	}
	return nil
}

func pendingRequestFromRecord(r storepkg.Record) PendingRequest {
	p := PendingRequest{
		ID:        r.ID,
		Origin:    r.Origin,
		OriginID:  r.OriginID,
		SessionID: r.SessionID,
		Family:    r.Family,
		Action:    r.Action,
		Resource:  r.Resource,
		Reason:    r.Reason,
		Status:    r.Status,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.ResolvedAt != nil {
		p.ResolvedAt = r.ResolvedAt.UTC().Format(time.RFC3339)
	}
	return p
}

// Compile-time interface check.
var _ BlockedRequestsAPI = (*API)(nil)
