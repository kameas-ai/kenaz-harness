package memory

import (
	"reflect"
	"testing"
)

func TestCheckEligibility_NoProfiles(t *testing.T) {
	t.Parallel()
	got := CheckEligibility(nil)
	if got.HasEligible {
		t.Error("expected HasEligible=false for empty profile list")
	}
	if got.AllProfiles != 0 {
		t.Errorf("AllProfiles: got %d, want 0", got.AllProfiles)
	}
	if got.EligibleProfiles != 0 {
		t.Errorf("EligibleProfiles: got %d, want 0", got.EligibleProfiles)
	}
	if len(got.SkippedKinds) != 0 {
		t.Errorf("SkippedKinds: got %v, want []", got.SkippedKinds)
	}
}

func TestCheckEligibility_OnlyAnthropicProfile(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "anthropic"},
	}
	got := CheckEligibility(profiles)
	if got.HasEligible {
		t.Error("expected HasEligible=false for anthropic-only list")
	}
	if got.AllProfiles != 1 {
		t.Errorf("AllProfiles: got %d, want 1", got.AllProfiles)
	}
	if got.EligibleProfiles != 0 {
		t.Errorf("EligibleProfiles: got %d, want 0", got.EligibleProfiles)
	}
	if !reflect.DeepEqual(got.SkippedKinds, []string{"anthropic"}) {
		t.Errorf("SkippedKinds: got %v, want [anthropic]", got.SkippedKinds)
	}
}

func TestCheckEligibility_AnthropicPlusBedrockSkipped(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "anthropic"},
		{Kind: "bedrock"},
	}
	got := CheckEligibility(profiles)
	if got.HasEligible {
		t.Error("expected HasEligible=false")
	}
	if got.AllProfiles != 2 {
		t.Errorf("AllProfiles: got %d, want 2", got.AllProfiles)
	}
	// SkippedKinds should be sorted: anthropic, bedrock.
	want := []string{"anthropic", "bedrock"}
	if !reflect.DeepEqual(got.SkippedKinds, want) {
		t.Errorf("SkippedKinds: got %v, want %v", got.SkippedKinds, want)
	}
}

func TestCheckEligibility_OpenAIIsEligible(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "openai"},
	}
	got := CheckEligibility(profiles)
	if !got.HasEligible {
		t.Error("expected HasEligible=true for openai profile")
	}
	if got.EligibleProfiles != 1 {
		t.Errorf("EligibleProfiles: got %d, want 1", got.EligibleProfiles)
	}
	if len(got.SkippedKinds) != 0 {
		t.Errorf("SkippedKinds: got %v, want []", got.SkippedKinds)
	}
}

func TestCheckEligibility_OpenRouterIsEligible(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "openrouter"},
	}
	got := CheckEligibility(profiles)
	if !got.HasEligible {
		t.Error("expected HasEligible=true for openrouter profile")
	}
}

func TestCheckEligibility_CustomOpenAIWithEndpoint(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "custom_openai_compatible", Endpoint: "http://localhost:11434"},
	}
	got := CheckEligibility(profiles)
	if !got.HasEligible {
		t.Error("expected HasEligible=true for custom_openai_compatible with endpoint")
	}
	if got.EligibleProfiles != 1 {
		t.Errorf("EligibleProfiles: got %d, want 1", got.EligibleProfiles)
	}
}

func TestCheckEligibility_CustomOpenAIWithoutEndpointIsNotEligible(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "custom_openai_compatible", Endpoint: ""},
	}
	got := CheckEligibility(profiles)
	if got.HasEligible {
		t.Error("expected HasEligible=false for custom_openai_compatible without endpoint")
	}
	// custom_openai_compatible without endpoint is a misconfiguration, not a
	// "known skipped kind" — should NOT appear in SkippedKinds.
	if len(got.SkippedKinds) != 0 {
		t.Errorf("SkippedKinds: got %v, want []", got.SkippedKinds)
	}
}

func TestCheckEligibility_AzureCompleteIsEligible(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "azure", AzureComplete: true},
	}
	got := CheckEligibility(profiles)
	if !got.HasEligible {
		t.Error("expected HasEligible=true for azure profile with all required fields")
	}
}

func TestCheckEligibility_AzureIncompleteIsNotEligible(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "azure", AzureComplete: false},
	}
	got := CheckEligibility(profiles)
	if got.HasEligible {
		t.Error("expected HasEligible=false for azure profile missing required fields")
	}
	// Azure without required fields is a misconfiguration, not a skipped kind.
	if len(got.SkippedKinds) != 0 {
		t.Errorf("SkippedKinds: got %v, want []", got.SkippedKinds)
	}
}

func TestCheckEligibility_MixedProfilesAnthropicAndOpenAI(t *testing.T) {
	t.Parallel()
	// Anthropic is skipped, openai is eligible — HasEligible=true.
	profiles := []ProfileEligibilityInput{
		{Kind: "anthropic"},
		{Kind: "openai"},
	}
	got := CheckEligibility(profiles)
	if !got.HasEligible {
		t.Error("expected HasEligible=true when at least one eligible profile exists")
	}
	if got.AllProfiles != 2 {
		t.Errorf("AllProfiles: got %d, want 2", got.AllProfiles)
	}
	if got.EligibleProfiles != 1 {
		t.Errorf("EligibleProfiles: got %d, want 1", got.EligibleProfiles)
	}
	// anthropic is skipped even though openai is eligible.
	if !reflect.DeepEqual(got.SkippedKinds, []string{"anthropic"}) {
		t.Errorf("SkippedKinds: got %v, want [anthropic]", got.SkippedKinds)
	}
}

func TestCheckEligibility_SkippedKindsAreDeduplicated(t *testing.T) {
	t.Parallel()
	// Two anthropic profiles should only appear once in SkippedKinds.
	profiles := []ProfileEligibilityInput{
		{Kind: "anthropic"},
		{Kind: "anthropic"},
	}
	got := CheckEligibility(profiles)
	if !reflect.DeepEqual(got.SkippedKinds, []string{"anthropic"}) {
		t.Errorf("SkippedKinds: got %v, want [anthropic] (deduplicated)", got.SkippedKinds)
	}
	if got.AllProfiles != 2 {
		t.Errorf("AllProfiles: got %d, want 2", got.AllProfiles)
	}
}

func TestCheckEligibility_SkippedKindsAreSorted(t *testing.T) {
	t.Parallel()
	profiles := []ProfileEligibilityInput{
		{Kind: "bedrock"},
		{Kind: "anthropic"},
	}
	got := CheckEligibility(profiles)
	want := []string{"anthropic", "bedrock"}
	if !reflect.DeepEqual(got.SkippedKinds, want) {
		t.Errorf("SkippedKinds: got %v, want %v (sorted)", got.SkippedKinds, want)
	}
}
