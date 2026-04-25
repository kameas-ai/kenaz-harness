// Package policy defines the PolicyAPI view-scoped accessor and the
// Denial type that <DenialNotice> renders uniformly per FR-012 / SC-009.
package policy

import "context"

// Denial is the typed shape returned by Explain when a policy denies an
// action. Renders through <DenialNotice>.
type Denial struct {
	PolicyID       string `json:"policyId"`
	ClauseID       string `json:"clauseId"`
	ViolatingInput string `json:"violatingInput"`
	Remediation    string `json:"remediation"`
}

// PolicyAPI is the view-scoped accessor for policy decisions and their
// explanations.
type PolicyAPI interface {
	Explain(ctx context.Context, input map[string]any) (Denial, error)
	StartStream(ctx context.Context) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
