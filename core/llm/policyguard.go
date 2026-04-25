package llm

import "context"

// PolicyGuard is the connector's call site for the (separately developed)
// policy engine. The connector itself ships a no-op default; the policy
// engine mission replaces it with a real binding (plan §6.4 / R10).
type PolicyGuard interface {
	Allow(ctx context.Context, req GenerationRequest, prof ProviderProfile) error
}

// AllowAllGuard is the no-op default. It permits every request and
// never inspects credentials. The real engine, when wired, returns
// ErrPolicyDenied for refused requests.
type AllowAllGuard struct{}

// Allow always returns nil.
func (AllowAllGuard) Allow(_ context.Context, _ GenerationRequest, _ ProviderProfile) error {
	return nil
}
