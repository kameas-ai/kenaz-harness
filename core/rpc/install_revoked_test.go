package rpc

// skill-library-01SKLIB01 WP01 (AC-3): a new install of a REVOKED catalog
// version fails through the real install framework with the named error and
// the user-facing copy — never "status 410" — and installs nothing.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/install"
	capabilitiesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

type revokedTestInstaller struct{ installs int }

func (r *revokedTestInstaller) SkillInstallPayload(context.Context, string, string, []byte) error {
	r.installs++
	return nil
}
func (r *revokedTestInstaller) SkillUninstall(context.Context, string) error { return nil }

func TestInstallFramework_RevokedCatalogVersionIsNamedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/catalog/r1@2.0.0" {
			// fleet handlers_catalog.go handleCatalogFetch, live shape.
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"code":"item_revoked","message":"this version was revoked and can no longer be downloaded","details":{"catalog_id":"r1","version":"2.0.0"}}`))
			return
		}
		if r.URL.Path == "/api/v1/catalog/r2@1.0.0" {
			// A catalog:manage caller (org admin) gets 200 + the payload
			// for forensic review — still revoked, still not installable.
			_, _ = w.Write([]byte(`{"id":"r2","kind":"skill","slug":"s","version":"1.0.0","visibility":"team","description":"","payload":"eyJpZCI6InMiLCJ0cmlnZ2VyIjoicyJ9","signature":"","mandated":false,"published_at":"2026-10-01T00:00:00Z","lint_blocking":[],"lint_warnings":[],"mandated_reviewed":false,"payload_sha256":"x","size_bytes":22,"lifecycle":"revoked"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	if err := corefleet.SaveTokens(corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	inst := &revokedTestInstaller{}
	store := coreslashcmd.NewSkillStore(t.TempDir())
	fw := install.New(nil, installSignatureVerifier(func() string { return "" }))
	if err := fw.Register(install.KindSkill, capabilitiesview.NewSkillProvider(
		fleetCatalogSeam{client: corefleet.NewClientForTesting(srv.URL)}, store, nil, inst)); err != nil {
		t.Fatal(err)
	}

	_, err := fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "r1", Version: "2.0.0"}, install.Inputs{})
	if !errors.Is(err, install.ErrRevoked) || !errors.Is(err, corefleet.ErrCatalogItemRevoked) {
		t.Fatalf("err = %v, want install.ErrRevoked wrapping fleet.ErrCatalogItemRevoked", err)
	}
	if msg := err.Error(); strings.Contains(msg, "410") || !strings.Contains(msg, "revoked by your org") {
		t.Errorf("message %q: want the revoked-by-your-org copy, no raw status", msg)
	}
	if inst.installs != 0 {
		t.Errorf("a revoked version reached the consumer %d time(s)", inst.installs)
	}

	// An admin's 200-with-lifecycle=revoked fetch is refused the same way.
	_, err = fw.Install(context.Background(), install.Ref{Kind: install.KindSkill, ID: "r2", Version: "1.0.0"}, install.Inputs{})
	if !errors.Is(err, install.ErrRevoked) || inst.installs != 0 {
		t.Fatalf("admin fetch of a revoked version: err=%v installs=%d, want ErrRevoked and nothing installed", err, inst.installs)
	}
}
