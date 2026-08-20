package settings

import "testing"

func TestWP00Falsify_AutoCollapseZeroValue(t *testing.T) {
	var s Settings
	got := s.EffectiveAutoCollapseBranchesInSidebar()
	if got != false {
		t.Fatalf("expected zero-value Settings to (wrongly) return false today, got %v", got)
	}
	if DefaultAutoCollapseBranchesInSidebar != true {
		t.Fatalf("expected documented default true")
	}
	t.Logf("OBSERVED: zero-value Settings.EffectiveAutoCollapseBranchesInSidebar() = %v, documented default = %v -- MISMATCH CONFIRMED", got, DefaultAutoCollapseBranchesInSidebar)
}
