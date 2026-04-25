package redact

import (
	"fmt"
)

// Policy configures the redaction pipeline.
//
// Refusing enabled=false: per C-004 / FR-005 the pipeline is
// non-bypassable. Setting Enabled to false at load time is rejected.
type Policy struct {
	// Enabled MUST be true. Provided so policy files can be explicit;
	// loaders refuse "false" (ErrPipelineDisabled).
	Enabled bool `json:"enabled" yaml:"enabled"`

	// SaltRef is the secrets-keychain reference used to resolve the
	// HMAC salt at process start.
	SaltRef CredentialReference `json:"salt_ref" yaml:"salt_ref"`

	// FieldPaths lists JSON paths whose values are unconditionally
	// redacted regardless of substring patterns. Paths use dot
	// notation: "request.headers.authorization", "args.password".
	FieldPaths []string `json:"field_paths" yaml:"field_paths"`

	// ExtraMatchers lists operator-supplied custom regex matchers.
	// They are applied in addition to the built-in catalog.
	ExtraMatchers []Matcher `json:"-" yaml:"-"`
}

// Validate enforces non-bypassable invariants on policy.
func (p Policy) Validate() error {
	if !p.Enabled {
		return ErrPipelineDisabled
	}
	if p.SaltRef.Keychain == "" {
		return fmt.Errorf("%w: salt_ref.keychain required", ErrInvalidPolicy)
	}
	return nil
}

// DefaultPolicy returns a Policy seeded with the conventional
// keychain id and built-in field paths the spec calls out as
// "operator-marked sensitive."
func DefaultPolicy() Policy {
	return Policy{
		Enabled: true,
		SaltRef: CredentialReference{Keychain: "event-log-redaction-salt"},
		FieldPaths: []string{
			"authorization",
			"headers.authorization",
			"request.headers.authorization",
			"password",
			"secret",
			"token",
			"api_key",
		},
	}
}
