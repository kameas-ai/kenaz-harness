package rpc

// install-framework-01DOGF0B review follow-up (F1): the composition wiring
// in New() — catalogAPI.(*catalogview.API).WithSkillStore(skillStore) — is
// what makes the Marketplace skill badge read the consumer. Every unit test
// of the catalog view calls WithSkillStore itself, so deleting the three
// wiring lines in api.go left every suite green while the live badge
// regressed to "Install". This test boots a real chassis, installs a skill
// through the production path (Slashcmd_SkillInstall → install framework →
// fleet.FetchCatalogItem / CatalogSignatureVerdict / InstallSkillPayload →
// LiveRegister → SkillStore), and asserts
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
	"github.com/kameas-ai/kenaz-harness/core/install"
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
	// install-framework-01DOGF0B WP05: the production skill install is the
	// Slashcmd_SkillInstall binding, which routes through the install
	// framework (fetch in Verify → single verifier → LiveRegister).
	b := NewBindings(api)
	sub, cancelSub := api.EventBus().Subscribe(4, install.TopicCapabilityInstalled)
	defer cancelSub()
	if err := b.Slashcmd_SkillInstall(catID, ver); err != nil {
		t.Fatalf("Slashcmd_SkillInstall: %v", err)
	}
	select {
	case ev := <-sub:
		got, ok := ev.Payload.(install.Event)
		if !ok || got.Kind != install.KindSkill || got.ID != catID || got.Verified {
			t.Fatalf("capability:installed payload = %#v, want an unverified (register C-2) skill install of %s", ev.Payload, catID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no capability:installed on the event bus — the skill binding bypassed the install framework")
	}
	cl, err := b.Capability_List(install.Filter{Kind: install.KindSkill})
	if err != nil {
		t.Fatalf("Capability_List: %v", err)
	}
	if len(cl.Items) != 1 || cl.Items[0].ID != catID || !cl.Items[0].State.Installed {
		t.Fatalf("Capability_List(skill) = %+v, want %s installed (consumer: slash registry)", cl.Items, catID)
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
