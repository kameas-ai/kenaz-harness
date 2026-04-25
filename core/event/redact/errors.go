package redact

import "errors"

// Sentinel errors local to the redact package; the public-facing
// surface in core/event re-exports the relevant taxonomy via
// errors.Is.
var (
	// ErrPipelineDisabled is returned when policy attempts
	// enabled=false. Per C-004 the pipeline is non-bypassable.
	ErrPipelineDisabled = errors.New("redact: policy refuses enabled=false")

	// ErrPipelinePanic is returned when a matcher panics. The append
	// path treats this as a non-bypassable failure (no plaintext
	// fallback).
	ErrPipelinePanic = errors.New("redact: pipeline panic")

	// ErrSaltNotResolved is returned when the HMAC salt cannot be
	// loaded from the resolver.
	ErrSaltNotResolved = errors.New("redact: salt not resolved")

	// ErrInvalidPolicy is returned for malformed policy input.
	ErrInvalidPolicy = errors.New("redact: invalid policy")
)
