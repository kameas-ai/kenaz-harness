package peers

import (
	"context"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets/secret"
)

// staticBackend is a test-only secrets.Backend that resolves references
// from a static `kind|locator` -> bytes map. It exists here (rather than
// in core/secrets) so the test fixture stays scoped to acp/peers per
// DIRECTIVE_001; consumers building their own facades should adapt this
// pattern.
type staticBackend struct {
	values map[string][]byte
}

func newStaticBackend(values map[string][]byte) *staticBackend {
	return &staticBackend{values: values}
}

func (b *staticBackend) Kind() secrets.BackendKind { return "static-test" }

func (b *staticBackend) SupportedRefKinds() []ref.RefKind {
	return []ref.RefKind{ref.RefEnv, ref.RefKeychain, ref.RefFile}
}

func (b *staticBackend) Resolve(ctx context.Context, r ref.CredentialReference) (secret.Secret, error) {
	key := r.Kind.String() + "|" + r.Locator
	v, ok := b.values[key]
	if !ok {
		return nil, errors.New("static-test: not found: " + key)
	}
	buf := make([]byte, len(v))
	copy(buf, v)
	return secret.NewStdlibSecret(buf, r.ID(), r.ConsumerID), nil
}

func (b *staticBackend) Health(context.Context) registry.BackendHealth {
	return registry.BackendHealth{
		Status:      registry.HealthOK,
		Message:     "static test backend",
		LastChecked: time.Now(),
	}
}

var _ secrets.Backend = (*staticBackend)(nil)
