package lockfile

import (
	"fmt"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
)

// ResolveConflicts is the entry point for the deferred
// `kaneaz lock --resolve-conflicts` subcommand. The full implementation
// re-runs resolution to merge two lockfiles whose [[bundle]] sets diverge
// (the typical pnpm-style merge-conflict scenario in CI).
//
// In v1.0 it returns bundle.ErrNotImplementedV1x with an actionable
// pointer to the deferred command. The CLI surface presents this as:
//
//	$ kaneaz lock --resolve-conflicts
//	error: feature deferred to v1.x. To unblock now, regenerate the
//	  lockfile with `kaneaz lock --regenerate`.
//
// See plan §8 R3 for the rationale.
func ResolveConflicts(_ ...*Lockfile) error {
	return fmt.Errorf("%w: kaneaz lock --resolve-conflicts is planned for v1.x", bundle.ErrNotImplementedV1x)
}
