package fleet_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// stubTestTokens installs a fake token set and registers a cleanup to clear it.
// Skips the test if the OS keychain is unavailable (CI without a keychain daemon).
func stubTestTokens(t *testing.T) {
	t.Helper()
	ts := fleet.TokenSet{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := fleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = fleet.ClearTokens() })
}

// newTestClient returns a *Client wired to the given httptest.Server.
// Uses BuildClientForTesting from export_test.go to bypass env-profile.
func newTestClient(t *testing.T, srv *httptest.Server) *fleet.Client {
	t.Helper()
	stubTestTokens(t)
	return fleet.BuildClientForTesting(srv.URL, srv.Client())
}

// ----- nop client behaviour (FR-301) -----

func TestSites_NopClient_AllMethodsReturnErrFleetDisabled(t *testing.T) {
	nop := fleet.NewNopClient()
	ctx := t.Context()

	t.Run("SiteCreate", func(t *testing.T) {
		_, err := nop.SiteCreate(ctx, "slug", "static")
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteCreate: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteDeploy", func(t *testing.T) {
		_, err := nop.SiteDeploy(ctx, "site-id", []byte("{}"), strings.NewReader("bundle"), "abc123")
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteDeploy: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SitesList", func(t *testing.T) {
		_, err := nop.SitesList(ctx)
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SitesList: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteStatus", func(t *testing.T) {
		_, err := nop.SiteStatus(ctx, "site-id")
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteStatus: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteLogs", func(t *testing.T) {
		_, err := nop.SiteLogs(ctx, "site-id", 100)
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteLogs: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteDelete", func(t *testing.T) {
		err := nop.SiteDelete(ctx, "site-id")
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteDelete: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteEnvSet", func(t *testing.T) {
		err := nop.SiteEnvSet(ctx, "site-id", map[string]string{"K": "V"})
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteEnvSet: got %v, want ErrFleetDisabled", err)
		}
	})
	t.Run("SiteEnvList", func(t *testing.T) {
		_, err := nop.SiteEnvList(ctx, "site-id")
		if !errors.Is(err, fleet.ErrFleetDisabled) {
			t.Errorf("SiteEnvList: got %v, want ErrFleetDisabled", err)
		}
	})
}

// ----- SiteCreate happy path -----

func TestSiteCreate_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sites" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fleet.SiteRecord{
			ID:   "site-abc",
			Slug: "my-site",
			Kind: "static",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	rec, err := c.SiteCreate(t.Context(), "my-site", "static")
	if err != nil {
		t.Fatalf("SiteCreate: %v", err)
	}
	if rec.ID != "site-abc" {
		t.Errorf("ID = %q, want %q", rec.ID, "site-abc")
	}
}

// ----- SiteCreate error mappings -----

func TestSiteCreate_409_ErrSlugConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "slug_conflict", "message": "slug taken"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteCreate(t.Context(), "taken-slug", "static")
	if !errors.Is(err, fleet.ErrSlugConflict) {
		t.Errorf("got %v, want ErrSlugConflict", err)
	}
}

func TestSiteCreate_403_ErrCapabilityNotInTier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "capability_not_in_tier", "message": "enterprise only"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteCreate(t.Context(), "my-site", "static")
	if !errors.Is(err, fleet.ErrCapabilityNotInTier) {
		t.Errorf("got %v, want ErrCapabilityNotInTier", err)
	}
}

func TestSiteCreate_422_OrgSlugRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "org_slug_required", "message": "set org slug first"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteCreate(t.Context(), "my-site", "static")
	if !errors.Is(err, fleet.ErrOrgSlugRequired) {
		t.Errorf("got %v, want ErrOrgSlugRequired", err)
	}
}

func TestSiteCreate_422_InvalidSlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "invalid_slug", "message": "slug is reserved"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteCreate(t.Context(), "admin", "static")
	if !errors.Is(err, fleet.ErrInvalidSlug) {
		t.Errorf("got %v, want ErrInvalidSlug", err)
	}
}

// ----- SiteDeploy -----

