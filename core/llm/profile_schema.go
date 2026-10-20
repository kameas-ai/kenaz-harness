package llm

import (
	"errors"
	"fmt"
	"strings"
)

// ValidateProfile applies the schema rules from plan §5.1 to a parsed
// ProviderProfile. It does not require any registry context — kind
// validity against registered adapters is the registry's concern.
//
// Returns nil on success or a typed error describing the first failure.
func ValidateProfile(p ProviderProfile) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("llm: profile id is required")
	}
	if strings.TrimSpace(p.Kind) == "" {
		return errors.New("llm: profile kind is required")
	}
	if strings.TrimSpace(p.Model) == "" {
		return fmt.Errorf("llm: profile %q: model is required", p.ID)
	}
	// A custom-openai profile pinned to auth_scheme=none (a host-granted
	// local endpoint in a served workbench) carries no credential
	// reference at all; the registry skips resolution for it. Every other
	// profile — and any custom-openai profile whose scheme sends a
	// credential — still needs a valid reference.
	if !credentialless(p) {
		if err := validateCredRef(p.ID, p.Cred); err != nil {
			return err
		}
	}
	if p.Kind == "custom-openai" && strings.TrimSpace(p.Endpoint) == "" {
		return fmt.Errorf("llm: profile %q: custom-openai kind requires non-empty endpoint", p.ID)
	}
	if p.Kind == "bedrock" && strings.TrimSpace(p.Region) == "" {
		// Plan R7 / spec edge case: bedrock + missing region must be
		// caught before any AWS call.
		return fmt.Errorf("llm: profile %q: bedrock kind requires non-empty region", p.ID)
	}
	// Gemini Vertex endpoint requires project + region.
	if p.Kind == "gemini" && p.GeminiEndpointKind == "vertex" {
		if strings.TrimSpace(p.Project) == "" {
			return fmt.Errorf("llm: profile %q: gemini vertex endpoint requires non-empty project", p.ID)
		}
		if strings.TrimSpace(p.Region) == "" {
			return fmt.Errorf("llm: profile %q: gemini vertex endpoint requires non-empty region", p.ID)
		}
	}
	if p.Retry != nil {
		if err := validateRetryPolicy(p.ID, p.Retry); err != nil {
			return err
		}
	}
	return nil
}

// validateRetryPolicy applies the shared retry-shape rules to r, prefixing
// errors with the given identifier (a ProviderProfile.ID or a
// ModelProfile.ID). Shared between ValidateProfile and
// ValidateModelProfile so the two artifact kinds can't drift on what
// "a well-formed retry policy" means.
func validateRetryPolicy(id string, r *RetryPolicy) error {
	if r.MaxAttempts < 1 {
		return fmt.Errorf("llm: profile %q: retry.max_attempts must be >= 1", id)
	}
	if r.BaseMS < 0 || r.MaxMS < 0 {
		return fmt.Errorf("llm: profile %q: retry delays must be >= 0", id)
	}
	if r.MaxMS > 0 && r.BaseMS > r.MaxMS {
		return fmt.Errorf("llm: profile %q: retry.base_ms exceeds retry.max_ms", id)
	}
	return nil
}

var validCredKinds = map[string]struct{}{
	"env":         {},
	"keychain":    {},
	"file":        {},
	"aws_profile": {},
	"kms":         {},
}

