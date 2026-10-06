package catalog

// install-framework-01DOGF0B WP02 — pin P-2 (backend half) at the RPC
// surface: Catalog_Install refuses with a named error and writes nothing.
// Pre-fix it returned nil and wrote installed/<kind>/<id>@<ver>/payload.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestCatalogInstall_RefusesUnconsumedKinds(t *testing.T) {
	for _, kind := range []corefleet.CatalogItemKind{
		corefleet.CatalogKindWorkflow, corefleet.CatalogKindPack, corefleet.CatalogKindBundle,
	} {
		t.Run(string(kind), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/catalog/it-1@1.0.0" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(corefleet.CatalogItem{
					ID: "it-1", Kind: kind, Slug: "it", Version: "1.0.0", PayloadBytes: []byte("opaque"),
				})
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

			dataDir := t.TempDir()
			api := NewAPI(corefleet.NewClientForTesting(srv.URL), nil, dataDir)
			err := api.Catalog_Install(context.Background(), "it-1", "1.0.0")
			if !errors.Is(err, corefleet.ErrCatalogKindNotInstallable) {
				t.Fatalf("Catalog_Install(%s) = %v, want ErrCatalogKindNotInstallable", kind, err)
			}
			if _, statErr := os.Stat(filepath.Join(dataDir, "installed")); !os.IsNotExist(statErr) {
				t.Errorf("refused install created installed/ (stat err %v)", statErr)
			}
		})
	}
}
