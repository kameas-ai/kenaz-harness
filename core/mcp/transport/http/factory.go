package http

import (
	"errors"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// ConnectionFactory implements transport.ConnectionFactory for the
// streamable-HTTP transport. It is constructed from a coremcp.ServerSpec
// whose Transport field is "http".
//
// Header substitution: HeadersTemplate values may contain ${ENV_VAR}
// tokens. The env map (coremcp.ServerSpec.Env) is expected to be fully
// resolved before reaching here — the recipe machinery resolves secrets
// from the keychain before calling ToServerSpec / constructing the factory.
//
// Request timeout: spec.RequestTimeoutMs overrides DefaultRequestTimeout
// when non-zero. The timeout is per POST round-trip.
type ConnectionFactory struct {
	spec   coremcp.ServerSpec
	logger ConnectionLogger
}

// NewConnectionFactory builds a ConnectionFactory for the given spec.
// Returns an error when spec.Transport != "http" or spec.URL is empty.
func NewConnectionFactory(spec coremcp.ServerSpec, logger ConnectionLogger) (*ConnectionFactory, error) {
	if spec.Transport != "http" {
		return nil, errors.New("http: factory requires transport=\"http\"")
	}
	if spec.URL == "" {
		return nil, errors.New("http: factory requires non-empty URL")
	}
	return &ConnectionFactory{spec: spec, logger: logger}, nil
}

// compile-time witness.
var _ transport.ConnectionFactory = (*ConnectionFactory)(nil)

// NewConnection returns a fresh, unopened Connection for id. The id
// parameter matches the transport.ConnectionFactory contract; the
// factory's spec already names the recipe, so id is used only to
// seed the Connection's identity in logs when the spec's Name is empty.
func (f *ConnectionFactory) NewConnection(id string) (transport.Connection, error) {
	// Resolve headers: substitute ${VAR} from spec.Env.
	resolved := SubstituteHeaders(f.spec.HeadersTemplate, f.spec.Env)

	// Derive per-request timeout from spec or use default.
	timeout := time.Duration(f.spec.RequestTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	url := f.spec.URL
	if url == "" {
		return nil, errors.New("http: factory spec has empty URL")
	}

	return NewConnection(url, resolved, timeout, f.logger), nil
}
