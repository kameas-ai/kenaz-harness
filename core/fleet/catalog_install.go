// catalog_install.go — the generic catalog install path (refusing), plus
// cleanup and listing of what earlier releases left under installed/.
//
// install-framework-01DOGF0B WP02: Install used to verify, then write
//   <DataDir>/installed/<kind>/<catalog_id>@<version>/payload (+ meta.json)
// and report success. Nothing reads installed/ — not for workflow,
// agent_pack, bundle or skill — so every such "install" was a badge with no
// capability behind it (docs/unwired-ledger.md, "badge-only catalog
// install"). Install now refuses every kind with ErrCatalogKindNotInstallable
// and writes nothing. Uninstall and InstalledItems stay: they are how
// existing installed/ residue is listed and removed.
package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrCatalogKindNotInstallable is returned by Install for every catalog
// kind: no kind has an installer that ends in a consumed capability on
// this path yet. The wrapped message names the kind and the working
// alternative. Deleted when each kind gains a provider that registers with
// its runtime consumer (install-framework-01DOGF0B WP05–WP07).
var ErrCatalogKindNotInstallable = errors.New("fleet/catalog: installing this kind from the org catalog isn't supported yet")

// CatalogInstallRefusal returns the named refusal for kind. Exported so the
// RPC view and tests share one source for the per-kind reason.
func CatalogInstallRefusal(kind CatalogItemKind) error {
	switch kind {
	case CatalogKindWorkflow:
		return fmt.Errorf("%w: workflow — nothing on this device loads a downloaded workflow; install workflows from Workflows › Catalog", ErrCatalogKindNotInstallable)
	case CatalogKindAgentPack:
		return fmt.Errorf("%w: agent_pack — agent profiles load only from the agents folder in your profile directory, which a catalog download does not reach", ErrCatalogKindNotInstallable)
	case CatalogKindBundle:
		return fmt.Errorf("%w: bundle — bundles install from a kenaz.yaml manifest directory; use Settings › Integrations › Bundles", ErrCatalogKindNotInstallable)
	case CatalogKindSkill:
		return fmt.Errorf("%w: skill — skills install through the skill path (SkillInstall), which live-registers them", ErrCatalogKindNotInstallable)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrCatalogKindNotInstallable, kind)
	}
}

// installBasePath returns the namespaced path for an installed item.
//   <dataDir>/installed/<kind>/<catalogID>@<version>/
func installBasePath(dataDir string, kind CatalogItemKind, catalogID, version string) string {
	return filepath.Join(dataDir, "installed", string(kind), catalogID+"@"+version)
}

// Install fetches the item to learn its kind, then refuses it with
// CatalogInstallRefusal — it writes nothing (install-framework-01DOGF0B
// WP02). dataDir and pubKeyBase64 are unread for now: they are kept so the
// per-kind providers that replace this method (WP05–WP07) and the C-2
// per-device key (catalog/impl.go's pubKeyBase64, WithPubKey) plug into the
// existing call chain rather than re-threading it.
func (c *Client) Install(ctx context.Context, _ string, _ string, catalogID, version string) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
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
	return CatalogInstallRefusal(item.Kind)
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
		// fleet-enforcement-truth-01PMZ505 WP10 (register C-2,
		// 2026-08-19): logged at warn, not silently, because this skip
		// means the install about to proceed is UNVERIFIED — no
		// per-device catalog signing key source exists in or out of
		// this repo (justify: blocker as above, owner alec). Behaviour
		// unchanged; the skip is now observable instead of invisible.
		logging.L().Warn("fleet.catalog_install.signature_verification_skipped",
			"reason", "no pubkey configured — no per-device catalog signing key source exists yet")
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
