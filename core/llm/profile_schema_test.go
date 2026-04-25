package llm

import "testing"

func TestValidateProfile_Cases(t *testing.T) {
	good := ProviderProfile{
		ID: "anthropic-default", Kind: "anthropic", Model: "claude-sonnet",
		Cred: CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	if err := ValidateProfile(good); err != nil {
		t.Fatalf("good profile rejected: %v", err)
	}

	cases := []struct {
		name string
		p    ProviderProfile
	}{
		{"missing id", ProviderProfile{Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env", Locator: "K"}}},
		{"missing kind", ProviderProfile{ID: "p", Model: "x", Cred: CredentialReference{Kind: "env", Locator: "K"}}},
		{"missing model", ProviderProfile{ID: "p", Kind: "anthropic", Cred: CredentialReference{Kind: "env", Locator: "K"}}},
		{"unknown cred kind", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "inline", Locator: "secret"}}},
		{"missing cred kind", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Locator: "K"}}},
		{"missing cred locator", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env"}}},
		{"bedrock missing region", ProviderProfile{ID: "p", Kind: "bedrock", Model: "anthropic.claude-3-sonnet", Cred: CredentialReference{Kind: "aws_profile", Locator: "default"}}},
		{"plaintext credential prefix", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env", Locator: "sk-ant-real-secret-here"}}},
		{"locator too long", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env", Locator: longString(257)}}},
		{"retry max < 1", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env", Locator: "K"}, Retry: &RetryPolicy{MaxAttempts: 0}}},
		{"retry base > max", ProviderProfile{ID: "p", Kind: "anthropic", Model: "x", Cred: CredentialReference{Kind: "env", Locator: "K"}, Retry: &RetryPolicy{MaxAttempts: 3, BaseMS: 500, MaxMS: 100}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateProfile(tc.p); err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}

func TestValidateProfile_BedrockOK(t *testing.T) {
	p := ProviderProfile{
		ID: "bedrock", Kind: "bedrock", Model: "anthropic.claude-3-sonnet",
		Cred: CredentialReference{Kind: "aws_profile", Locator: "default"}, Region: "us-east-1",
	}
	if err := ValidateProfile(p); err != nil {
		t.Fatalf("bedrock with region rejected: %v", err)
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
