// Package servedfleet wires fleet enrollment for the served entry points
// (main.go --serve and cmd/harness-served). It exists so the two cannot drift:
// both call Start. It lives outside core/ because it joins core/serve (which
// may not import core/fleet) to core/fleet.
package servedfleet

import (
	"context"
	"log/slog"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/serve"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

// Start installs the broker session as the fleet token source and runs the
// enroll supervisor until ctx ends. It returns nil (and does nothing) on a
// bare local run with no broker, which keeps keychain-backed sign-in.
func Start(ctx context.Context, api *rpc.API, cfg authbroker.Config, session *authbroker.Session, log *slog.Logger) *serve.FleetEnrollSupervisor {
	if cfg.BrokerAddr == "" || session == nil || api == nil {
		return nil
	}
	// The guest has no OS keychain and renewal is host-owned; no refresh
	// token crosses the boundary.
	fleet.SetExternalTokenSource(session.AccessToken)

	st := api.Settings()
	// A 401 from Fleet's OTLP receiver nudges an immediate broker renewal.
	st.SetFleetExportUnauthorizedHook(session.NotifyOn401)

	sup := serve.NewFleetEnrollSupervisor(serve.FleetEnrollConfig{
		Auth:         session,
		Identity:     identityKey,
		Enroll:       enrollFunc(st, identityKey),
		Reconcile:    st.RefreshTelemetryPreferences,
		SessionEnded: st.FleetSessionEnded,
		Log:          log,
	})
	go sup.Run(ctx)
	return sup
}

// identityKey is the account the current token asserts. A renewal keeps it
// stable; a different subject, org or issuer changes it.
func identityKey() string { return fleet.TokenIdentityKey() }

// enrollSettings is the slice of the settings API the enroll step needs.
type enrollSettings interface {
	FleetHostIdentityPresented(identity string) bool
	FleetRefreshIdentity(ctx context.Context) (settings.FleetIdentity, error)
}

// enrollFunc is the supervisor's Enroll step. Before each attempt it hands
// the host's current identity to the settings API, which lifts a durable
// node_removed block when that identity differs from the one removed: a
// served guest has no sign-in of its own, so a host presenting a new
// account is its re-authorization (unwired-ledger device-keys residual 7).
func enrollFunc(s enrollSettings, identity func() string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		s.FleetHostIdentityPresented(identity())
		_, err := s.FleetRefreshIdentity(ctx)
		return err
	}
}