func validateCredRef(profileID string, ref CredentialReference) error {
	kind := strings.TrimSpace(ref.Kind)
	if kind == "" {
		return fmt.Errorf("llm: profile %q: credential reference kind is required (one of env|keychain|file|aws_profile|kms)", profileID)
	}
	if _, ok := validCredKinds[kind]; !ok {
		return fmt.Errorf("llm: profile %q: unknown credential reference kind %q (must be env|keychain|file|aws_profile|kms)", profileID, ref.Kind)
	}
	if !ref.Source.Valid() {
		return fmt.Errorf("llm: profile %q: unknown credential source %q (must be local|user_sync|org|team)", profileID, ref.Source)
	}
	if ref.Source.IsFleet() {
		// A Fleet-sourced key always lives in the OS keychain under a
		// namespace derived from Source + CredentialID; the locator is
		// not consulted.
		if kind != "keychain" {
			return fmt.Errorf("llm: profile %q: a %s credential must be kind keychain, not %q", profileID, ref.Source, ref.Kind)
		}
		if !ValidCredentialID(ref.CredentialID) {
			return fmt.Errorf("llm: profile %q: a %s credential needs a valid credential_id", profileID, ref.Source)
		}
		return nil
	}
	if ref.CredentialID != "" {
		return fmt.Errorf("llm: profile %q: credential_id is only valid for a Fleet source (user_sync|org|team)", profileID)
	}
	if strings.TrimSpace(ref.Locator) == "" {
		return fmt.Errorf("llm: profile %q: credential reference locator is required", profileID)
	}
	// Defense-in-depth (C-002): reject anything that *looks* like an
	// inline plaintext credential. The locator should be a name / path,
	// never a credential value. Heuristic: a locator that contains a
	// known credential prefix or is implausibly long for a name is
	// rejected.
	loc := ref.Locator
	for _, prefix := range plaintextCredentialPrefixes {
		if strings.HasPrefix(loc, prefix) {
			return fmt.Errorf("llm: profile %q: credential reference locator must be an indirect reference, not a plaintext credential value (matched prefix %q)", profileID, prefix)
		}
	}
	if len(loc) > 256 {
		return fmt.Errorf("llm: profile %q: credential reference locator is implausibly long (%d bytes); use an indirect reference", profileID, len(loc))
	}
	return nil
}

// plaintextCredentialPrefixes is the connector's contribution to the
// redaction-pattern catalog (per WP04 / R9). The list is conservative
// and is shared between schema validation (defense-in-depth) and the
// audit redactor.
var plaintextCredentialPrefixes = []string{
	"sk-ant-",
	"sk-",
	"AKIA",
	"ASIA",
	"ya29.",
	"AIza",
}

// PlaintextCredentialPrefixes returns a copy of the curated catalog so
// other packages (audit redactor, schema validators) can reuse it.
func PlaintextCredentialPrefixes() []string {
	out := make([]string, len(plaintextCredentialPrefixes))
	copy(out, plaintextCredentialPrefixes)
	return out
}

// ValidCredentialID reports whether id is a well-formed Fleet credential
// id: 1-128 characters of [A-Za-z0-9._-], starting with an alphanumeric.
// Fleet issues lowercase hyphenated UUIDs (spec §10); the wider charset
// keeps the check from rejecting a future id format while still keeping
// path separators and keychain-index names (leading "_") out of the
// derived keychain locator.
func ValidCredentialID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// credentialless reports whether p is the one profile shape allowed to
// carry no credential reference: kind custom-openai, a zero reference, and
// Defaults["auth_scheme"] explicitly "none". The kind is spelled as a
// literal (core/llm/custom imports this package, not the reverse).
func credentialless(p ProviderProfile) bool {
	if p.Kind != "custom-openai" || !p.Cred.IsZero() {
		return false
	}
	scheme, _ := p.Defaults["auth_scheme"].(string)
	return scheme == "none"
}

// NormalizeAuthScheme folds the top-level ProviderProfile.AuthScheme field
// into Defaults["auth_scheme"], the single place the custom-openai adapter,
// credentialless and the served probe read the scheme from. Before this, a
// personal-store row carrying only the top-level field passed the store's
// own none-auth check but failed ValidateProfile, aborting the whole
// MergePersonalProfiles batch — and the adapter ignored the field anyway.
// An explicit Defaults value wins; other kinds are returned unchanged. The
// Defaults map is copied, never mutated in place.
func NormalizeAuthScheme(p ProviderProfile) ProviderProfile {
	if p.Kind != "custom-openai" || p.AuthScheme == "" {
		return p
	}
	if s, ok := p.Defaults["auth_scheme"].(string); ok && s != "" {
		return p
	}
	d := make(map[string]any, len(p.Defaults)+1)
	for k, v := range p.Defaults {
		d[k] = v
	}
	d["auth_scheme"] = p.AuthScheme
	p.Defaults = d
	return p
}
