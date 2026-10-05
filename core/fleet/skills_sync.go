// Package fleet — skills_sync.go
//
// Skill publish (push-up), install/uninstall (pull-down), and mandated
// skill apply (push-down) via the existing catalog seam.
//
// Fork-removal recipe: delete this file + the Publish button in
// SlashCommandsView + the skill provider of the Capabilities surface
// (core/rpc/views/capabilities/skill_provider.go; MarketplaceView, the
// skill kind's old browse, was deleted in install-framework Phase 4). The
// core/slashcmd/store.go store stays (useful standalone).
//
// (fleet-skills-sync-01NDFSEX18 WP03 / WP04 / WP05)
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// SkillSyncMaxPayloadBytes is the enforced cap for a single skill payload
// (NFR-003: ≤ 256KB).
const SkillSyncMaxPayloadBytes = 256 * 1024

// ── WP03: Publish (push-up) ──────────────────────────────────────────────────

// PublishSkill serialises skill as an opaque JSON payload and POSTs it to the
// fleet catalog with kind="skill". The payload is signed by signer.
//
// Visibility must be one of CatalogVisPrivate, CatalogVisTeam, or
// CatalogVisOrgPublic. Capability gate:
//   - CatalogVisTeam or CatalogVisOrgPublic → requires CapSharedTeamGraph
//   - CatalogVisPrivate                     → requires CapPersonalFleetDashboard
//
// Returns ErrCapabilityNotInTier when the current capabilities don't permit
// the requested visibility scope (FR-102).
func PublishSkill(
	ctx context.Context,
	client *Client,
	caps *Capabilities,
	signer *DeviceSigner,
	skill slashcmd.Skill,
	visibility CatalogVisibility,
) (CatalogItem, error) {
	if client == nil || client.isNop {
		return CatalogItem{}, ErrFleetDisabled
	}
	// Capability gate (FR-102).
	switch visibility {
	case CatalogVisTeam, CatalogVisOrgPublic:
		if err := caps.Require(CapSharedTeamGraph); err != nil {
			return CatalogItem{}, fmt.Errorf("%w (need Team+ for team/org-public skill publish)", ErrCatalogNotInTier)
		}
	case CatalogVisPrivate:
		if err := caps.Require(CapPersonalFleetDashboard); err != nil {
			return CatalogItem{}, fmt.Errorf("%w (need Pro for private skill sync)", ErrCatalogNotInTier)
		}
	default:
		return CatalogItem{}, fmt.Errorf("fleet/skills: unknown visibility %q", visibility)
	}

	payload, err := json.Marshal(skill)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/skills: marshal skill: %w", err)
	}
	if len(payload) > SkillSyncMaxPayloadBytes {
		return CatalogItem{}, fmt.Errorf("%w: skill %q is %d bytes (max %d)",
			ErrCatalogPayloadTooLarge, skill.ID, len(payload), SkillSyncMaxPayloadBytes)
	}

	slug := skill.Trigger
	if slug == "" {
		slug = skill.ID
	}
	version := skill.Version
	if version == "" {
		version = "1.0.0"
	}

	return client.Publish(ctx, signer, CatalogKindSkill, slug, version, skill.Description, visibility, payload)
}

// ── WP04: Install / Uninstall (pull-down) ───────────────────────────────────

// ErrCatalogPayloadMalformed is returned when a fetched catalog payload is
// not the format its kind requires (install-framework-01DOGF0B FR-2:
// "Unknown/opaque payload → install fails with a named error, never a
// silent success").
var ErrCatalogPayloadMalformed = errors.New("fleet/catalog: payload is not in the format its kind requires")

// FetchCatalogItem fetches the full signed item — payload and detached
// signature — for catalogID@version (GET /api/v1/catalog/{id}@{ver}). It
// installs nothing: install-framework-01DOGF0B's providers fetch here in
// Provider.Verify, the framework verifies the bytes once
// (CatalogSignatureVerdict), and Provider.Install consumes the same bytes.
func FetchCatalogItem(ctx context.Context, client *Client, catalogID, version string) (CatalogItem, error) {
	if client == nil || client.isNop {
		return CatalogItem{}, ErrFleetDisabled
	}
	path := fmt.Sprintf("/api/v1/catalog/%s@%s", catalogID, version)
	resp, err := client.Get(ctx, path)
	if err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/catalog: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return CatalogItem{}, fmt.Errorf("fleet/catalog: fetch %s@%s: status %d: %s", catalogID, version, resp.StatusCode, body)
	}
	var item CatalogItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return CatalogItem{}, fmt.Errorf("fleet/catalog: fetch decode: %w", err)
	}
	return item, nil
}

