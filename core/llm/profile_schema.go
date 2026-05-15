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
	if err := validateCredRef(p.ID, p.Cred); err != nil {
		return err
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
		if p.Retry.MaxAttempts < 1 {
			return fmt.Errorf("llm: profile %q: retry.max_attempts must be >= 1", p.ID)
		}
		if p.Retry.BaseMS < 0 || p.Retry.MaxMS < 0 {
			return fmt.Errorf("llm: profile %q: retry delays must be >= 0", p.ID)
		}
		if p.Retry.MaxMS > 0 && p.Retry.BaseMS > p.Retry.MaxMS {
			return fmt.Errorf("llm: profile %q: retry.base_ms exceeds retry.max_ms", p.ID)
		}
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
