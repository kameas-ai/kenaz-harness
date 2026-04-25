package db

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MinSQLiteVersion is the floor enforced by Open per research D5: the
// Windows NTFS WAL close() lock bug is fixed in SQLite 3.51.0 (2025-11-04).
const MinSQLiteVersion = "3.51.0"

// ErrSQLiteVersionTooOld mirrors storage.ErrSQLiteVersionTooOld so this
// package can return it without importing the parent.
var ErrSQLiteVersionTooOld = errors.New("storage: SQLite version below 3.51.0 floor")

// SemVer represents a "major.minor.patch" SQLite-version triple.
type SemVer struct {
	Major, Minor, Patch int
}

// ParseSemVer parses "major.minor.patch" with optional leading "v".
// Trailing text (e.g. "3.51.0-something") is ignored after the patch.
func ParseSemVer(s string) (SemVer, error) {
	t := strings.TrimPrefix(s, "v")
	// trim a "-suffix" if present
	if i := strings.IndexByte(t, '-'); i >= 0 {
		t = t[:i]
	}
	parts := strings.SplitN(t, ".", 3)
	if len(parts) != 3 {
		return SemVer{}, fmt.Errorf("semver: %q is not major.minor.patch", s)
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return SemVer{}, fmt.Errorf("semver: major %q: %w", parts[0], err)
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return SemVer{}, fmt.Errorf("semver: minor %q: %w", parts[1], err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return SemVer{}, fmt.Errorf("semver: patch %q: %w", parts[2], err)
	}
	return SemVer{Major: maj, Minor: min, Patch: patch}, nil
}

// LessThan reports whether v is strictly less than other.
func (v SemVer) LessThan(other SemVer) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// CheckSQLiteVersion returns ErrSQLiteVersionTooOld wrapping the
// detected version when version < 3.51.0.
func CheckSQLiteVersion(detected string) error {
	v, err := ParseSemVer(detected)
	if err != nil {
		return fmt.Errorf("check sqlite version: %w", err)
	}
	floor, _ := ParseSemVer(MinSQLiteVersion)
	if v.LessThan(floor) {
		return fmt.Errorf("%w: detected %s, floor %s",
			ErrSQLiteVersionTooOld, detected, MinSQLiteVersion)
	}
	return nil
}
