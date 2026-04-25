package event

import "errors"

// Sentinel errors form the package-level taxonomy. Tests and consumers
// classify via errors.Is or via the Is* helpers below.
var (
	// ErrChainBroken is returned when a chain walk detects a
	// payload_hash mismatch or a prev_hash linkage that does not match
	// the prior event's payload_hash.
	ErrChainBroken = errors.New("event: hash chain broken")

	// ErrRedactionBypassed is returned when the redaction pipeline
	// could not run (panic, nil pipeline, etc.). Per C-004 the append
	// path aborts with this error rather than persisting plaintext.
	ErrRedactionBypassed = errors.New("event: redaction pipeline bypassed")

	// ErrUnknownEmitter is returned when an EmitterID has a prefix not
	// in the namespace allowlist (FR-017, plan §5.6).
	ErrUnknownEmitter = errors.New("event: unknown emitter prefix")

	// ErrInvalidKind is returned when a Kind fails well-formedness
	// validation (FR-002 grammar rules in WP02).
	ErrInvalidKind = errors.New("event: invalid kind")

	// ErrTruncated is returned when a chain head reference points to
	// an event id absent from the events table (legitimate truncation
	// or restored-from-backup tail loss; not a tamper).
	ErrTruncated = errors.New("event: chain truncated")

	// ErrAppendOnly is returned when a caller attempts a mutation
	// path that is not exposed (defense-in-depth; the public API has
	// no mutation methods, so this is an invariant guard).
	ErrAppendOnly = errors.New("event: append-only invariant")

	// ErrPolicyViolation is returned when policy refuses an action,
	// e.g. opening a Raw replay cursor without operator authorization.
	ErrPolicyViolation = errors.New("event: policy violation")

	// ErrSessionNotFound is returned when a session id has no events.
	ErrSessionNotFound = errors.New("event: session not found")

	// ErrAlreadyArchived is returned when a query targets a session
	// whose events have moved to an archive. The wrapping error carries
	// the ArchiveRef.
	ErrAlreadyArchived = errors.New("event: session already archived")

	// ErrInvalidQuery is returned when a Reader.Search query string
	// is malformed.
	ErrInvalidQuery = errors.New("event: invalid query")

	// ErrInvalidArgument is returned for caller-input problems that
	// don't fit a more specific sentinel.
	ErrInvalidArgument = errors.New("event: invalid argument")
)

// IsAppendOnlyViolation reports whether err indicates a forbidden
// mutation path was attempted.
func IsAppendOnlyViolation(err error) bool { return errors.Is(err, ErrAppendOnly) }

// IsChainBroken reports whether err indicates a hash-chain integrity
// failure detected during verification.
func IsChainBroken(err error) bool { return errors.Is(err, ErrChainBroken) }

// IsRedactionBypassed reports whether err indicates the pipeline did
// not run on a payload (which means the payload was NOT persisted).
func IsRedactionBypassed(err error) bool { return errors.Is(err, ErrRedactionBypassed) }

// IsUnknownEmitter reports whether err indicates an emitter id with a
// non-allowlisted prefix.
func IsUnknownEmitter(err error) bool { return errors.Is(err, ErrUnknownEmitter) }

// IsInvalidKind reports whether err indicates a malformed kind.
func IsInvalidKind(err error) bool { return errors.Is(err, ErrInvalidKind) }

// IsTruncated reports whether err indicates a clean chain truncation
// (not a tamper).
func IsTruncated(err error) bool { return errors.Is(err, ErrTruncated) }

// IsPolicyViolation reports whether err indicates a policy refusal.
func IsPolicyViolation(err error) bool { return errors.Is(err, ErrPolicyViolation) }

// IsSessionNotFound reports whether err indicates an unknown session.
func IsSessionNotFound(err error) bool { return errors.Is(err, ErrSessionNotFound) }

// IsAlreadyArchived reports whether err indicates an archived session.
func IsAlreadyArchived(err error) bool { return errors.Is(err, ErrAlreadyArchived) }

// ArchivedError wraps ErrAlreadyArchived with the archive reference so
// callers do not need to reach into the error to discover where the
// session moved to.
type ArchivedError struct {
	SessionID ULID
	Ref       ArchiveRef
}

// Error implements error.
func (e *ArchivedError) Error() string {
	return "event: session " + string(e.SessionID) + " already archived to " + e.Ref.Path
}

// Unwrap implements errors.Unwrap so errors.Is(err, ErrAlreadyArchived)
// returns true.
func (e *ArchivedError) Unwrap() error { return ErrAlreadyArchived }
