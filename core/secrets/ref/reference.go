// Package ref defines the indirect credential reference type that flows
// through configuration, lockfiles, and RPC payloads in place of any
// resolved credential value.
//
// A CredentialReference is a (Kind, Locator, ConsumerID) triple. Resolution
// is the sole responsibility of a Backend; the reference itself is opaque
// metadata that names a place — never a credential value.
//
// The package is deliberately tiny and dependency-free: every other
// secrets subpackage and every backend depends on it, and DIRECTIVE_001
// requires that no backend SDK ever land in this layer.
//
// Spec mapping:
//   - FR-001  Indirect-reference syntax
//   - FR-015  Refusal of inline plaintext
//   - FR-016  Per-reference scoping
//   - C-002   No plaintext credentials in any persisted state
package ref

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// RefKind identifies which backend resolves a CredentialReference.
//
// The set is closed and stable: parsers reject unknown kinds. New
// backends declare a new RefKind value here in coordination with the
// data model in `kitty-specs/secrets-keychain-01KQ1A3M/data-model.md`.
type RefKind int

const (
	// RefUnknown is the zero value. Resolution against an unknown kind
	// is always a typed error.
	RefUnknown RefKind = iota
	// RefEnv resolves through the environment-variable backend.
	RefEnv
	// RefKeychain resolves through the OS keychain backend.
	RefKeychain
	// RefFile resolves through the file backend.
	RefFile
	// RefAWSProfile resolves through the AWS credential chain.
	RefAWSProfile
	// RefAWSKMS resolves through AWS KMS via envelope encryption.
	RefAWSKMS
	// RefYubikeyPIV resolves through go-piv/piv-go.
	RefYubikeyPIV
	// RefPKCS11 resolves through a PKCS#11 token (deferred to v2 but
	// reserved here so the parser surface is stable).
	RefPKCS11
)

// String returns the canonical wire form of the kind. The wire forms are
// the keys that appear in configuration syntax.
func (k RefKind) String() string {
	switch k {
	case RefEnv:
		return "env"
	case RefKeychain:
		return "keychain"
	case RefFile:
		return "file"
	case RefAWSProfile:
		return "aws_profile"
	case RefAWSKMS:
		return "aws_kms"
	case RefYubikeyPIV:
		return "yubikey_piv"
	case RefPKCS11:
		return "pkcs11"
	default:
		return "unknown"
	}
}

// kindFromWire is the inverse of RefKind.String. Unknown wire forms
// produce RefUnknown; callers must convert that into a typed error.
func kindFromWire(s string) RefKind {
	switch s {
	case "env":
		return RefEnv
	case "keychain":
		return RefKeychain
	case "file":
		return RefFile
	case "aws_profile":
		return RefAWSProfile
	case "aws_kms":
		return RefAWSKMS
	case "yubikey_piv":
		return RefYubikeyPIV
	case "pkcs11":
		return RefPKCS11
	default:
		return RefUnknown
	}
}

// AllKinds returns the closed set of supported RefKind values, in
// declaration order. Callers iterating for completeness use this.
func AllKinds() []RefKind {
	return []RefKind{RefEnv, RefKeychain, RefFile, RefAWSProfile, RefAWSKMS, RefYubikeyPIV, RefPKCS11}
}

// CredentialReference is the indirect pointer that lives wherever a
// credential value would otherwise live. The field set is intentionally
// small; backends interpret Locator per their own schema.
//
// Required is operator policy, not parser policy: pre-flight uses it to
// decide whether an unresolvable reference fails startup or is recorded
// as `optional_unresolvable`. The default zero-value is "required" — see
// data-model.md.
type CredentialReference struct {
	// Kind selects the backend.
	Kind RefKind
	// Locator is the kind-specific addressing string (env var name,
	// keychain entry name, file path, AWS profile, KMS ARN, PIV slot
	// id, PKCS#11 URI). It is sensitive in multi-tenant audit contexts
	// and MUST NOT appear in event payloads or error messages — use
	// ID() instead.
	Locator string
	// ConsumerID names the intended consumer for FR-016 scoping. May
	// be empty.
	ConsumerID string
	// Optional marks references whose resolution failure should not
	// fail startup. Set explicitly by configuration; the zero value
	// means required.
	Optional bool
}

