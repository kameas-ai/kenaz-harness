package rpc

// install-framework-01DOGF0B review follow-up (F1): the composition wiring
// in New() — catalogAPI.(*catalogview.API).WithSkillStore(skillStore) — is
// what makes the Marketplace skill badge read the consumer. Every unit test
// of the catalog view calls WithSkillStore itself, so deleting the three
// wiring lines in api.go left every suite green while the live badge
// regressed to "Install". This test boots a real chassis, installs a skill
// through the production slashcmd path (Slash().SkillInstall →
// fleet.InstallSkill → LiveRegister → SkillStore), and asserts
// Catalog().Catalog_List reports it installed — with no test-side
// WithSkillStore call anywhere.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	catalogview "github.com/kameas-ai/kenaz-harness/core/rpc/views/catalog"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

func TestChassis_CatalogList_SkillInstalledStateIsWiredToSkillStore(t *testing.T) {
	// Point the real chassis fleet client at a fake catalog server: the
	// "local" profile's SPA host resolves through the seeded config cache
	// to this server's API base.
	t.Setenv("HARNESS_FLEET_DISABLED", "")
	t.Setenv("KENAZ_HARNESS_ENV", "local")

	const catID, ver = "skill-cat-wire", "1.0.0"
	payload, _ := json.Marshal(coreslashcmd.Skill{
		ID: "wiretest", Trigger: "wiretest", Kind: coreslashcmd.KindText,
		Body: "hello", Source: coreslashcmd.SkillSourceCatalog,
	})
	item := corefleet.CatalogItem{
		ID: catID, Kind: corefleet.CatalogKindSkill, Slug: "wiretest", Version: ver,
		PayloadBytes: payload,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/list":
			meta := item
			meta.PayloadBytes = nil
			_ = json.NewEncoder(w).Encode([]corefleet.CatalogItem{meta})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/"+catID+"@"+ver:
			_ = json.NewEncoder(w).Encode(item)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	spa := corefleet.ResolveProfile().FleetBaseURL
	corefleet.SeedFleetConfigForTesting(spa, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	t.Cleanup(func() { corefleet.InvalidateFleetConfig("", spa) })
	ts := corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	sandboxUserConfigDir(t)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)

	ctx := context.Background()
	if err := api.Slash().SkillInstall(ctx, catID, ver); err != nil {
		t.Fatalf("Slash().SkillInstall: %v", err)
	}
	views, err := api.Catalog().Catalog_List(ctx, catalogview.CatalogFilter{})
	if err != nil {
		t.Fatalf("Catalog().Catalog_List: %v", err)
	}
	if len(views) != 1 || views[0].ID != catID {
		t.Fatalf("Catalog_List = %+v, want the one seeded skill", views)
	}
	if !views[0].Installed {
		t.Error("skill installed via the slashcmd path reports installed=false through the chassis Catalog(): " +
			"the WithSkillStore wiring in rpc.New is missing (P-1 regresses in the live app)")
	}
}
