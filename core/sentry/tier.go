package sentry

// Tier controls what crash / error data is transmitted.
type Tier string

const (
	// TierOff disables all Sentry transmission. A nopClient is active;
	// local crash reports (GenerateLocalReport) still work.
	TierOff Tier = "off"

	// TierAnonymous sends structured exception data + redacted breadcrumbs
	// with no user identifier. DSN must be configured by the operator.
	TierAnonymous Tier = "anonymous"

	// TierIdentified sends the same payload as TierAnonymous but attaches
	// the user's fleet identity as a Sentry user tag. Requires fleet login.
	TierIdentified Tier = "identified"
)

// ResolveTier computes the active tier from the stored settings value and
// the current fleet login state. If the requested tier is TierIdentified but
// the user is not logged in, it auto-downgrades to TierAnonymous.
func ResolveTier(storedTier string, fleetLoggedIn bool) Tier {
	switch Tier(storedTier) {
	case TierAnonymous:
		return TierAnonymous
	case TierIdentified:
		if !fleetLoggedIn {
			return TierAnonymous
		}
		return TierIdentified
	default:
		return TierOff
	}
}
