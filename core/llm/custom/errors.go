package custom

import "fmt"

// ErrInvalidConfig is returned when the adapter cannot proceed because of
// a missing or contradictory configuration (e.g. bearer auth with no key,
// unknown auth_scheme).
type ErrInvalidConfig struct {
	Reason string
}

func (e *ErrInvalidConfig) Error() string {
	return "custom-openai: invalid config: " + e.Reason
}

// ErrKeyAgainstNone is a sentinel returned when credential bytes are
// supplied against an auth_scheme:none template. This is informational —
// the adapter still proceeds, but an audit warning is emitted.
var ErrKeyAgainstNone = fmt.Errorf("custom-openai: API key supplied against auth_scheme:none template (key will be ignored)")
