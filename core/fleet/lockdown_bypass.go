package fleet

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// lockdownBypassEnvVar is the env var that operators can set to allow the
// harness to run even when a fleet lockdown is active. Its use is always
// audited via KindFleetLockdownBypassUsed.
const lockdownBypassEnvVar = "HARNESS_FLEET_LOCKDOWN_BYPASS"

// LockdownBypassed returns true when HARNESS_FLEET_LOCKDOWN_BYPASS=1 is set.
// The harness checks this before blocking any action on LockdownActive().
func LockdownBypassed() bool {
	v := os.Getenv(lockdownBypassEnvVar)
	return v == "1" || v == "true"
}

// AuditLockdownBypass emits KindFleetLockdownBypassUsed into the audit log
// when the bypass env var is set. Must be called once at process start
// (before any user-facing surface mounts) so every run that carries the
// bypass flag is traceable.
//
// em may be nil in which case the function is a no-op (test / fleet-disabled
// paths where no audit emitter is wired).
func AuditLockdownBypass(ctx context.Context, em audit.Emitter) {
	if !LockdownBypassed() {
		return
	}
	log.Printf("fleet.lockdown: HARNESS_FLEET_LOCKDOWN_BYPASS is set — bypass active; auditing")
	if err := audit.Emit(ctx, em, audit.KindFleetLockdownBypassUsed,
		audit.FleetLockdownBypassPayload{EnvVar: lockdownBypassEnvVar},
		time.Now().UTC(),
	); err != nil {
		log.Printf("fleet.lockdown: bypass audit emit failed: %v", err)
	}
}