// ID returns a redaction-safe identifier for the reference. It is the
// hex-encoded SHA-256 of `kind|locator`, truncated to 16 hex chars
// (64 bits) for readability. The ID is stable across runs but reveals
// nothing about the locator.
//
// ID is the only reference identifier safe to include in event log
// payloads, error messages, or panic traces.
func (r CredentialReference) ID() string {
	h := sha256.Sum256([]byte(r.Kind.String() + "|" + r.Locator))
	return hex.EncodeToString(h[:8])
}

// String redacts the locator. It is safe for logging.
func (r CredentialReference) String() string {
	return fmt.Sprintf("ref(kind=%s,id=%s,consumer=%s)", r.Kind, r.ID(), r.ConsumerID)
}

// Errors returned by parsers. These are package-local sentinels in
// WP01; WP07 will replace them with the canonical error taxonomy in
// `core/secrets/errors`. Until then, the wrapping shape (`%w: ref=ID`)
// is what callers depend on.
var (
	// ErrUnknownKind is returned when configuration declares a kind
	// the parser does not recognise.
	ErrUnknownKind = errors.New("ref: unknown kind")
	// ErrEmptyLocator is returned when a kind is given but the
	// associated locator is empty.
	ErrEmptyLocator = errors.New("ref: empty locator")
	// ErrInlinePlaintext is returned when configuration appears to
	// inline a credential value rather than naming a reference.
	ErrInlinePlaintext = errors.New("ref: inline plaintext credential refused")
	// ErrAmbiguousReference is returned when more than one kind key
	// appears in the same reference object.
	ErrAmbiguousReference = errors.New("ref: ambiguous reference (multiple kinds set)")
	// ErrLocatorFormat is returned when a locator violates the
	// kind-specific shape (e.g. AWS KMS expects an ARN).
	ErrLocatorFormat = errors.New("ref: locator format invalid")
)

// inlinePlaintextHints are conservative regular expressions that match
// the most common shapes of inlined plaintext credentials. The list is
// deliberately narrow to keep false positives low: anything matching is
// almost certainly a leaked secret.
//
// Order matters: the longer / more specific patterns appear first so
// that error messages cite the strongest signal.
var inlinePlaintextHints = []*regexp.Regexp{
	// Long base64ish blobs. 32+ chars of base64 alphabet, often what
	// API keys look like in plaintext.
	regexp.MustCompile(`^[A-Za-z0-9+/_\-]{32,}={0,2}$`),
	// Anthropic / OpenAI shaped keys.
	regexp.MustCompile(`^sk-[A-Za-z0-9_\-]{20,}$`),
	regexp.MustCompile(`^xox[baprs]-[A-Za-z0-9_\-]{10,}$`),
	regexp.MustCompile(`(?i)^bearer\s+[A-Za-z0-9._\-]{20,}$`),
}

