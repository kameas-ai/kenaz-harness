// Package fleet — skills_sync.go
//
// Skill publish (push-up), install/uninstall (pull-down), and mandated
// skill apply (push-down) via the existing catalog seam.
//
// Fork-removal recipe: delete this file + the Publish button in
// SlashCommandsView + the "skill" kind in MarketplaceView. The
// core/slashcmd/store.go store stays (useful standalone).
//
// (fleet-skills-sync-01NDFSEX18 WP03 / WP04 / WP05)
package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sigil-tech/kaneaz-harness/core/slashcmd"
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
//   - CatalogVisPrivate                     → requires CapHostedInference
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
		if err := caps.Require(CapHostedInference); err != nil {
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

// InstallSkill downloads a skill catalog item from fleet, verifies its
// signature, persists it to the SkillStore, and live-registers it in the
// Registry.
//
// Returns ErrTriggerShadowed (wrapped) when the skill's trigger conflicts
// with a higher-priority command — the skill is persisted but not dispatched
// until the conflict resolves.
func InstallSkill(
	ctx context.Context,
	client *Client,
	store *slashcmd.SkillStore,
	registry *slashcmd.Registry,
	pubKeyBase64 string,
	catalogID, version string,
) error {
	if client == nil || client.isNop {
		return ErrFleetDisabled
	}

	// Fetch the signed payload via the existing catalog install path.
	// We use the low-level fetch so we can decode the skill JSON; the
	// Install method would just write the bytes to disk which we don't want.
	path := fmt.Sprintf("/api/v1/catalog/%s@%s", catalogID, version)
	resp, err := client.Get(ctx, path)
	if err != nil {
		return fmt.Errorf("fleet/skills: install fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var item CatalogItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return fmt.Errorf("fleet/skills: install decode: %w", err)
	}

	// Verify signature.
	if err := verifyCatalogSignature(pubKeyBase64, item.PayloadBytes, item.Signature); err != nil {
		return fmt.Errorf("fleet/skills: install verify: %w", err)
	}

	// Decode the skill from the opaque payload.
	var skill slashcmd.Skill
	if err := json.Unmarshal(item.PayloadBytes, &skill); err != nil {
		return fmt.Errorf("fleet/skills: install unmarshal skill: %w", err)
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

// UninstallSkill removes the skill from the SkillStore and live-unregisters
// it from the Registry. Returns ErrSkillNotFound when the skill is not
// installed (idempotent from the caller's perspective).
func UninstallSkill(
	store *slashcmd.SkillStore,
	registry *slashcmd.Registry,
	skillID string,
) error {
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
func isSkillShadowedErr(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), slashcmd.ErrTriggerShadowed.Error())
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})())
}
