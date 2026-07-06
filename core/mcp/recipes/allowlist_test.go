package recipes

import (
	"testing"
)

func TestAllowlistFilter_NoRestriction(t *testing.T) {
	var f AllowlistFilter
	// No allow-list set → all recipes permitted.
	for _, id := range []string{"github", "slack", "filesystem", "anything"} {
		if !f.IsAllowed(id) {
			t.Errorf("IsAllowed(%q) = false, want true (no restriction)", id)
		}
	}
}

func TestAllowlistFilter_SetAndAllow(t *testing.T) {
	var f AllowlistFilter
	f.Set([]string{"github", "slack"})

	if !f.IsAllowed("github") {
		t.Error("IsAllowed(github) should be true")
	}
	if !f.IsAllowed("slack") {
		t.Error("IsAllowed(slack) should be true")
	}
	if f.IsAllowed("filesystem") {
		t.Error("IsAllowed(filesystem) should be false — not in allowlist")
	}
}

func TestAllowlistFilter_ClearAllowlist(t *testing.T) {
	var f AllowlistFilter
	f.Set([]string{"github"})
	if f.IsAllowed("slack") {
		t.Error("slack should not be allowed with active allowlist")
	}

	// Clear the allowlist — all recipes become allowed again.
	f.Set(nil)
	if !f.IsAllowed("slack") {
		t.Error("slack should be allowed after clearing allowlist")
	}
	if !f.IsAllowed("github") {
		t.Error("github should be allowed after clearing allowlist")
	}
}

// TestAllowlistFilter_EmptySliceBlocksAll verifies the cross-repo contract:
// a non-nil empty slice means "block all" (distinct from nil = no restriction).
// This mirrors the fleet-side behaviour where mcp_allowlist:[] in the bundle
// JSON means the org admin has explicitly blocked all MCP recipes.
func TestAllowlistFilter_EmptySliceBlocksAll(t *testing.T) {
	var f AllowlistFilter
	f.Set([]string{"github"})
	f.Set([]string{}) // non-nil empty slice → block-all, NOT clear
	if f.IsAllowed("github") {
		t.Error("github should be blocked after Set([]) — block-all mode active")
	}
	if f.IsAllowed("filesystem") {
		t.Error("filesystem should be blocked after Set([]) — block-all mode active")
	}
	// nil clears the restriction.
	f.Set(nil)
	if !f.IsAllowed("filesystem") {
		t.Error("filesystem should be allowed after Set(nil)")
	}
}

func TestAllowlistFilter_ActiveIDs(t *testing.T) {
	var f AllowlistFilter
	if ids := f.ActiveIDs(); ids != nil {
		t.Errorf("ActiveIDs() on unrestricted filter: got %v, want nil", ids)
	}

	f.Set([]string{"github", "slack"})
	ids := f.ActiveIDs()
	if len(ids) != 2 {
		t.Errorf("ActiveIDs() len = %d, want 2", len(ids))
	}
}

func TestGlobalAllowlist_ApplyFleetAllowlist(t *testing.T) {
	// Reset state after test.
	t.Cleanup(func() { ApplyFleetAllowlist(nil) })

	ApplyFleetAllowlist([]string{"github", "memory"})
	if !IsAllowed("github") {
		t.Error("github should be allowed")
	}
	if IsAllowed("slack") {
		t.Error("slack should not be allowed")
	}

	// Clear.
	ApplyFleetAllowlist(nil)
	if !IsAllowed("slack") {
		t.Error("slack should be allowed after clearing")
	}
}
