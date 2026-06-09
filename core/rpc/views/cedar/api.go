// Package cedar is the view-scoped RPC surface for the Team Cedar policy
// publish feature (fleet-share-and-sync-01NDFSEX14 WP07).
//
// This surface is separate from cedarpolicy (the local policy-panel view) —
// it owns only the fleet push path. Fork-removal recipe: delete this package
// + the "Publish to team" button on CedarEditor.vue + Cedar_PublishToTeam binding.
package cedar

import "context"

// CedarAPI is the RPC surface for Cedar team publishing.
type CedarAPI interface {
	// Cedar_PublishToTeam publishes a Cedar rule to the team via fleet.
	// The current user must have the policy_admin role; the server returns 403
	// otherwise and this method returns ErrCapabilityNotInTier.
	Cedar_PublishToTeam(ctx context.Context, ruleID, ruleSource string) error
}