// looksLikeInlineSecret returns true when s exhibits one of the
// shapes we treat as inline plaintext credentials. The check is
// intentionally conservative — locators legitimately are short
// alphanumeric tokens.
func looksLikeInlineSecret(s string) bool {
	if len(s) < 16 {
		return false
	}
	for _, re := range inlinePlaintextHints {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Map is the canonical configuration shape: a map with exactly one
// kind key plus an optional `consumer_id` and `optional` flag.
//
// Examples:
//
//	{ "env": "ANTHROPIC_API_KEY" }
//	{ "keychain": "anthropic-api-key", "consumer_id": "llm:anthropic" }
//	{ "file": "/run/secrets/db_key" }
//	{ "aws_kms": "arn:aws:kms:us-east-1:111122223333:key/abcd" }
//
// The map form is what every consumer (bundle config, lockfile, RPC
// payload) embeds.
type Map map[string]any

// FromMap parses a configuration map into a CredentialReference. It
// rejects ambiguous shapes (multiple kind keys), unknown kinds, empty
// locators, and inline plaintext.
func FromMap(m Map) (CredentialReference, error) {
	if len(m) == 0 {
		return CredentialReference{}, fmt.Errorf("%w: empty reference object", ErrEmptyLocator)
	}

	var kind RefKind
	var locator string
	matchedKey := ""
	for _, k := range []string{"env", "keychain", "file", "aws_profile", "aws_kms", "yubikey_piv", "pkcs11"} {
		v, ok := m[k]
		if !ok {
			continue
		}
		if matchedKey != "" {
			return CredentialReference{}, fmt.Errorf("%w: keys=%s,%s", ErrAmbiguousReference, matchedKey, k)
		}
		matchedKey = k
		s, ok := v.(string)
		if !ok {
			return CredentialReference{}, fmt.Errorf("%w: kind=%s requires string locator", ErrLocatorFormat, k)
		}
		kind = kindFromWire(k)
		locator = strings.TrimSpace(s)
	}

	if matchedKey == "" {
		// Probe for unknown-kind keys to give a useful error.
		for k := range m {
			if k == "consumer_id" || k == "optional" {
				continue
			}
			return CredentialReference{}, fmt.Errorf("%w: %q", ErrUnknownKind, k)
		}
		return CredentialReference{}, fmt.Errorf("%w: no recognised kind key", ErrUnknownKind)
	}

	if locator == "" {
		return CredentialReference{}, fmt.Errorf("%w: kind=%s", ErrEmptyLocator, kind)
	}
	// Kind-specific format validation runs before the inline-plaintext
	// heuristic so that obviously-malformed locators (e.g. an env-var
	// name with spaces) report ErrLocatorFormat rather than the more
	// alarming ErrInlinePlaintext.
	if err := validateLocator(kind, locator); err != nil {
		return CredentialReference{}, err
	}
	if looksLikeInlineSecret(locator) {
		return CredentialReference{}, fmt.Errorf("%w: kind=%s", ErrInlinePlaintext, kind)
	}

	consumerID, _ := m["consumer_id"].(string)
	optional, _ := m["optional"].(bool)

	return CredentialReference{
		Kind:       kind,
		Locator:    locator,
		ConsumerID: strings.TrimSpace(consumerID),
		Optional:   optional,
	}, nil
}

// FromString parses a single canonical wire-form string of shape
// `kind:locator`. It is the form used in CLI flags, e.g.
// `--secret-backend=keychain:anthropic-api-key`.
func FromString(s string) (CredentialReference, error) {
	if s == "" {
		return CredentialReference{}, fmt.Errorf("%w: empty input", ErrEmptyLocator)
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		// Non-empty but whitespace-only input: it had a value but the
		// shape is invalid. Surface as a locator-format error.
		return CredentialReference{}, fmt.Errorf("%w: whitespace-only input", ErrLocatorFormat)
	}
	s = trimmed
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return CredentialReference{}, fmt.Errorf("%w: expected kind:locator", ErrLocatorFormat)
	}
	kindWire := s[:idx]
	locator := strings.TrimSpace(s[idx+1:])
	kind := kindFromWire(kindWire)
	if kind == RefUnknown {
		return CredentialReference{}, fmt.Errorf("%w: %q", ErrUnknownKind, kindWire)
	}
	if locator == "" {
		return CredentialReference{}, fmt.Errorf("%w: kind=%s", ErrEmptyLocator, kind)
	}
	// Kind-specific format validation precedes the inline-plaintext
	// heuristic; see FromMap for the rationale.
	if err := validateLocator(kind, locator); err != nil {
		return CredentialReference{}, err
	}
	if looksLikeInlineSecret(locator) {
		return CredentialReference{}, fmt.Errorf("%w: kind=%s", ErrInlinePlaintext, kind)
	}
	return CredentialReference{Kind: kind, Locator: locator}, nil
}

// validateLocator applies kind-specific shape checks. The checks are
// deliberately permissive: backends do their own deeper validation at
// resolve time. We only catch obvious format errors here to fail fast
// at config-load time.
func validateLocator(kind RefKind, locator string) error {
	switch kind {
	case RefEnv:
		// POSIX-ish env var names.
		if !envVarPattern.MatchString(locator) {
			return fmt.Errorf("%w: env var name %q invalid", ErrLocatorFormat, locator)
		}
	case RefAWSKMS:
		if !strings.HasPrefix(locator, "arn:") && !strings.HasPrefix(locator, "alias/") {
			return fmt.Errorf("%w: aws_kms expected ARN or alias/, got %q", ErrLocatorFormat, locator)
		}
	case RefFile:
		// File paths are validated at resolve time; here we only refuse
		// obvious garbage like a NUL byte.
		if strings.ContainsRune(locator, 0) {
			return fmt.Errorf("%w: file path contains NUL", ErrLocatorFormat)
		}
	}
	return nil
}

var envVarPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
