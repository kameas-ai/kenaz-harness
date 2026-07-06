package sites_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/sites"
)

func TestRecents_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	rootDir := "/home/user/my-site"

	if err := sites.RecordRecent(dir, rootDir, "my-site", "static", "dep-123", "https://my-site--org.sites.kameas.ai"); err != nil {
		t.Fatalf("RecordRecent: %v", err)
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatalf("LoadRecents: %v", err)
	}
	if len(recents) != 1 {
		t.Fatalf("len(recents) = %d, want 1", len(recents))
	}
	r := recents[0]
	if r.Name != "my-site" {
		t.Errorf("Name = %q, want %q", r.Name, "my-site")
	}
	if r.LastDeploymentID != "dep-123" {
		t.Errorf("LastDeploymentID = %q, want %q", r.LastDeploymentID, "dep-123")
	}
	if r.LastURL != "https://my-site--org.sites.kameas.ai" {
		t.Errorf("LastURL = %q, want url", r.LastURL)
	}
	if r.DeployedAt.IsZero() {
		t.Error("DeployedAt is zero")
	}
}

func TestRecents_UpdatePreservesLatest(t *testing.T) {
	dir := t.TempDir()
	root := "/home/user/site-a"

	if err := sites.RecordRecent(dir, root, "site-a", "dynamic", "dep-1", "https://url1"); err != nil {
		t.Fatal(err)
	}
	if err := sites.RecordRecent(dir, root, "site-a", "dynamic", "dep-2", "https://url2"); err != nil {
		t.Fatal(err)
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recents) != 1 {
		t.Fatalf("len(recents) = %d, want 1 (update should not create a second entry)", len(recents))
	}
	if recents[0].LastDeploymentID != "dep-2" {
		t.Errorf("LastDeploymentID = %q, want %q", recents[0].LastDeploymentID, "dep-2")
	}
}

func TestRecents_SortedByDeployedAtDesc(t *testing.T) {
	dir := t.TempDir()

	for i, name := range []string{"alpha", "beta", "gamma"} {
		root := "/home/user/" + name
		if err := sites.RecordRecent(dir, root, name, "static", "", ""); err != nil {
			t.Fatal(err)
		}
		// Add a short delay so DeployedAt values differ.
		_ = i
		time.Sleep(2 * time.Millisecond)
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recents) != 3 {
		t.Fatalf("len(recents) = %d, want 3", len(recents))
	}
	// Most recent (gamma) should be first.
	if recents[0].Name != "gamma" {
		t.Errorf("recents[0].Name = %q, want %q", recents[0].Name, "gamma")
	}
	if recents[2].Name != "alpha" {
		t.Errorf("recents[2].Name = %q, want %q", recents[2].Name, "alpha")
	}
}

func TestRecents_CapAt10_OldestEvicted(t *testing.T) {
	dir := t.TempDir()

	// Insert 12 entries; expect only 10 remain.
	for i := range 12 {
		root := filepath.Join("/home/user", fmt.Sprintf("site-%02d", i))
		name := fmt.Sprintf("site-%02d", i)
		if err := sites.RecordRecent(dir, root, name, "static", "", ""); err != nil {
			t.Fatalf("RecordRecent %d: %v", i, err)
		}
		time.Sleep(1 * time.Millisecond) // distinct DeployedAt
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recents) != 10 {
		t.Errorf("len(recents) = %d, want 10 (cap)", len(recents))
	}

	// The two oldest (site-00, site-01) must be gone.
	for _, r := range recents {
		if r.Name == "site-00" || r.Name == "site-01" {
			t.Errorf("evicted site %q still present in recents", r.Name)
		}
	}
}

func TestRecents_MissingFile_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// Don't write anything.
	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatalf("LoadRecents on missing file = error %v, want nil", err)
	}
	if len(recents) != 0 {
		t.Errorf("len(recents) = %d, want 0", len(recents))
	}
}

func TestRecents_CorruptFile_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	sitesDir := filepath.Join(dir, "sites")
	if err := os.MkdirAll(sitesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Write garbage JSON.
	if err := os.WriteFile(filepath.Join(sitesDir, "recent.json"), []byte("NOT JSON"), 0o600); err != nil {
		t.Fatal(err)
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatalf("LoadRecents on corrupt file = error %v, want nil", err)
	}
	if len(recents) != 0 {
		t.Errorf("len(recents) = %d, want 0 on corrupt file", len(recents))
	}
}

func TestRecents_CorruptFile_RecoverOnNextRecord(t *testing.T) {
	dir := t.TempDir()
	sitesDir := filepath.Join(dir, "sites")
	if err := os.MkdirAll(sitesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Start with corrupt file.
	if err := os.WriteFile(filepath.Join(sitesDir, "recent.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}

	// RecordRecent should reset and succeed.
	if err := sites.RecordRecent(dir, "/home/user/new-site", "new-site", "static", "dep-1", ""); err != nil {
		t.Fatalf("RecordRecent after corrupt file: %v", err)
	}

	recents, err := sites.LoadRecents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recents) != 1 {
		t.Errorf("len(recents) = %d, want 1", len(recents))
	}
}

func TestRecents_EmptyDataDir(t *testing.T) {
	recents, err := sites.LoadRecents("")
	if err != nil {
		t.Fatalf("LoadRecents(\"\") = %v, want nil error", err)
	}
	if recents != nil {
		t.Errorf("LoadRecents(\"\") = %v, want nil", recents)
	}
}