// CatalogSignatureVerdict is the fleet half of install-framework-01DOGF0B's
// single SignatureVerifier hook — the one place register C-2's per-device
// catalog key lands, for every install kind. With no key configured it
// reports verified=false and the C-2 reason (the install proceeds,
// recorded as unverified — unchanged behaviour, now visible on the
// capability:installed event); with a key, a mismatch is an error and the
// install is refused.
func CatalogSignatureVerdict(pubKeyBase64 string, payload []byte, sigBase64 string) (verified bool, reason string, err error) {
	if pubKeyBase64 == "" {
		return false, "not signature-verified: no per-device catalog signing key source exists yet (register C-2)", verifyCatalogSignature("", payload, sigBase64)
	}
	if err := verifyCatalogSignature(pubKeyBase64, payload, sigBase64); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// InstallSkillPayload decodes a skill catalog payload (slashcmd.Skill JSON),
// stamps its catalog provenance, persists it to the SkillStore and
// live-registers it in the Registry — the consumer. The payload must
// already be verified (the install framework's Verify step).
//
// Returns ErrCatalogPayloadMalformed when the payload is not a skill, and
// ErrTriggerShadowed (wrapped) when the skill's trigger conflicts with a
// higher-priority command — the skill is persisted but not dispatched
// until the conflict resolves.
func InstallSkillPayload(store *slashcmd.SkillStore, registry *slashcmd.Registry, catalogID, version string, payload []byte) error {
	var skill slashcmd.Skill
	if err := json.Unmarshal(payload, &skill); err != nil {
		return fmt.Errorf("%w: skill %s@%s: %v", ErrCatalogPayloadMalformed, catalogID, version, err)
	}
	if skill.ID == "" {
		return fmt.Errorf("%w: skill %s@%s has no id", ErrCatalogPayloadMalformed, catalogID, version)
	}
	// Stamp provenance.
	skill.CatalogID = catalogID
	skill.Version = version
	skill.Source = slashcmd.SkillSourceCatalog

	// Persist + live-register.
	if err := slashcmd.LiveRegister(store, registry, skill); err != nil {
		return fmt.Errorf("fleet/skills: install register: %w", err)
	}
	return nil
}

// ResolveSkillStoreID maps a caller-supplied identifier to the SkillStore ID.
//
// A catalog-installed skill is stored under the ID carried in its payload —
// for SkillPublish that is the command name, not the catalog_id — while the
// catalog browse (the Capabilities surface's catalog rows; formerly the
// Marketplace) only knows the catalog_id. Resolution order (review F3):
//  1. a stored skill whose CatalogID matches id (and whose Version matches
//     version, when version is non-empty);
//  2. any stored skill whose CatalogID matches id;
//  3. id itself, as an exact store ID;
//
// otherwise id unchanged (the caller's not-found error then names what was
// asked for). CatalogID is matched first so a catalog_id that happens to
// equal another skill's store ID resolves to the catalog skill, never
// cross-deletes the other one. install-framework-01DOGF0B WP01.
func ResolveSkillStoreID(store *slashcmd.SkillStore, id, version string) string {
	if store == nil || id == "" {
		return id
	}
	if skills, err := store.List(); err == nil {
		if version != "" {
			for _, sk := range skills {
				if sk.CatalogID == id && sk.Version == version {
					return sk.ID
				}
			}
		}
		for _, sk := range skills {
			if sk.CatalogID == id {
				return sk.ID
			}
		}
	}
	return id
}

// UninstallSkill removes the skill from the SkillStore and live-unregisters
// it from the Registry. Returns ErrSkillNotFound when the skill is not
// installed (idempotent from the caller's perspective).
func UninstallSkill(
	store *slashcmd.SkillStore,
	registry *slashcmd.Registry,
	skillID string,
) error {
	skillID = ResolveSkillStoreID(store, skillID, "")
	if err := slashcmd.LiveUnregister(store, registry, skillID); err != nil {
		return fmt.Errorf("fleet/skills: uninstall: %w", err)
	}
	return nil
}

// ── WP05: Push-down (org-mandated skills) ───────────────────────────────────

// ApplyMandatedSkills processes the MandatedSkills section of a config bundle
// and installs any new/updated skills read-only in the SkillStore.
//
// Each entry in mandatedSkills is an opaque JSON blob encoding a slashcmd.Skill
// with Source=SkillSourceMandated. Malformed entries are skipped with an error
// collected in the returned slice (partial-success pattern matching the rest of
// the bundle applier).
//
// Called by the compositeConfigApplier in settings/fleet.go.
func ApplyMandatedSkills(
	store *slashcmd.SkillStore,
	registry *slashcmd.Registry,
	mandatedSkills []json.RawMessage,
) []error {
	var errs []error
	for i, raw := range mandatedSkills {
		var skill slashcmd.Skill
		if err := json.Unmarshal(raw, &skill); err != nil {
			errs = append(errs, fmt.Errorf("fleet/skills: mandated[%d] unmarshal: %w", i, err))
			continue
		}
		// Ensure source is mandated regardless of what the server sent.
		skill.Source = slashcmd.SkillSourceMandated
		skill.OrgManaged = true

		if err := slashcmd.LiveRegister(store, registry, skill); err != nil {
			// ErrTriggerShadowed is informational — persist the skill but
			// don't treat as a hard error so the bundle ACK still advances.
			if isSkillShadowedErr(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("fleet/skills: mandated[%d] register %q: %w", i, skill.ID, err))
		}
	}
	return errs
}

// isSkillShadowedErr reports whether err wraps ErrTriggerShadowed.
// LiveRegister wraps the sentinel with %w, so errors.Is traverses the chain.
func isSkillShadowedErr(err error) bool {
	return errors.Is(err, slashcmd.ErrTriggerShadowed)
}
