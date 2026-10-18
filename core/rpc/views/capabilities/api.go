// Package capabilities is the view-scoped RPC surface over the one install
// framework (install-framework-01DOGF0B): the Capability_* bindings the
// shared "Add capability" list/detail UI reads, plus the per-kind providers
// that plug the existing install paths into install.Framework.
//
// The package is fleet-free (scripts/ci/check-no-fleet-imports.sh): a
// provider that needs the fleet catalog receives it as an injected seam
// from core/rpc.
package capabilities

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/install"
)

// API is the Capability_* surface. It holds the framework every per-kind
// install binding (Tools_InstallRecipe, Slashcmd_SkillInstall,
// Workflows_CatalogInstall, …) also routes through, so the generic list
// and the per-kind flows share one pipeline, one verifier and one event.
type API struct {
	fw *install.Framework
}

// New wraps fw. A nil fw yields a surface whose every call reports
// ErrUnavailable (test chassis without a broker).
func New(fw *install.Framework) *API { return &API{fw: fw} }

// ErrUnavailable is returned when no framework is wired.
var ErrUnavailable = errors.New("capabilities: install framework not wired")

// Framework exposes the framework to the per-kind bindings in core/rpc.
func (a *API) Framework() *install.Framework {
	if a == nil {
		return nil
	}
	return a.fw
}

// List returns every registered provider's items (consumer-derived state)
// and the sources that could not be listed, with reasons.
func (a *API) List(ctx context.Context, filter install.Filter) (install.Listing, error) {
	if a == nil || a.fw == nil {
		return install.Listing{Items: []install.Item{}}, ErrUnavailable
	}
	return a.fw.List(ctx, filter)
}

// Install installs a zero-input item (one whose requirements are all
// satisfied) and returns its refreshed row. Items that need keys, OAuth or
// a directory go through their per-kind flow instead.
func (a *API) Install(ctx context.Context, kind, id, version string) (install.Item, error) {
	if a == nil || a.fw == nil {
		return install.Item{}, ErrUnavailable
	}
	res, err := a.fw.Install(ctx, install.Ref{Kind: install.Kind(kind), ID: id, Version: version}, install.Inputs{})
	if err != nil {
		return install.Item{}, err
	}
	return a.rowAfter(ctx, res), nil
}

// Uninstall removes an item through its provider.
func (a *API) Uninstall(ctx context.Context, kind, id string) error {
	if a == nil || a.fw == nil {
		return ErrUnavailable
	}
	return a.fw.Uninstall(ctx, install.Kind(kind), id)
}

// Update installs the newest version of an installed item.
func (a *API) Update(ctx context.Context, kind, id string) (install.Item, error) {
	if a == nil || a.fw == nil {
		return install.Item{}, ErrUnavailable
	}
	res, err := a.fw.Update(ctx, install.Kind(kind), id)
	if err != nil {
		return install.Item{}, err
	}
	return a.rowAfter(ctx, res), nil
}

// rowAfter re-reads the installed item's row so the caller can repaint it
// without a full List. If the provider cannot produce the row (a fleet
// source went offline between install and read-back), the consumer state
// the framework already confirmed is returned on a minimal row.
func (a *API) rowAfter(ctx context.Context, res install.Result) install.Item {
	if it, err := a.fw.Detail(ctx, res.Ref.Kind, res.Ref.ID); err == nil {
		return it
	}
	return install.Item{Kind: res.Ref.Kind, ID: res.Ref.ID, Version: res.Ref.Version, Name: res.Ref.ID, State: res.State}
}
