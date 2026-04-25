package event

import (
	"fmt"
	"time"

	"kaneaz-harness/core/event/internal/idgen"
)

// NewULID returns a fresh process-monotonic ULID.
func NewULID() (ULID, error) {
	s, err := idgen.Next()
	if err != nil {
		return "", fmt.Errorf("event: ulid: %w", err)
	}
	return ULID(s), nil
}

// ParseULID validates s as a ULID. Returns the parsed ULID on success.
func ParseULID(s string) (ULID, error) {
	if _, err := idgen.Parse(s); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidArgument, err.Error())
	}
	return ULID(s), nil
}

// Time returns the wall-clock millisecond floor encoded in u.
func (u ULID) Time() (time.Time, error) {
	raw, err := idgen.Parse(string(u))
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(int64(idgen.TimestampMillis(raw))), nil
}

// MustParseULID panics on invalid input. Tests only.
func MustParseULID(s string) ULID {
	u, err := ParseULID(s)
	if err != nil {
		panic(err)
	}
	return u
}
