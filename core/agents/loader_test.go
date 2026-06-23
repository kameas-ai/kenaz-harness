package agents_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agents"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// TestBundledProfilesLoad verifies all five shipped profiles parse cleanly.
func TestBundledProfilesLoad(t *testing.T) {
	t.Parallel()
	profiles, err := agents.LoadAll("")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	wantIDs := []string{"bug-finder", "code-reviewer", "explore", "implementer", "summarizer"}
	if len(profiles) != len(wantIDs) {
		t.Errorf("got %d profiles, want %d", len(profiles), len(wantIDs))
	}
	byID := make(map[string]agents.Profile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	for _, id := range wantIDs {
		p, ok := byID[id]
		if !ok {
			t.Errorf("missing bundled profile %q", id)
			continue
		}
		if !p.Bundled {
			t.Errorf("profile %q: Bundled=false, want true", id)
		}
		if p.Name == "" {
			t.Errorf("profile %q: Name empty", id)
		}
		if p.Description == "" {
			t.Errorf("profile %q: Description empty", id)
		}
	}
}

// TestBundledProfilesValidate verifies Validate passes for all bundled profiles.
func TestBundledProfilesValidate(t *testing.T) {
	t.Parallel()
	profiles, err := agents.LoadAll("")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, p := range profiles {
		if err := agents.Validate(p); err != nil {
			t.Errorf("profile %q: Validate: %v", p.ID, err)
		}
	}
}

// TestUserProfileOverridesBundled verifies a user profile wins over a bundled
// one when they share the same id.
func TestUserProfileOverridesBundled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Write a user profile that overrides "explore".
	userYAML := `
id: explore
name: My Explore
description: User override of the bundled explore profile.
autonomy_tier: bold
merge_policy: confirm
`
	if err := os.WriteFile(filepath.Join(agentsDir, "explore.yaml"), []byte(userYAML), 0o640); err != nil {
		t.Fatal(err)
	}

	profiles, err := agents.LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	var got *agents.Profile
	for i := range profiles {
		if profiles[i].ID == "explore" {
			got = &profiles[i]
			break
		}
	}
	if got == nil {
		t.Fatal("explore profile not found")
	}
	if got.Name != "My Explore" {
		t.Errorf("Name=%q, want %q", got.Name, "My Explore")
	}
	if got.Bundled {
		t.Error("user profile should not have Bundled=true")
	}
	if got.AutonomyTier != autonomy.TierBold {
		t.Errorf("AutonomyTier=%v, want TierBold", got.AutonomyTier)
	}
}

// TestSaveBundledReadOnly verifies that Save returns ErrBundledReadOnly for
// a bundled id.
func TestSaveBundledReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := agents.Profile{
		ID:          "explore",
		Name:        "My Explore",
		Description: "Override attempt.",
		MergePolicy: agents.MergePolicyAuto,
	}
	err := agents.Save(dir, p)
	if err == nil {
		t.Fatal("Save: expected ErrBundledReadOnly, got nil")
	}
	if err != agents.ErrBundledReadOnly {
		t.Errorf("Save: got %v, want ErrBundledReadOnly", err)
	}
}

// TestSaveAndLoadRoundTrip verifies that a user profile saved to disk can
// be loaded back with the same field values.
func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := agents.Profile{
		ID:           "my-custom-agent",
		Name:         "My Custom Agent",
		Description:  "A test-only profile.",
		Model:        "anthropic/claude-haiku-4-5",
		AutonomyTier: autonomy.TierCautious,
		AllowedTools: []string{"kenaz__read_file", "kenaz__grep"},
		DeniedTools:  []string{"kenaz__bash"},
		BudgetTokens: 30000,
		BudgetTimeS:  120,
		MergePolicy:  agents.MergePolicyConfirm,
	}
	if err := agents.Save(dir, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := agents.LoadProfile(dir, "my-custom-agent")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if got.Name != p.Name {
		t.Errorf("Name=%q, want %q", got.Name, p.Name)
	}
	if got.AutonomyTier != p.AutonomyTier {
		t.Errorf("AutonomyTier=%v, want %v", got.AutonomyTier, p.AutonomyTier)
	}
	if len(got.AllowedTools) != len(p.AllowedTools) {
		t.Errorf("AllowedTools len=%d, want %d", len(got.AllowedTools), len(p.AllowedTools))
	}
	if got.EffectiveMergePolicy() != agents.MergePolicyConfirm {
		t.Errorf("MergePolicy=%v, want confirm", got.EffectiveMergePolicy())
	}
}

// TestValidate_UnknownMergePolicy checks that an unknown merge_policy is rejected.
func TestValidate_UnknownMergePolicy(t *testing.T) {
	t.Parallel()
	p := agents.Profile{
		ID:          "test",
		Name:        "Test",
		Description: "Test.",
		MergePolicy: "invalid-policy",
	}
	if err := agents.Validate(p); err == nil {
		t.Error("Validate: expected error for invalid merge_policy, got nil")
	}
}

// TestValidate_ToolInBothLists checks that a tool in both allowed and denied is rejected.
func TestValidate_ToolInBothLists(t *testing.T) {
	t.Parallel()
	p := agents.Profile{
		ID:           "test",
		Name:         "Test",
		Description:  "Test.",
		AllowedTools: []string{"kenaz__bash"},
		DeniedTools:  []string{"kenaz__bash"},
	}
	if err := agents.Validate(p); err == nil {
		t.Error("Validate: expected error for tool in both lists, got nil")
	}
}

// TestDeleteBundledReadOnly checks Delete rejects bundled ids.
func TestDeleteBundledReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := agents.Delete(dir, "explore")
	if err != agents.ErrBundledReadOnly {
		t.Errorf("Delete: got %v, want ErrBundledReadOnly", err)
	}
}

// TestDeleteNotFound checks Delete returns ErrProfileNotFound for unknown user ids.
func TestDeleteNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := agents.Delete(dir, "no-such-profile")
	if err == nil {
		t.Error("Delete: expected ErrProfileNotFound, got nil")
	}
}
