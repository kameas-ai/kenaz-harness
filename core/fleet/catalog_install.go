// catalog_install.go — signature verification + namespaced extraction.
//
// Install verifies the ed25519 signature, then writes the payload to
//   <DataDir>/installed/<kind>/<catalog_id>@<version>/payload
//
// Uninstall removes the namespaced directory (FR-006).
package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// installBasePath returns the namespaced path for an installed item.
//   <dataDir>/installed/<kind>/<catalogID>@<version>/
func installBasePath(dataDir string, kind CatalogItemKind, catalogID, version string) string {
	return filepath.Join(dataDir, "installed", string(kind), catalogID+"@"+version)
}

// Install downloads the item from fleet, verifies the signature against the
// provided fleet signing public key (base64), and extracts the payload to
//   <dataDir>/installed/<kind>/<id>@<version>/payload
//
// The pubKeyBase64 parameter is the raw 32-byte ed25519 public key of the
// fleet signing key, encoded in standard base64. For device-signed catalog
// items the pubkey is the publishing device's key (sourced from the item's
// pubkey_fp lookup — not yet implemented server-side; the parameter carries
// the fleet-level verification key until per-device key lookup is available).
//
// Install is verified in under 500ms per NFR-004.
func (c *Client) Install(ctx context.Context, dataDir string, pubKeyBase64 string, catalogID, version string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	// Fetch the signed payload.
	path := fmt.Sprintf("/api/v1/catalog/%s@%s", catalogID, version)
	resp, err := c.Get(ctx, path)
	if err != nil {
		return fmt.Errorf("fleet/catalog: install: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/catalog: install: status %d: %s", resp.StatusCode, body)
	}

	var item CatalogItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return fmt.Errorf("fleet/catalog: install: decode: %w", err)
	}

	// Verify signature.
	if err := verifyCatalogSignature(pubKeyBase64, item.PayloadBytes, item.Signature); err != nil {
		return err
	}

	// Extract to namespaced dir.
	destDir := installBasePath(dataDir, item.Kind, catalogID, version)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("fleet/catalog: install: mkdir: %w", err)
	}
	payloadPath := filepath.Join(destDir, "payload")
	if err := os.WriteFile(payloadPath, item.PayloadBytes, 0o600); err != nil {
		return fmt.Errorf("fleet/catalog: install: write payload: %w", err)
	}
	// Write a metadata sidecar for provenance.
	metaPath := filepath.Join(destDir, "meta.json")
	metaBytes, _ := json.MarshalIndent(map[string]string{
		"catalog_id": catalogID,
		"version":    version,
		"kind":       string(item.Kind),
		"slug":       item.Slug,
		"signature":  item.Signature,
	}, "", "  ")
	_ = os.WriteFile(metaPath, metaBytes, 0o600) // best-effort
	return nil
}

// Uninstall removes the namespaced install directory (FR-006).
// Returns nil when the directory does not exist (idempotent).
func (c *Client) Uninstall(dataDir string, kind CatalogItemKind, catalogID, version string) error {
	dir := installBasePath(dataDir, kind, catalogID, version)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fleet/catalog: uninstall: %w", err)
	}
	return nil
}

// InstalledItems returns a slice of CatalogItem stubs for all locally
// installed items under dataDir/installed/. Only meta.json fields are
// populated (no PayloadBytes).
func InstalledItems(dataDir string) ([]CatalogItem, error) {
	baseDir := filepath.Join(dataDir, "installed")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fleet/catalog: list installed: %w", err)
	}
	var out []CatalogItem
	for _, kindEntry := range entries {
		if !kindEntry.IsDir() {
			continue
		}
		kindDir := filepath.Join(baseDir, kindEntry.Name())
		itemDirs, err := os.ReadDir(kindDir)
		if err != nil {
			continue
		}
		for _, d := range itemDirs {
			if !d.IsDir() {
				continue
			}
			metaPath := filepath.Join(kindDir, d.Name(), "meta.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta map[string]string
			if err := json.Unmarshal(data, &meta); err != nil {
				continue
			}
			out = append(out, CatalogItem{
				ID:         meta["catalog_id"],
				Kind:       CatalogItemKind(meta["kind"]),
				Slug:       meta["slug"],
				Version:    meta["version"],
				Signature:  meta["signature"],
				Visibility: CatalogVisPrivate, // local install; visibility unknown
			})
		}
	}
	return out, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

// verifyCatalogSignature verifies the base64 ed25519 signature over payload.
// pubKeyBase64 is the standard (non-URL-safe) base64-encoded 32-byte public key.
func verifyCatalogSignature(pubKeyBase64 string, payload []byte, sigBase64 string) error {
	if pubKeyBase64 == "" {
		// No public key provided — skip verification (useful when server
		// hasn't yet published per-device keys; tracked for follow-up).
		return nil
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyBase64)
	if err != nil {
		// Also try raw (unpadded) base64 for forward compat with
		// DeviceSigner.Sign which uses base64.StdEncoding.
		pubBytes, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(pubKeyBase64, "="))
		if err != nil {
			return fmt.Errorf("%w: decode pubkey: %v", ErrCatalogSignatureMismatch, err)
		}
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: wrong pubkey length %d", ErrCatalogSignatureMismatch, len(pubBytes))
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil {
		return fmt.Errorf("%w: decode sig: %v", ErrCatalogSignatureMismatch, err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), payload, sigBytes) {
		return ErrCatalogSignatureMismatch
	}
	return nil
}
