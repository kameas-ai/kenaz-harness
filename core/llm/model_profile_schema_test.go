package llm

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

func TestValidateModelProfile_ZeroValueIsInert(t *testing.T) {
	var p ModelProfile
	if !p.IsZero() {
		t.Fatalf("zero-value ModelProfile reports IsZero()==false")
	}
	if err := ValidateModelProfile(p); err != nil {
		t.Fatalf("zero-value (absent) profile must be valid/inert, got error: %v", err)
	}
}

func TestValidateModelProfile_WellFormedAccepted(t *testing.T) {
	trueVal := true
	good := ModelProfile{
		ID:      "claude-sonnet-*",
		Version: "2026.07.30",
		PromptTemplate: &PromptTemplateRef{
			ID:                 "constitution-anthropic",
			Version:            "3",
			Format:             "xml",
			AttentionPlacement: true,
		},
		ToolDialect: &ToolDialectConfig{
			Dialect:                 "native",
			ParallelToolCalls:       &trueVal,
			MaxToolDescriptionBytes: 2000,
		},
		Context: &ContextPolicy{
			Aggressiveness:        compactionpolicy.AggressivenessBalanced,
			ContextWindowOverride: 180000,
		},
		Retry:           &RetryPolicy{MaxAttempts: 3, BaseMS: 250, MaxMS: 5000, Jitter: "full"},
		FallbackChainId: "anthropic-then-openrouter",
		EvalManifest: &EvalManifestRef{
			ID:      "chat-default-suite",
			Version: "1",
		},
	}
	if err := ValidateModelProfile(good); err != nil {
		t.Fatalf("well-formed profile rejected: %v", err)
	}
	if good.IsZero() {
		t.Fatalf("well-formed profile must not report IsZero()==true")
	}
}

func TestValidateModelProfile_MinimalAccepted(t *testing.T) {
	// A profile carrying only the required identity fields (no optional
	// behavioral fields set) must validate — every behavioral field is
	// optional/zero-safe per the WP01 design constraint.
	p := ModelProfile{ID: "gpt-4o", Version: "1"}
	if err := ValidateModelProfile(p); err != nil {
		t.Fatalf("minimal profile rejected: %v", err)
	}
}

