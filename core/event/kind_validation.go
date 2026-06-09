package event

import (
	"errors"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/event/kind"
)

// ValidateKind is the public-API mirror of kind.Validate. It rewrites
// kind.ErrInvalid as ErrInvalidKind so callers can classify with the
// package-level taxonomy.
func ValidateKind(k Kind) error {
	if err := kind.Validate(kind.Kind(k)); err != nil {
		if errors.Is(err, kind.ErrInvalid) {
			return fmt.Errorf("%w: %s", ErrInvalidKind, err.Error())
		}
		return err
	}
	return nil
}

// IsRegisteredKind reports whether k is in the process-local kind
// registry. Unknown but well-formed kinds are still accepted by
// Append (NFR-006); IsRegisteredKind is exposed for tooling that
// wants to surface "this kind is novel" warnings.
func IsRegisteredKind(k Kind) bool {
	return kind.IsRegistered(kind.Kind(k))
}

// RegisterKind adds k to the process-local kind registry.
func RegisterKind(k Kind) error {
	if err := kind.Register(kind.Kind(k)); err != nil {
		if errors.Is(err, kind.ErrInvalid) {
			return fmt.Errorf("%w: %s", ErrInvalidKind, err.Error())
		}
		return err
	}
	return nil
}
