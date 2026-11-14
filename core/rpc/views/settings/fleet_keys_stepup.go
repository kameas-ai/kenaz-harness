package settings

// fleet_keys_stepup.go — "Sign in again to update this device's keys"
// (kenaz-fleet ER-40; core/fleet/device_keys_stepup.go).
//
// Under FLEET_NODE_KEYS_STEP_UP=enforce Fleet refuses to REPLACE an active
// device key without a fresh interactive sign-in (PUT keys → 403
// reauth_required; enroll → keys_reauth_required). The client records that
// as KeyRegistration{Status: reauth_required}; the session snapshot carries
// it to Settings → Account and the shared-sessions inbox, which offer
// "Sign in again". That button lands here:
//
//  1. a max_age=0, prompt=login re-authentication in the browser
//     (cancellable with FleetSignInCancel);
//  2. its ID token is held in memory only (never the keychain);
//  3. the key registration is retried exactly ONCE, carrying the token;
//  4. the token is dropped, whatever the outcome.
//
// Nothing here starts a browser on its own: only the user's click does, so
// a refusal can never loop. A cancel or failure leaves the reauth_required
// state in the snapshot (visible, recoverable), never silently cleared.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// keysStepUpCall is one in-flight step-up that concurrent callers join.
type keysStepUpCall struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// FleetDeviceKeysReauth implements the API method (see api.go).
func (a *API) FleetDeviceKeysReauth(ctx context.Context) error {
	if a == nil || a.fleet == nil {
		return fleet.ErrFleetDisabled
	}
	a.fleet.mu.Lock()
	if call := a.fleet.keysStepUp; call != nil {
		a.fleet.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Own context: FleetSignInCancel (the UI's Cancel) stops it, not
	// whichever caller's context ends first.
	flowCtx, cancel := context.WithCancel(context.Background())
	call := &keysStepUpCall{done: make(chan struct{}), cancel: cancel}
	a.fleet.keysStepUp = call
	a.fleet.mu.Unlock()

	err := a.runKeysStepUp(flowCtx)
	cancel()

	a.fleet.mu.Lock()
	a.fleet.keysStepUp = nil
	a.fleet.mu.Unlock()
	call.err = err
	close(call.done)
	a.publishFleetSession("device_keys_step_up")
	return err
}

func (a *API) runKeysStepUp(ctx context.Context) error {
	c := a.fleetClient()
	if c == nil || c.IsNop() || fleet.Disabled() {
		return fleet.ErrFleetDisabled
	}
	profile := fleet.ResolveProfile()
	if !profile.Configured() {
		return fleet.ErrProfileNotConfigured
	}
	if ok, _ := c.SignedIn(ctx); !ok {
		return fleet.ErrNotSignedIn
	}
	a.fleet.mu.RLock()
	flow := a.fleet.stepUpFlow
	a.fleet.mu.RUnlock()
	if flow == nil {
		flow = fleet.StepUpFlow
	}
	logging.L().Info("fleet.rpc.keys_step_up.start")
	idToken, err := flow(ctx, profile)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// The user pressed Cancel: an outcome, not a failure. The
			// reauth_required state stays on the snapshot.
			logging.L().Info("fleet.rpc.keys_step_up.cancelled")
			return err
		}
		logging.L().Warn("fleet.rpc.keys_step_up.flow_failed", "err", err.Error())
		return err
	}
	// Whatever happens next, the token lives no longer than this call.
	defer c.ClearStepUpToken()
	if err := c.HoldStepUpToken(idToken); err != nil {
		logging.L().Warn("fleet.rpc.keys_step_up.token_rejected", "err", err.Error())
		return err
	}

	nodeID := fleet.ReadNodeID(a.fleetDataDir())
	if nodeID == "" {
		// Never enrolled on this install: enroll registers the keys and
		// carries the held token.
		_, err := a.fleetEnroll(ctx)
		return err
	}
	// One retry, never more: a second refusal returns
	// ErrKeysReauthRequired and leaves the state visible.
	err = c.RegisterDeviceKeys(ctx, nodeID)
	switch {
	case err == nil:
		logging.L().Info("fleet.rpc.keys_step_up.keys_updated")
	case errors.Is(err, fleet.ErrNodeRemoved):
		logging.L().Warn("fleet.rpc.keys_step_up.node_removed")
		a.handleNodeRemoved()
	default:
		logging.L().Warn("fleet.rpc.keys_step_up.retry_failed", "err", err.Error())
	}
	return err
}

// FleetDeviceKeysChanged republishes the session snapshot after a key
// registration that ran outside enroll (PUT keys after a recovery-code
// import), so a reauth_required outcome reaches the UI at once.
func (a *API) FleetDeviceKeysChanged() {
	a.publishFleetSession("device_keys_changed")
}
