package llm

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/compaction"
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
			Aggressiveness:        compaction.AggressivenessBalanced,
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
