package mlsidecar

import (
	"context"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// UpdateResult reports what Update did.
type UpdateResult struct {
	Install           InstallResult
	Flipped           bool
	ShutdownRequested bool
	// ShutdownErr is non-fatal: a failed shutdown request leaves the OLD
	// process serving (design §3.7 R6: "a failed swap leaves the old
	// version serving with a visible 'update pending' badge — old-but-
	// correct beats absent"). Update itself does not fail on this —
	// Flipped is already true by the time ShutdownErr could be set.
	ShutdownErr error
}

// Update implements design §3.5/§3.7 R6's flip-and-respawn: verify +
// stage the new engine into versions/, atomically rename-swap `current`
// (via Install), then request a graceful, TOKEN-AUTHORIZED stop of
// whatever is currently running. It never execs the new binary directly
// and never asks the currently-running process to drain-and-observe
// anything — the next health poll (from this client or another) notices
// the version changed (via a fresh EvaluateAdoption / spawn cycle,
// Manager.Reconcile) and respawns from `current` under the spawn lock.
//
// A failure staging or verifying the new version leaves the OLD version
// fully installed and serving: Install (which Update delegates to)
// never touches `current` before the new artifact passes verification.
func Update(ctx context.Context, layout Layout, registry channels.Registry, creds secrets.ResolverAPI, verifier Verifier, client *Client, req InstallRequest) UpdateResult {
	res, err := Install(ctx, layout, registry, creds, verifier, req)
	if err != nil {
		logging.L().Warn("mlsidecar.update.install_failed", "version", req.Version, "err", err.Error())
		return UpdateResult{ShutdownErr: fmt.Errorf("install: %w", err)}
	}
	out := UpdateResult{Install: res, Flipped: true}

	token, ok, terr := ReadLocalToken(layout)
	if terr != nil || !ok {
		out.ShutdownErr = fmt.Errorf("mlsidecar: no local shutdown token available (terr=%v)", terr)
		logging.L().Info("mlsidecar.update.no_token", "version", req.Version)
		return out
	}
	if client == nil {
		out.ShutdownErr = fmt.Errorf("mlsidecar: no client configured to request shutdown")
		return out
	}
	if err := client.Shutdown(ctx, token); err != nil {
		out.ShutdownErr = err
		logging.L().Info("mlsidecar.update.shutdown_request_failed", "version", req.Version, "err", err.Error())
		return out
	}
	out.ShutdownRequested = true
	logging.L().Info("mlsidecar.update.done", "version", req.Version)
	return out
}
