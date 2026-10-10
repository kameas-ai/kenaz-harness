package settings

// Pending-approvals hub (ml-producer-01MLPRD01 WP06; kenaz-fleet
// docs/contract-pending-approvals.md, spec §12 A-13). Settings shows an
// "N items need your approval" issue in the settings banner and a
// step-through modal over the items; the Cloud ML panel renders the hub's
// ml_notice body_text verbatim and approves it through the hub.
//
// The frontend never holds an approve action. FleetApproveItem takes only an
// item id, re-reads the list, looks the item up and hands it to
// fleet.Client.ApprovePendingItem, which refuses any method + path outside
// the harness allowlist before sending anything.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// PendingApprovalView is one hub item as the UI renders it. The approve
// action itself is deliberately absent.
type PendingApprovalView struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	// BodyText is plain text, rendered verbatim as text (never HTML).
	BodyText string `json:"bodyText"`
	// DocumentURL is an absolute http(s) URL ("" when the item has none);
	// DocumentSHA256 pins the bytes Version means.
	DocumentURL    string `json:"documentUrl"`
	DocumentSHA256 string `json:"documentSha256"`
	Version        string `json:"version"`
	// Blocking says what is paused until this is approved.
	Blocking string `json:"blocking"`
	Required bool   `json:"required"`
	// ApproveAllowed is false when Fleet's approve action is outside the
	// harness allowlist: the item is shown but cannot be approved here.
	ApproveAllowed bool `json:"approveAllowed"`
}

// PendingApprovalsView is the hub as Settings renders it.
type PendingApprovalsView struct {
	// SignedIn: a live Fleet session exists. Nothing is shown otherwise.
	SignedIn bool `json:"signedIn"`
	// Available: this Fleet serves the hub (false on 404, an older Fleet).
	Available bool `json:"available"`
	// Items, in Fleet's order, minus ml_exclusions_change while anything
	// required is pending. Never null on the wire.
	Items []PendingApprovalView `json:"items"`
	// RequiredCount is how many Items are required (blocking).
	RequiredCount int `json:"requiredCount"`
	// Changed is set on the answer to an approval Fleet refused as stale
	// (409 policy_changed / 400 legal_acceptance_outdated) or whose item was
	// gone: Items is the fresh list, show it again.
	Changed bool `json:"changed,omitempty"`
	// FleetError is the last read error ("" on success or when unavailable).
	FleetError string `json:"fleetError,omitempty"`
}

// ErrApprovalsNotWired is returned by FleetApproveItem when Fleet is not
// wired or the user is signed out.
var ErrApprovalsNotWired = errors.New("settings: approvals are not available (fleet not wired or signed out)")

// approvalsView turns a hub read into the view: drops the informational
// ml_exclusions_change while anything required is pending (contract: shown
// "only when no required ML item is pending"; the harness applies the
// stricter "nothing required") and marks allowlisted actions.
func approvalsView(p fleet.PendingApprovals) PendingApprovalsView {
	v := PendingApprovalsView{SignedIn: true, Available: true, Items: []PendingApprovalView{}}
	anyRequired := false
	for _, it := range p.Items {
		if it.Required {
			anyRequired = true
		}
	}
	for _, it := range p.Items {
		if anyRequired && it.Kind == fleet.ApprovalKindMLExclusionsChange {
			continue
		}
		if it.Required {
			v.RequiredCount++
		}
		v.Items = append(v.Items, PendingApprovalView{
			ID: it.ID, Kind: it.Kind, Title: it.Title, Summary: it.Summary,
			BodyText: it.BodyText, DocumentURL: it.DocumentURL, DocumentSHA256: it.DocumentSHA256,
			Version: it.Version, Blocking: it.Blocking, Required: it.Required,
			ApproveAllowed: fleet.ApproveActionAllowed(it.Approve.Method, it.Approve.Path),
		})
	}
	return v
}

// approvalsClient returns the fleet client when signed in.
func (a *API) approvalsClient(ctx context.Context) (*fleet.Client, bool) {
	if fleet.Disabled() {
		return nil, false
	}
	c := a.fleetClient()
	if c == nil || c.IsNop() {
		return nil, false
	}
	if ok, err := c.SignedIn(ctx); err != nil || !ok {
		return nil, false
	}
	return c, true
}

// readApprovals reads the hub. The raw list is returned too so callers can
// resolve an id to its approve action.
func readApprovals(ctx context.Context, c *fleet.Client) (PendingApprovalsView, fleet.PendingApprovals) {
	p, err := c.GetPendingApprovals(ctx)
	switch {
	case errors.Is(err, fleet.ErrPendingApprovalsUnavailable):
		return PendingApprovalsView{SignedIn: true, Items: []PendingApprovalView{}}, fleet.PendingApprovals{}
	case err != nil:
		return PendingApprovalsView{SignedIn: true, Items: []PendingApprovalView{}, FleetError: err.Error()}, fleet.PendingApprovals{}
	}
	return approvalsView(p), p
}

// FleetPendingApprovals implements SettingsAPI: a fresh hub read.
func (a *API) FleetPendingApprovals(ctx context.Context) (PendingApprovalsView, error) {
	c, ok := a.approvalsClient(ctx)
	if !ok {
		return PendingApprovalsView{Items: []PendingApprovalView{}}, nil
	}
	v, _ := readApprovals(ctx, c)
	return v, nil
}

// FleetApproveItem implements SettingsAPI: approves the item with id. The
// approve action is looked up from a FRESH read (never taken from the
// caller) and only among the items the view shows. A stale or vanished item
// is not an error: the fresh list comes back with Changed set. An action
// outside the allowlist is fleet.ErrApproveActionNotAllowed (nothing sent).
func (a *API) FleetApproveItem(ctx context.Context, id string) (PendingApprovalsView, error) {
	c, ok := a.approvalsClient(ctx)
	if !ok {
		return PendingApprovalsView{Items: []PendingApprovalView{}}, ErrApprovalsNotWired
	}
	v, raw := readApprovals(ctx, c)
	if !v.Available {
		if v.FleetError != "" {
			return v, errors.New(v.FleetError)
		}
		return v, fleet.ErrPendingApprovalsUnavailable
	}
	shown := false
	for _, it := range v.Items {
		if it.ID == id {
			shown = true
			break
		}
	}
	item, found := raw.Find(id)
	if !shown || !found {
		v.Changed = true
		return v, nil
	}
	err := c.ApprovePendingItem(ctx, item)
	if errors.Is(err, fleet.ErrApprovalStale) {
		fresh, _ := readApprovals(ctx, c)
		fresh.Changed = true
		return fresh, nil
	}
	if err != nil {
		return v, err
	}
	fresh, _ := readApprovals(ctx, c)
	return fresh, nil
}

// hubMLNotice returns the hub's ml_notice item when the hub is served and
// lists one the harness may approve. ok is false otherwise (older Fleet,
// read error, no such item), and the caller keeps its pre-hub notice path.
func hubMLNotice(ctx context.Context, c *fleet.Client) (PendingApprovalView, bool) {
	v, _ := readApprovals(ctx, c)
	if !v.Available {
		return PendingApprovalView{}, false
	}
	for _, it := range v.Items {
		if it.Kind == fleet.ApprovalKindMLNotice && it.ApproveAllowed {
			return it, true
		}
	}
	return PendingApprovalView{}, false
}
