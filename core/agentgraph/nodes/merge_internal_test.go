package nodes

// Internal tests for the user-overlay deep-merge path. These live in
// `package nodes` (not `nodes_test`) because they exercise the
// unexported `mergeUserOverlay` helper directly. The public-API tests
// in `loader_test.go` exercise the same path via filesystem fixtures.

import (
	"errors"
	"testing"
)

// TestMergeUserOverlayHonoursAllowedFields drives the FR-022 allowed-
// field path: a user override may tune `defaults`, `attrs.<name>.default`,
// `description`, `display_name`, `version`, and additive aliases.
func TestMergeUserOverlayHonoursAllowedFields(t *testing.T) {
	t.Parallel()
	defOld := float64(0)
	defNew := float64(1)
	shipped := &Manifest{
		ID:          "review",
		Extends:     "compute",
		Category:    CategoryCompute,
		DisplayName: "Review (shipped)",
		Description: "shipped desc",
		Executor:    "agentgraph.ExecReview",
		Aliases:     []string{"reviewer"},
		Version:     "1.0",
		Attrs: map[string]AttrSpec{
			"max_iterations": {Type: AttrTypeInt, Min: &defOld, Default: 3},
		},
		Defaults: map[string]interface{}{
			"max_iterations": 3,
		},
	}
	user := &Manifest{
		ID:          "review",
		DisplayName: "Review (user)",
		Description: "tuned for my graphs",
		Aliases:     []string{"my_review"},
		Version:     "1.0-local",
		Attrs: map[string]AttrSpec{
			"max_iterations": {Default: 5, Min: &defNew},
		},
		Defaults: map[string]interface{}{
			"max_iterations": 5,
		},
	}
	merged, err := mergeUserOverlay(shipped, user)
	if err != nil {
		t.Fatalf("mergeUserOverlay: %v", err)
	}
	if merged.DisplayName != "Review (user)" {
		t.Errorf("display_name: got %q", merged.DisplayName)
	}
	if merged.Description != "tuned for my graphs" {
		t.Errorf("description: got %q", merged.Description)
	}
	if merged.Version != "1.0-local" {
		t.Errorf("version: got %q", merged.Version)
	}
	// Aliases additive.
	if got, want := len(merged.Aliases), 2; got != want {
		t.Fatalf("aliases len: got %d, want %d", got, want)
	}
	// Forbidden-field invariants survive: id/category/extends/executor
	// retain their shipped values.
	if merged.ID != "review" || merged.Category != CategoryCompute || merged.Extends != "compute" || merged.Executor != "agentgraph.ExecReview" {
		t.Errorf("forbidden fields drifted: %+v", merged)
	}
	// Attrs default merged.
	if v, ok := merged.Attrs["max_iterations"].Default.(int); !ok || v != 5 {
		t.Errorf("attrs.max_iterations.default: got %v", merged.Attrs["max_iterations"].Default)
	}
	if got := merged.Attrs["max_iterations"].Min; got == nil || *got != 1 {
		t.Errorf("attrs.max_iterations.min: got %v, want 1", got)
	}
	// Type is preserved (was unset on user, inherited from shipped).
	if got := merged.Attrs["max_iterations"].Type; got != AttrTypeInt {
		t.Errorf("attrs.max_iterations.type: got %q, want int", got)
	}
	// Defaults merged.
	if v, ok := merged.Defaults["max_iterations"].(int); !ok || v != 5 {
		t.Errorf("defaults.max_iterations: got %v", merged.Defaults["max_iterations"])
	}
}

// TestMergeUserOverlayForbiddenFields covers FR-021's forbidden fields.
// Each sub-case asserts ErrForbiddenOverrideField is wrapped.
func TestMergeUserOverlayForbiddenFields(t *testing.T) {
	t.Parallel()
	shipped := &Manifest{
		ID:       "model",
		Extends:  "compute",
		Category: CategoryCompute,
		Executor: "agentgraph.ExecModel",
		Attrs: map[string]AttrSpec{
			"max_tokens": {Type: AttrTypeInt},
		},
	}
	cases := []struct {
		name string
		user *Manifest
	}{
		{
			name: "category change",
			user: &Manifest{ID: "model", Category: CategoryControl},
		},
		{
			name: "extends change",
			user: &Manifest{ID: "model", Extends: "control"},
		},
		{
			name: "executor change",
			user: &Manifest{ID: "model", Executor: "agentgraph.ExecBogus"},
		},
		{
			name: "id change",
			user: &Manifest{ID: "different"},
		},
		{
			name: "attr type change",
			user: &Manifest{
				ID: "model",
				Attrs: map[string]AttrSpec{
					"max_tokens": {Type: AttrTypeString},
				},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := mergeUserOverlay(shipped, tc.user)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !errors.Is(err, ErrForbiddenOverrideField) {
				t.Errorf("expected ErrForbiddenOverrideField, got: %v", err)
			}
		})
	}
}

// TestMergeUserOverlayAttrTypeSameAllowed: a user manifest that re-
// declares an attr's type IDENTICAL to the shipped value is allowed
// (even though the shipped manifest's spec is what wins). This guards
// against an over-strict equality check.
func TestMergeUserOverlayAttrTypeSameAllowed(t *testing.T) {
	t.Parallel()
	shipped := &Manifest{
		ID: "model",
		Attrs: map[string]AttrSpec{
			"max_tokens": {Type: AttrTypeInt},
		},
	}
	user := &Manifest{
		ID: "model",
		Attrs: map[string]AttrSpec{
			"max_tokens": {Type: AttrTypeInt, Default: 8192},
		},
	}
	merged, err := mergeUserOverlay(shipped, user)
	if err != nil {
		t.Fatalf("mergeUserOverlay: %v", err)
	}
	if got, ok := merged.Attrs["max_tokens"].Default.(int); !ok || got != 8192 {
		t.Errorf("default: got %v, want 8192", merged.Attrs["max_tokens"].Default)
	}
}
