package settings

import (
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// purgeFleetCredentialsForSignOut removes every Fleet-delivered provider key
// (org/team and synced user keys) the signed-in account stored on this OS
// account (provider-credentials-client-01PCCLI01 WP04, spec B2 "purge on
// sign-out"). It must run BEFORE the tokens are cleared: the token is what
// says whose keys they are. Local keys and other accounts' keys are never
// touched. Best-effort: sign-out proceeds regardless.
func purgeFleetCredentialsForSignOut(reason string) {
	p, err := fleet.CredentialPrincipal()
	if err != nil {
		return // nobody signed in: nothing of theirs to purge
	}
	n, err := secrets.PurgeFleetCredentialsForSignOut(paths.KeychainNamespace(), p, nil)
	if err != nil {
		logging.L().Warn("fleet.credentials.purge_partial", "reason", reason, "removed", n, "err", err.Error())
		return
	}
	if n > 0 {
		logging.L().Info("fleet.credentials.purged", "reason", reason, "removed", n)
	}
}

// ReconcileFleetCredentialsOnSessionReset is the OnFleetSessionReset hook
// for Fleet-delivered provider keys: after a sign-in it purges every other
// account's keys and, when the same user is now in a different org, the old
// org's delivered keys (spec B2 "OnFleetSessionReset to a different org").
// After a sign-out there is no principal and it does nothing (sign-out
// already purged).
func ReconcileFleetCredentialsOnSessionReset() {
	p, err := fleet.CredentialPrincipal()
	if err != nil {
		return
	}
	n, err := secrets.ReconcileFleetCredentialPrincipal(paths.KeychainNamespace(), p, nil)
	if err != nil {
		logging.L().Warn("fleet.credentials.reconcile_partial", "removed", n, "err", err.Error())
		return
	}
	if n > 0 {
		logging.L().Info("fleet.credentials.reconciled", "removed", n)
	}
}