func TestSiteDeploy_OK(t *testing.T) {
	var gotSHA, gotManifest string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/deployments") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		gotSHA = r.Header.Get("X-Kameas-Bundle-Sha256")
		gotManifest = r.Header.Get("X-Kameas-Manifest")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fleet.DeploymentRecord{
			ID:     "dep-xyz",
			SiteID: "site-abc",
			Status: "live",
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	rec, err := c.SiteDeploy(t.Context(), "site-abc", []byte(`{"version":1}`), strings.NewReader("bundle-bytes"), "deadbeef")
	if err != nil {
		t.Fatalf("SiteDeploy: %v", err)
	}
	if rec.ID != "dep-xyz" {
		t.Errorf("ID = %q, want %q", rec.ID, "dep-xyz")
	}
	if gotSHA != "deadbeef" {
		t.Errorf("X-Kameas-Bundle-Sha256 = %q, want %q", gotSHA, "deadbeef")
	}
	if gotManifest == "" {
		t.Error("X-Kameas-Manifest header not sent")
	}
}

func TestSiteDeploy_413_ErrBundleTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "bundle_too_large", "message": "exceeded 512MiB"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteDeploy(t.Context(), "site-abc", []byte(`{}`), strings.NewReader("x"), "sha")
	if !errors.Is(err, fleet.ErrBundleTooLarge) {
		t.Errorf("got %v, want ErrBundleTooLarge", err)
	}
}

func TestSiteDeploy_422_DigestMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "bundle_digest_mismatch", "message": "sha256 mismatch"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteDeploy(t.Context(), "site-abc", []byte(`{}`), strings.NewReader("x"), "wrong")
	if !errors.Is(err, fleet.ErrBundleDigestMismatch) {
		t.Errorf("got %v, want ErrBundleDigestMismatch", err)
	}
}

// ----- SitesList -----

func TestSitesList_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sites" {
			http.Error(w, "wrong", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]fleet.SiteRecord{
			{ID: "s1", Slug: "site-one"},
			{ID: "s2", Slug: "site-two"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	sites, err := c.SitesList(t.Context())
	if err != nil {
		t.Fatalf("SitesList: %v", err)
	}
	if len(sites) != 2 {
		t.Errorf("len(sites) = %d, want 2", len(sites))
	}
}

// ----- SiteStatus -----

func TestSiteStatus_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fleet.SiteRecord{ID: "site-abc", Slug: "mysite", URL: "https://mysite--org.sites.kameas.ai"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	rec, err := c.SiteStatus(t.Context(), "site-abc")
	if err != nil {
		t.Fatalf("SiteStatus: %v", err)
	}
	if rec.URL == "" {
		t.Error("URL is empty")
	}
}

func TestSiteStatus_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteStatus(t.Context(), "missing")
	if !errors.Is(err, fleet.ErrSiteNotFound) {
		t.Errorf("got %v, want ErrSiteNotFound", err)
	}
}

// ----- SiteDelete -----

func TestSiteDelete_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.SiteDelete(t.Context(), "site-abc"); err != nil {
		t.Fatalf("SiteDelete: %v", err)
	}
}

// ----- SiteEnvSet / SiteEnvList -----

func TestSiteEnvSet_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.SiteEnvSet(t.Context(), "site-abc", map[string]string{"DB_URL": "postgres://..."}); err != nil {
		t.Fatalf("SiteEnvSet: %v", err)
	}
}

func TestSiteEnvList_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]fleet.SiteEnvEntry{
			{Name: "DB_URL"},
			{Name: "API_KEY"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	entries, err := c.SiteEnvList(t.Context(), "site-abc")
	if err != nil {
		t.Fatalf("SiteEnvList: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("len(entries) = %d, want 2", len(entries))
	}
}

func TestSiteEnvList_429_QuotaExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "quota_exceeded", "message": "site count limit"})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.SiteEnvList(t.Context(), "site-abc")
	if !errors.Is(err, fleet.ErrQuotaExceeded) {
		t.Errorf("got %v, want ErrQuotaExceeded", err)
	}
}

// ----- SiteLogs -----

func TestSiteLogs_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/logs") {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("build log line 1\nbuild log line 2\n"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	logs, err := c.SiteLogs(t.Context(), "site-abc", 100)
	if err != nil {
		t.Fatalf("SiteLogs: %v", err)
	}
	if !strings.Contains(logs, "build log") {
		t.Errorf("logs = %q, want build log content", logs)
	}
}