func TestValidateModelProfile_Rejections(t *testing.T) {
	cases := []struct {
		name string
		p    ModelProfile
	}{
		{"missing id", ModelProfile{Version: "1"}},
		{"missing version", ModelProfile{ID: "p"}},
		{
			"prompt template missing id",
			ModelProfile{ID: "p", Version: "1", PromptTemplate: &PromptTemplateRef{Format: "xml"}},
		},
		{
			"prompt template unknown format",
			ModelProfile{ID: "p", Version: "1", PromptTemplate: &PromptTemplateRef{ID: "t", Format: "yaml"}},
		},
		{
			"tool dialect negative max bytes",
			ModelProfile{ID: "p", Version: "1", ToolDialect: &ToolDialectConfig{MaxToolDescriptionBytes: -1}},
		},
		{
			"context unknown aggressiveness",
			ModelProfile{ID: "p", Version: "1", Context: &ContextPolicy{Aggressiveness: "extreme"}},
		},
		{
			"context negative window override",
			ModelProfile{ID: "p", Version: "1", Context: &ContextPolicy{ContextWindowOverride: -1}},
		},
		{
			"retry max attempts < 1",
			ModelProfile{ID: "p", Version: "1", Retry: &RetryPolicy{MaxAttempts: 0}},
		},
		{
			"retry base exceeds max",
			ModelProfile{ID: "p", Version: "1", Retry: &RetryPolicy{MaxAttempts: 3, BaseMS: 500, MaxMS: 100}},
		},
		{
			"eval manifest missing id",
			ModelProfile{ID: "p", Version: "1", EvalManifest: &EvalManifestRef{Version: "1"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateModelProfile(tc.p); err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}

// TestModelProfile_IndependentOfProviderProfile documents the mission's
// core design constraint (spec §5 / plan conflict zones): ModelProfile
// and ProviderProfile are separate types with no shared required fields,
// so a connection-config value (credential, endpoint) can never leak
// into behavioral validation and vice-versa.
func TestModelProfile_IndependentOfProviderProfile(t *testing.T) {
	prof := ProviderProfile{
		ID: "anthropic-default", Kind: "anthropic", Model: "claude-sonnet",
		Cred: CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	if err := ValidateProfile(prof); err != nil {
		t.Fatalf("connection profile rejected: %v", err)
	}

	mp := ModelProfile{ID: "claude-sonnet-*", Version: "1"}
	if err := ValidateModelProfile(mp); err != nil {
		t.Fatalf("behavioral profile rejected: %v", err)
	}

	// Rotating credentials (a ProviderProfile mutation) does not touch
	// or require re-validating the ModelProfile, and vice-versa.
	prof.Cred = CredentialReference{Kind: "keychain", Locator: "anthropic-rotated"}
	if err := ValidateProfile(prof); err != nil {
		t.Fatalf("rotated connection profile rejected: %v", err)
	}
	if err := ValidateModelProfile(mp); err != nil {
		t.Fatalf("behavioral profile invalidated by unrelated credential rotation: %v", err)
	}
}

// --- WP06: bundle-build layering validator --------------------------------

func TestValidateModelProfileBundle_CleanProfilePasses(t *testing.T) {
	raw := []byte(`
id: claude-sonnet-*
version: "2026.07.30"
prompt_template:
  id: constitution-anthropic
  version: "3"
  format: xml
tool_dialect:
  dialect: native
  max_tool_description_bytes: 2000
context:
  aggressiveness: balanced
  context_window_override: 180000
retry:
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
fallback_chain_id: anthropic-then-openrouter
eval_manifest:
  id: chat-default-suite
  version: "1"
`)
	p, err := ValidateModelProfileBundle(raw)
	if err != nil {
		t.Fatalf("clean bundle rejected: %v", err)
	}
	if p.ID != "claude-sonnet-*" || p.Version != "2026.07.30" {
		t.Fatalf("unexpected parsed profile: %+v", p)
	}
}

func TestValidateModelProfileBundle_MinimalCleanProfilePasses(t *testing.T) {
	raw := []byte(`
id: gpt-4o
version: "1"
`)
	if _, err := ValidateModelProfileBundle(raw); err != nil {
		t.Fatalf("minimal clean bundle rejected: %v", err)
	}
}

func TestValidateModelProfileBundle_RejectsTopLevelCedarField(t *testing.T) {
	raw := []byte(`
id: p
version: "1"
cedar_actions:
  - AllowSpend
`)
	_, err := ValidateModelProfileBundle(raw)
	if err == nil {
		t.Fatalf("expected rejection of a profile carrying a cedar field, got nil")
	}
	if !strings.Contains(err.Error(), "cedar_actions") {
		t.Fatalf("error must name the offending key %q, got: %v", "cedar_actions", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cedar") {
		t.Fatalf("error should hint at Cedar/authorization, got: %v", err)
	}
}

func TestValidateModelProfileBundle_RejectsTopLevelBudgetField(t *testing.T) {
	raw := []byte(`
id: p
version: "1"
budget:
  max_spend_usd: 100
`)
	_, err := ValidateModelProfileBundle(raw)
	if err == nil {
		t.Fatalf("expected rejection of a profile carrying a budget field, got nil")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error must name the offending key %q, got: %v", "budget", err)
	}
}

func TestValidateModelProfileBundle_RejectsNestedSpendField(t *testing.T) {
	// The governance/budget check must not be a top-level-only allow-list
	// diff — it recurses into ModelProfile's own declared nested objects
	// too, since a smuggled key could just as easily be appended one
	// level down inside a legitimate object (e.g. "context:").
	raw := []byte(`
id: p
version: "1"
context:
  aggressiveness: balanced
  spend_cap_usd: 50
`)
	_, err := ValidateModelProfileBundle(raw)
	if err == nil {
		t.Fatalf("expected rejection of a nested spend field, got nil")
	}
	if !strings.Contains(err.Error(), "context.spend_cap_usd") {
		t.Fatalf("error must name the full nested key path, got: %v", err)
	}
}

func TestValidateModelProfileBundle_RejectsTypoedLegitimateKey(t *testing.T) {
	// Strict unknown-field rejection is the same mechanism that catches
	// governance smuggling; it also catches an honest typo of a real
	// field name. Documented trade-off: bundle authors get a fail-closed,
	// exact-schema UX rather than the key being silently ignored.
	raw := []byte(`
id: p
versoin: "1"
`)
	_, err := ValidateModelProfileBundle(raw)
	if err == nil {
		t.Fatalf("expected rejection of a typo'd field, got nil")
	}
	if !strings.Contains(err.Error(), "versoin") {
		t.Fatalf("error must name the offending (typo'd) key, got: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "cedar") || strings.Contains(strings.ToLower(err.Error()), "budget") {
		t.Fatalf("typo should get the generic unknown-field message, not a governance hint: %v", err)
	}
}
