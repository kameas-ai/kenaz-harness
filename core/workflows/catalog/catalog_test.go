package catalog_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/catalog"
)

// TestCatalog_ListReturnsBuiltins confirms that a catalog with no Store
// returns the embedded builtin entries.
func TestCatalog_ListReturnsBuiltins(t *testing.T) {
	t.Parallel()
	cat := catalog.New(catalog.Config{})
	entries, err := cat.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one builtin entry")
	}
	// plan_implement_review is the only builtin in WP01.
	found := false
	for _, e := range entries {
		if e.ID == "plan_implement_review" {
			found = true
			if e.Name == "" {
				t.Error("entry.Name must not be empty")
			}
			if e.Version == "" {
				t.Error("entry.Version must not be empty")
			}
		}
	}
	if !found {
		t.Error("plan_implement_review not in catalog List")
	}
}

// TestCatalog_GetReturnsYAMLAndEntry confirms that Get returns both the
// YAML source and a populated Entry.
func TestCatalog_GetReturnsYAMLAndEntry(t *testing.T) {
	t.Parallel()
	cat := catalog.New(catalog.Config{})
	doc, err := cat.Get(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.YAMLSource == "" {
		t.Error("YAMLSource must not be empty")
	}
	if doc.Entry.ID != "plan_implement_review" {
		t.Errorf("Entry.ID: got %q want plan_implement_review", doc.Entry.ID)
	}
	if doc.Entry.InstallStatus == "" {
		t.Error("Entry.InstallStatus must not be empty")
	}
}

// TestCatalog_GetUnknownReturnsErrNotFound confirms the correct sentinel.
func TestCatalog_GetUnknownReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	cat := catalog.New(catalog.Config{})
	_, err := cat.Get(context.Background(), "no-such-workflow")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestCatalog_InstallWithoutStoreErrors verifies the ErrStoreUnavailable path.
func TestCatalog_InstallWithoutStoreErrors(t *testing.T) {
	t.Parallel()
	cat := catalog.New(catalog.Config{}) // no Store
	_, err := cat.Install(context.Background(), "plan_implement_review")
	if err == nil {
		t.Fatal("expected ErrStoreUnavailable, got nil")
	}
}
