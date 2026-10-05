package catalog

// install-framework-01DOGF0B WP01 — pin P-1: a skill installed from the
// catalog shows as installed, because kind=skill installed state is read
// from the skill store (the consumer LiveRegister writes), not from
// <dataDir>/installed/ (which skill installs never touch).
//
// Pre-fix, Catalog_List built installed state from installed/ alone, so
// TestCatalogList_SkillInstalledStateFromSkillStore failed with
// installed=false for the stored skill (and WithSkillStore did not exist).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

func listServer(t *testing.T, items []corefleet.CatalogItem) *corefleet.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/catalog/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	ts := corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })
	return corefleet.NewClientForTesting(srv.URL)
}

// seedResidue writes an installed/ entry the way a previous release's
// Catalog_Install left it (payload + meta.json).
func seedResidue(t *testing.T, dataDir string, kind corefleet.CatalogItemKind, id, version string) {
	t.Helper()
	dir := filepath.Join(dataDir, "installed", string(kind), id+"@"+version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]string{
		"catalog_id": id, "version": version, "kind": string(kind), "slug": id,
	})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
}

func byID(t *testing.T, views []CatalogItemView) map[string]CatalogItemView {
	t.Helper()
	out := map[string]CatalogItemView{}
	for _, v := range views {
		out[v.ID] = v
	}
	return out
}

func TestCatalogList_SkillInstalledStateFromSkillStore(t *testing.T) {
	client := listServer(t, []corefleet.CatalogItem{
		{ID: "skill-cat-1", Kind: corefleet.CatalogKindSkill, Slug: "standup", Version: "1.0.0"},
		{ID: "skill-cat-2", Kind: corefleet.CatalogKindSkill, Slug: "review", Version: "1.0.0"},
	})
	dataDir := t.TempDir()
	store := coreslashcmd.NewSkillStore(dataDir)
	// Stored under the payload's skill ID ("standup"), NOT the catalog ID —
	// exactly what fleet.InstallSkill does for a SkillPublish'd skill.
	if err := store.Save(coreslashcmd.Skill{
		ID: "standup", CatalogID: "skill-cat-1", Version: "1.0.0",
		Source: coreslashcmd.SkillSourceCatalog, Trigger: "standup", Kind: coreslashcmd.KindText,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	api := NewAPI(client, nil, dataDir).WithSkillStore(store)
	views, err := api.Catalog_List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("Catalog_List: %v", err)
	}
	got := byID(t, views)
	if !got["skill-cat-1"].Installed {
		t.Error("skill-cat-1 is in the skill store but Catalog_List reports installed=false (P-1)")
	}
	if got["skill-cat-2"].Installed {
		t.Error("skill-cat-2 is not in the skill store but Catalog_List reports installed=true")
	}
}

// installed/ residue for a skill must not paint it installed: the store is
// the only authority. Otherwise the card shows Uninstall, which routes to
// SkillUninstall and fails with skill-not-found.
func TestCatalogList_SkillResidueInInstalledDirIsNotInstalled(t *testing.T) {
	client := listServer(t, []corefleet.CatalogItem{
		{ID: "skill-cat-3", Kind: corefleet.CatalogKindSkill, Slug: "ghost", Version: "1.0.0"},
	})
	dataDir := t.TempDir()
	seedResidue(t, dataDir, corefleet.CatalogKindSkill, "skill-cat-3", "1.0.0")

	api := NewAPI(client, nil, dataDir).WithSkillStore(coreslashcmd.NewSkillStore(dataDir))
	views, err := api.Catalog_List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("Catalog_List: %v", err)
	}
	if byID(t, views)["skill-cat-3"].Installed {
		t.Error("skill with only installed/ residue reported installed; the skill store is the authority")
	}
}

// Non-skill kinds keep reporting installed/ residue so the UI can offer its
// removal (WP02 labels it a download, not an install).
func TestCatalogList_NonSkillResidueStillReported(t *testing.T) {
	client := listServer(t, []corefleet.CatalogItem{
		{ID: "wf-1", Kind: corefleet.CatalogKindWorkflow, Slug: "wf", Version: "2.0.0"},
	})
	dataDir := t.TempDir()
	seedResidue(t, dataDir, corefleet.CatalogKindWorkflow, "wf-1", "2.0.0")

	api := NewAPI(client, nil, dataDir)
	views, err := api.Catalog_List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("Catalog_List: %v", err)
	}
	if !byID(t, views)["wf-1"].Installed {
		t.Error("workflow residue not reported; Uninstall (residue cleanup) would be unreachable")
	}
}
