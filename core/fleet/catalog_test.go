package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── fake fleet catalog server ─────────────────────────────────────────────────
//
// The fake speaks fleet's REAL wire shapes (WP04): before, it encoded the
// harness's own CatalogItem as a bare array — exactly the shape the harness
// decoded — which hid that fleet sends {"items":[...]} keyed "id" (audit
// §0-C). The structs below mirror kenaz-fleet service/handlers_catalog.go.

// fleetCatalogKinds mirrors handlers_catalog.go:66-71 catalogKinds; publish
// of any other kind is 400 invalid_kind.
var fleetCatalogKinds = map[string]bool{"workflow": true, "pack": true, "bundle": true, "skill": true}

// fleetCatalogListResponse mirrors handlers_catalog.go:110-112.
type fleetCatalogListResponse struct {
	Items []fleetCatalogItemMeta `json:"items"`
}

// fleetCatalogItemMeta mirrors handlers_catalog.go:115-133 CatalogItemMetaAPI.
type fleetCatalogItemMeta struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"owner_user_id"`
	Kind        string    `json:"kind"`
	Slug        string    `json:"slug"`
	Version     string    `json:"version"`
	Visibility  string    `json:"visibility"`
	Description string    `json:"description"`
	Signature   string    `json:"signature"`
	Mandated    bool      `json:"mandated"`
	PublishedAt time.Time `json:"published_at"`
}

// fleetCatalogFetchResponse mirrors handlers_catalog.go:135-157 CatalogFetchResponse.
type fleetCatalogFetchResponse struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Slug        string    `json:"slug"`
	Version     string    `json:"version"`
	Visibility  string    `json:"visibility"`
	Description string    `json:"description"`
	Payload     []byte    `json:"payload"`
	Signature   string    `json:"signature"`
	Mandated    bool      `json:"mandated"`
	PublishedAt time.Time `json:"published_at"`
}

type fakeCatalogServer struct {
	published []publishRequest
	items     map[string]CatalogItem // keyed by catalogID@version
	deleted   []string
	// forbidUnpublish, when true, makes every DELETE return 403 — the
	// "not the owner and not an admin" case AC-020 tests.
	forbidUnpublish bool
}

func (f *fakeCatalogServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/publish":
		var req publishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !fleetCatalogKinds[string(req.Kind)] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"invalid_kind","message":"kind must be one of workflow, pack, bundle, skill"}`))
			return
		}
		f.published = append(f.published, req)
		id := req.Slug + "-id"
		if f.items == nil {
			f.items = make(map[string]CatalogItem)
		}
		item := CatalogItem{
			ID:           id,
			Kind:         req.Kind,
			Slug:         req.Slug,
			Version:      req.Version,
			Description:  req.Description,
			Visibility:   req.Visibility,
			PayloadBytes: req.Payload,
			Signature:    req.Signature,
			PublishedAt:  time.Now(),
		}
		f.items[id+"@"+req.Version] = item
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"catalog_id":   id,
			"version":      req.Version,
			"published_at": item.PublishedAt,
		})

	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/list":
		if k := r.URL.Query().Get("kind"); k != "" && !fleetCatalogKinds[k] {
			w.WriteHeader(http.StatusBadRequest) // handlers_catalog.go: invalid kind filter
			return
		}
		out := fleetCatalogListResponse{Items: []fleetCatalogItemMeta{}}
		for _, it := range f.items {
			if k := r.URL.Query().Get("kind"); k != "" && string(it.Kind) != k {
				continue
			}
			out.Items = append(out.Items, fleetCatalogItemMeta{
				ID: it.ID, Kind: string(it.Kind), Slug: it.Slug, Version: it.Version,
				Visibility: string(it.Visibility), Description: it.Description,
				Signature: it.Signature, PublishedAt: it.PublishedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/v1/catalog/"):
		key := r.URL.Path[len("/api/v1/catalog/"):]
		if it, ok := f.items[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fleetCatalogFetchResponse{
				ID: it.ID, Kind: string(it.Kind), Slug: it.Slug, Version: it.Version,
				Visibility: string(it.Visibility), Description: it.Description,
				Payload: it.PayloadBytes, Signature: it.Signature, PublishedAt: it.PublishedAt,
			})
		} else {
			http.NotFound(w, r)
		}

	case r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/v1/catalog/"):
		if f.forbidUnpublish {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		id := r.URL.Path[len("/api/v1/catalog/"):]
		f.deleted = append(f.deleted, id)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestCatalog_PublishRoundTrip(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	dataDir := t.TempDir()
	signer, err := NewDeviceSigner(dataDir)
	if err != nil {
		t.Fatalf("NewDeviceSigner: %v", err)
	}

	payload := []byte(`{"nodes":[],"edges":[]}`)
	item, err := c.Publish(context.Background(), signer,
		CatalogKindWorkflow, "my-workflow", "1.0.0",
		"A test workflow", CatalogVisTeam, payload)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if item.ID == "" {
		t.Error("expected non-empty catalog ID")
	}
	if item.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0", item.Version)
	}
	if item.Signature == "" {
		t.Error("expected non-empty signature")
	}
	if len(fake.published) != 1 {
		t.Errorf("server received %d publishes, want 1", len(fake.published))
	}
}

func TestCatalog_ListAfterPublish(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	signer, _ := NewDeviceSigner(t.TempDir())

	payload := []byte("pack-content")
	_, err := c.Publish(context.Background(), signer,
		CatalogKindPack, "test-pack", "0.1.0", "desc", CatalogVisTeam, payload)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	items, err := c.List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one item in list")
	}
	// List items are keyed "id" on the wire; the ID must survive the decode
	// (it arrived empty when the harness read "catalog_id").
	if items[0].ID != "test-pack-id" || items[0].Kind != CatalogKindPack {
		t.Errorf("list item = %+v, want id test-pack-id kind pack", items[0])
	}
	// Verify metadata-only: list items must not carry payload.
	for _, it := range items {
		if len(it.PayloadBytes) > 0 {
			t.Errorf("list item %q should not carry PayloadBytes", it.ID)
		}
	}
}

// TestCatalog_Install_RefusesEveryKind — install-framework-01DOGF0B WP02,
// pin P-2 (backend half). Pre-fix, Install wrote installed/<kind>/... and
// returned nil for every kind although nothing consumes that directory; this
// test then failed on the nil error and on the written payload.
func TestCatalog_Install_RefusesEveryKind(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	signer, _ := NewDeviceSigner(t.TempDir())

	for _, kind := range []CatalogItemKind{
		CatalogKindWorkflow, CatalogKindPack, CatalogKindBundle, CatalogKindSkill,
	} {
		t.Run(string(kind), func(t *testing.T) {
			item, err := c.Publish(context.Background(), signer,
				kind, "refuse-"+string(kind), "1.2.3", "desc", CatalogVisTeam, []byte("content"))
			if err != nil {
				t.Fatalf("Publish: %v", err)
			}
			dataDir := t.TempDir()
			err = c.Install(context.Background(), dataDir, "", item.ID, "1.2.3")
			if !errors.Is(err, ErrCatalogKindNotInstallable) {
				t.Fatalf("Install(%s) = %v, want ErrCatalogKindNotInstallable", kind, err)
			}
			if !strings.Contains(err.Error(), string(kind)) {
				t.Errorf("refusal %q does not name the kind %q", err, kind)
			}
			if _, statErr := os.Stat(filepath.Join(dataDir, "installed")); !os.IsNotExist(statErr) {
				t.Errorf("Install(%s) created installed/ (stat err %v); a refused install must write nothing", kind, statErr)
			}
			if got, _ := InstalledItems(dataDir); len(got) != 0 {
				t.Errorf("InstalledItems after refused install = %v, want none", got)
			}
		})
	}
}

// seedInstalledResidue writes installed/ exactly as a pre-WP02 release's
// Install did (payload + meta.json), so cleanup is tested against real
// residue rather than something this release can no longer produce.
func seedInstalledResidue(t *testing.T, dataDir string, kind CatalogItemKind, id, version string) {
	t.Helper()
	dir := installBasePath(dataDir, kind, id, version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.MarshalIndent(map[string]string{
		"catalog_id": id, "version": version, "kind": string(kind), "slug": id, "signature": "",
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalog_InstalledItems_ReadsPriorReleaseResidue(t *testing.T) {
	dataDir := t.TempDir()
	seedInstalledResidue(t, dataDir, CatalogKindWorkflow, "wf-old", "1.2.3")
	installed, err := InstalledItems(dataDir)
	if err != nil {
		t.Fatalf("InstalledItems: %v", err)
	}
	if len(installed) != 1 || installed[0].ID != "wf-old" || installed[0].Kind != CatalogKindWorkflow {
		t.Errorf("InstalledItems = %+v, want the one seeded workflow", installed)
	}
}

func TestCatalog_TamperedPayloadRejected(t *testing.T) {
	// Test verifyCatalogSignature directly: signing over A, verifying over B
	// with a matching public key must return ErrCatalogSignatureMismatch.
	signerDataDir := t.TempDir()
	signer, err := NewDeviceSigner(signerDataDir)
	if err != nil {
		t.Fatalf("NewDeviceSigner: %v", err)
	}

	origPayload := []byte("original-payload")
	sig, _, err := signer.Sign(origPayload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Extract raw ed25519 public key bytes from the DeviceSigner's private key.
	pubKey := signer.privKey.Public().(ed25519.PublicKey)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)

	// Verify against tampered content — must fail.
	verifyErr := verifyCatalogSignature(pubKeyB64, []byte("tampered-content"), sig)
	if !errors.Is(verifyErr, ErrCatalogSignatureMismatch) {
		t.Errorf("expected ErrCatalogSignatureMismatch for tampered payload, got %v", verifyErr)
	}

	// Verify against original content — must succeed.
	if err := verifyCatalogSignature(pubKeyB64, origPayload, sig); err != nil {
		t.Errorf("verification of original payload should succeed: %v", err)
	}
}

func TestCatalog_Uninstall(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	// Uninstall is the cleanup path for residue a pre-WP02 release's
	// Install left behind (install-framework-01DOGF0B WP02).
	item := CatalogItem{ID: "to-remove-id"}
	dataDir := t.TempDir()
	seedInstalledResidue(t, dataDir, CatalogKindWorkflow, item.ID, "1.0.0")
	if got, _ := InstalledItems(dataDir); len(got) != 1 {
		t.Fatalf("fixture: InstalledItems = %v, want the seeded item", got)
	}

	// Uninstall.
	if err := c.Uninstall(dataDir, CatalogKindWorkflow, item.ID, "1.0.0"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// Idempotent second call.
	if err := c.Uninstall(dataDir, CatalogKindWorkflow, item.ID, "1.0.0"); err != nil {
		t.Errorf("second Uninstall should be idempotent: %v", err)
	}

	// Item should no longer appear in InstalledItems.
	installed, _ := InstalledItems(dataDir)
	for _, it := range installed {
		if it.ID == item.ID {
			t.Errorf("item %q still appears in InstalledItems after Uninstall", item.ID)
		}
	}
}

// ── AC-020 (fleet-enforcement-truth-01PMZ505 WP11) ──────────────────────────
//
// "a publisher can withdraw, and the refusal is honest." Two assertions:
// (a) Unpublish on an item the server accepts removes it; (b) a 403 maps
// to ErrCatalogForbidden, not ErrCatalogNotInTier — the mis-mapping this
// WP fixes, register C-3/C-8.

func TestCatalog_Unpublish_OK(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	signer, _ := NewDeviceSigner(t.TempDir())
	item, err := c.Publish(context.Background(), signer,
		CatalogKindWorkflow, "to-withdraw", "1.0.0", "desc", CatalogVisOrgPublic, []byte("data"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := c.Unpublish(context.Background(), item.ID); err != nil {
		t.Fatalf("Unpublish: %v", err)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != item.ID {
		t.Errorf("server deleted = %v, want [%s]", fake.deleted, item.ID)
	}
}

// TestCatalog_Unpublish_ForbiddenMapsToForbiddenNotTier is the mutation
// this WP exists to fix: before it, a 403 on DELETE mapped to
// ErrCatalogNotInTier (a billing error) — telling a publisher trying to
// withdraw someone else's item to upgrade their subscription, when the
// real reason is that they are not the owner and not an admin.
func TestCatalog_Unpublish_ForbiddenMapsToForbiddenNotTier(t *testing.T) {
	fake := &fakeCatalogServer{forbidUnpublish: true}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	err := c.Unpublish(context.Background(), "someone-elses-item")
	if err == nil {
		t.Fatal("Unpublish on a 403: want error, got nil")
	}
	if !errors.Is(err, ErrCatalogForbidden) {
		t.Errorf("Unpublish 403 error = %v, want to wrap ErrCatalogForbidden", err)
	}
	if errors.Is(err, ErrCatalogNotInTier) {
		t.Error("Unpublish 403 must NOT map to ErrCatalogNotInTier — that tells " +
			"a publisher to upgrade their subscription for an item they don't own")
	}
}

// TestCatalog_Publish_ForbiddenStillMeansTier is the guard against
// over-correcting: Publish's own 403 handling (a genuinely different
// server route/semantics) must be left alone — C-3/C-8 fixes Unpublish
// only.
func TestCatalog_Publish_ForbiddenStillMeansTier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tier", http.StatusForbidden)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	signer, _ := NewDeviceSigner(t.TempDir())

	_, err := c.Publish(context.Background(), signer,
		CatalogKindWorkflow, "x", "1.0.0", "desc", CatalogVisOrgPublic, []byte("data"))
	if !errors.Is(err, ErrCatalogNotInTier) {
		t.Errorf("Publish 403 error = %v, want ErrCatalogNotInTier (unchanged by WP11)", err)
	}
}

func TestCatalog_PayloadTooLarge(t *testing.T) {
	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, "http://127.0.0.1:19999") // unreachable; error happens before HTTP
	signer, _ := NewDeviceSigner(t.TempDir())
	big := make([]byte, catalogMaxPayloadBytes+1)
	_, err := c.Publish(context.Background(), signer,
		CatalogKindBundle, "big-bundle", "1.0.0", "desc", CatalogVisTeam, big)
	if err != ErrCatalogPayloadTooLarge {
		t.Errorf("expected ErrCatalogPayloadTooLarge, got %v", err)
	}
}

// TestCatalog_Uninstall_RefusesTraversal — review F8. Uninstall is now the
// promoted "Remove download" path; kind/catalogID/version arrive from the
// frontend and feed filepath.Join → os.RemoveAll. Pre-fix, a version of
// "/../../../../victim" removed <root>/victim, outside installed/ entirely.
// Each input must be a single clean segment.
func TestCatalog_Uninstall_RefusesTraversal(t *testing.T) {
	var c *Client // Uninstall touches no client state
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	victim := filepath.Join(root, "victim")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "installed", "workflow"), 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ kind, id, ver string }{
		// Joins to <root>/victim: the case that removed a real directory
		// outside installed/ before the guard.
		{"workflow", "x", "/../../../../victim"},
		{"../../..", "victim", "1"},
		{"workflow", "../../../victim", "1"},
		{"workflow", `..\..\victim`, "1"},
		{"..", "..", "x"},
		{"", "a", "1"},
		{"workflow", "", "1"},
		{"workflow", "a", ""},
		{"workflow", ".", "1"},
		{"workflow/sub", "a", "1"},
	}
	for _, tc := range cases {
		err := c.Uninstall(dataDir, CatalogItemKind(tc.kind), tc.id, tc.ver)
		if !errors.Is(err, ErrCatalogInvalidPathSegment) {
			t.Errorf("Uninstall(%q, %q, %q) = %v, want ErrCatalogInvalidPathSegment", tc.kind, tc.id, tc.ver, err)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim directory outside installed/ was removed: %v", err)
	}
	if err := c.Uninstall("", CatalogKindWorkflow, "a", "1"); err == nil {
		t.Error("Uninstall with empty dataDir must refuse (it would resolve against the working directory)")
	}
}

// TestCatalog_List_DecodesFleetEnvelope pins the exact fleet list body
// (service/handlers_catalog.go:110-133): an {"items":[...]} envelope keyed
// "id". A bare array (the old fake's shape) is a decode error.
func TestCatalog_List_DecodesFleetEnvelope(t *testing.T) {
	body := `{"items":[{"id":"7e3d2c1b-0f9e-4a6c-9b8d-12a3b4c5d6e7","owner_user_id":"u","kind":"pack","slug":"p","version":"1.0.0","visibility":"team","description":"d","signature":"","mandated":false,"published_at":"2026-10-06T00:00:00Z","mandated_reviewed":false}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("kind") == "agent_pack" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, srv.URL)
	items, err := c.List(context.Background(), CatalogFilter{Kind: CatalogKindForCapability("agent_pack")})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != "7e3d2c1b-0f9e-4a6c-9b8d-12a3b4c5d6e7" || items[0].Kind != CatalogKindPack {
		t.Fatalf("items = %+v", items)
	}
	if CapabilityKindForCatalog(items[0].Kind) != "agent_pack" || CatalogKindForCapability("agent_pack") != "pack" {
		t.Error("pack <-> agent_pack translation wrong")
	}
	var bare []CatalogItem
	if err := json.Unmarshal([]byte(body), &bare); err == nil {
		t.Error("fixture sanity: the envelope must not decode as a bare array")
	}
}
