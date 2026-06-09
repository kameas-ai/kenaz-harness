package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ── fake fleet catalog server ─────────────────────────────────────────────────

type fakeCatalogServer struct {
	published []publishRequest
	items     map[string]CatalogItem // keyed by catalogID@version
	deleted   []string
}

func (f *fakeCatalogServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/catalog/publish":
		var req publishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
		var out []CatalogItem
		for _, it := range f.items {
			cp := it
			cp.PayloadBytes = nil // metadata only on list
			out = append(out, cp)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/v1/catalog/"):
		key := r.URL.Path[len("/api/v1/catalog/"):]
		if it, ok := f.items[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(it)
		} else {
			http.NotFound(w, r)
		}

	case r.Method == http.MethodDelete && len(r.URL.Path) > len("/api/v1/catalog/"):
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
		CatalogKindAgentPack, "test-pack", "0.1.0", "desc", CatalogVisTeam, payload)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	items, err := c.List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) == 0 {
		t.Error("expected at least one item in list")
	}
	// Verify metadata-only: list items must not carry payload.
	for _, it := range items {
		if len(it.PayloadBytes) > 0 {
			t.Errorf("list item %q should not carry PayloadBytes", it.ID)
		}
	}
}

func TestCatalog_InstallAndInstalledItems(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-cat",
		RefreshToken: "rt-cat",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	signerDataDir := t.TempDir()
	signer, _ := NewDeviceSigner(signerDataDir)

	payload := []byte(`workflow-content`)
	item, err := c.Publish(context.Background(), signer,
		CatalogKindWorkflow, "wf-install", "1.2.3", "desc", CatalogVisTeam, payload)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Install with no pubkey (skip signature verification — per-device key
	// lookup is not yet implemented server-side; tracked for follow-up).
	installDir := t.TempDir()
	if err := c.Install(context.Background(), installDir, "", item.ID, "1.2.3"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Verify InstalledItems reports the installed item.
	installed, err := InstalledItems(installDir)
	if err != nil {
		t.Fatalf("InstalledItems: %v", err)
	}
	if len(installed) == 0 {
		t.Error("expected installed item; got none")
	}
	if installed[0].ID != item.ID {
		t.Errorf("installed item ID = %q, want %q", installed[0].ID, item.ID)
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

	signer, _ := NewDeviceSigner(t.TempDir())
	item, _ := c.Publish(context.Background(), signer,
		CatalogKindWorkflow, "to-remove", "1.0.0", "desc", CatalogVisTeam, []byte("data"))

	dataDir := t.TempDir()
	_ = c.Install(context.Background(), dataDir, "", item.ID, "1.0.0")

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
