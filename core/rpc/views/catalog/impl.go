package catalog

import (
	"context"
	"fmt"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// API implements CatalogAPI backed by fleet.Client.
type API struct {
	client  *corefleet.Client
	signer  *corefleet.DeviceSigner
	dataDir string
	// pubKeyBase64 is the signing public key for catalog-payload install
	// verification. Always empty today (fleet-enforcement-truth-
	// 01PMZ505 WP10, register C-2, 2026-08-19, owner alec).
	// Design truth (kenaz-fleet owner, 2026-10-05): there is no
	// per-org / per-device catalog key and never will be. Org-mandated
	// items arrive in the ed25519-signed config bundle and verify against
	// the build-time-pinned fleet key (config_pull.go VerifyWithKeySet);
	// non-mandated catalog payloads carry no fleet signature today.
	// justify(blocker: "fleet owner decision on signing catalog item
	// payloads with the bundle key", owner: alec, date: 2026-10-05).
	// WithPubKey (below) is the seam that design would use; it is
	// deliberately NOT deleted.
	pubKeyBase64 string
	// emitter is optional; nil emitter means audit events are silently dropped.
	emitter auditEmitter
	// skills is the consumer-side source of truth for kind=skill installed
	// state (install-framework-01DOGF0B WP01). Skill installs never touch
	// <dataDir>/installed/ — they go through the install framework
	// (fleet.InstallSkillPayload → slashcmd.LiveRegister → SkillStore) — so the installed/ scan alone painted every installed
	// skill as "Install". nil means skill items always report not-installed
	// (fail toward offering Install, never toward a false "Installed").
	skills skillLister
}

// skillLister is the slice of *slashcmd.SkillStore the catalog view reads.
type skillLister interface {
	List() ([]coreslashcmd.Skill, error)
}

// auditEmitter is the minimal interface the catalog RPC needs.
type auditEmitter interface {
	EmitFleetEvent(ctx context.Context, kind contextaudit.Kind, payload any) error
}

var _ CatalogAPI = (*API)(nil)

// NewAPI constructs a CatalogAPI backed by the given fleet client.
// signer and dataDir are required for publish and install operations.
func NewAPI(client *corefleet.Client, signer *corefleet.DeviceSigner, dataDir string) *API {
	return &API{client: client, signer: signer, dataDir: dataDir}
}

// WithEmitter sets the audit emitter.
func (a *API) WithEmitter(em auditEmitter) *API {
	a.emitter = em
	return a
}

// WithSkillStore sets the skill store that kind=skill installed state is
// read from (install-framework-01DOGF0B WP01).
func (a *API) WithSkillStore(s skillLister) *API {
	a.skills = s
	return a
}

// WithPubKey sets the fleet-level signing public key for install verification.
func (a *API) WithPubKey(pubKeyBase64 string) *API {
	a.pubKeyBase64 = pubKeyBase64
	return a
}

// PubKey returns the catalog signing public key — the one key source the
// install framework's single SignatureVerifier reads for every fleet-backed
// kind (install-framework-01DOGF0B WP05). Empty today: register C-2, see
// pubKeyBase64's doc above.
func (a *API) PubKey() string {
	if a == nil {
		return ""
	}
	return a.pubKeyBase64
}

// Catalog_Publish implements CatalogAPI.
func (a *API) Catalog_Publish(ctx context.Context, input PublishInput) (CatalogItemView, error) {
	if a.client == nil {
		return CatalogItemView{}, corefleet.ErrFleetDisabled
	}
	if a.signer == nil {
		return CatalogItemView{}, fmt.Errorf("catalog: signer not configured")
	}
	kind := corefleet.CatalogKindForCapability(input.Kind) // "agent_pack" → fleet "pack"
	vis := corefleet.CatalogVisibility(input.Visibility)
	payload := []byte(input.PayloadJSON)

	item, err := a.client.Publish(ctx, a.signer, kind, input.Slug, input.Version,
		input.Description, vis, payload)
	if err != nil {
		return CatalogItemView{}, err
	}

	// Emit audit event.
	if a.emitter != nil {
		_ = a.emitter.EmitFleetEvent(ctx, contextaudit.KindFleetCatalogPublished,
			contextaudit.FleetCatalogPublishedPayload{
				CatalogID: item.ID,
				Kind:      string(item.Kind),
				Slug:      item.Slug,
				Version:   item.Version,
			})
	}

	return catalogItemToView(item, false), nil
}

// Catalog_List implements CatalogAPI.
func (a *API) Catalog_List(ctx context.Context, filter CatalogFilter) ([]CatalogItemView, error) {
	if a.client == nil {
		return nil, corefleet.ErrFleetDisabled
	}
	items, err := a.client.List(ctx, corefleet.CatalogFilter{
		Kind:       corefleet.CatalogKindForCapability(filter.Kind),
		Visibility: corefleet.CatalogVisibility(filter.Visibility),
	})
	if err != nil {
		return nil, err
	}

	// Installed state per kind (install-framework-01DOGF0B WP01):
	//   - skill: read from the consumer (the SkillStore LiveRegister writes),
	//     never from installed/ — skill installs do not write there.
	//   - every other kind: a payload under installed/ (download residue;
	//     nothing consumes it — see docs/unwired-ledger.md, the
	//     badge-only catalog install entry). The frontend labels it as a
	//     download, not an install, and offers only removal.
	residueSet := map[string]bool{}
	if a.dataDir != "" {
		if installed, err := corefleet.InstalledItems(a.dataDir); err == nil {
			for _, it := range installed {
				if it.Kind == corefleet.CatalogKindSkill {
					continue
				}
				residueSet[it.ID+"@"+it.Version] = true
			}
		}
	}
	skillSet := a.installedSkillSet()

	out := make([]CatalogItemView, len(items))
	for i, it := range items {
		key := it.ID + "@" + it.Version
		installed := residueSet[key]
		if it.Kind == corefleet.CatalogKindSkill {
			installed = skillSet[key]
		}
		out[i] = catalogItemToView(it, installed)
	}
	return out, nil
}

// installedSkillSet returns catalogID@version for every skill in the skill
// store that came from the catalog. A store read error yields an empty set:
// the badge then offers Install, and a re-install is idempotent (Save
// overwrites by skill ID), whereas a false "Installed" would hide the action.
func (a *API) installedSkillSet() map[string]bool {
	out := map[string]bool{}
	if a.skills == nil {
		return out
	}
	skills, err := a.skills.List()
	if err != nil {
		return out
	}
	for _, sk := range skills {
		if sk.CatalogID == "" {
			continue
		}
		out[sk.CatalogID+"@"+sk.Version] = true
	}
	return out
}

// Catalog_Install implements CatalogAPI.
func (a *API) Catalog_Install(ctx context.Context, catalogID, version string) error {
	if a.client == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.client.Install(ctx, a.dataDir, a.pubKeyBase64, catalogID, version)
}

// Catalog_Uninstall implements CatalogAPI.
func (a *API) Catalog_Uninstall(_ context.Context, kind, catalogID, version string) error {
	// The frontend speaks capability kinds ("agent_pack"); residue lives
	// under the catalog kind it was downloaded as. Remove both spellings
	// (Uninstall is idempotent on a missing directory).
	if err := a.client.Uninstall(a.dataDir, corefleet.CatalogKindForCapability(kind), catalogID, version); err != nil {
		return err
	}
	if wire := corefleet.CatalogKindForCapability(kind); string(wire) != kind {
		return a.client.Uninstall(a.dataDir, corefleet.CatalogItemKind(kind), catalogID, version)
	}
	return nil
}

// Catalog_Installed implements CatalogAPI.
func (a *API) Catalog_Installed(_ context.Context) ([]CatalogItemView, error) {
	if a.dataDir == "" {
		return nil, nil
	}
	items, err := corefleet.InstalledItems(a.dataDir)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogItemView, len(items))
	for i, it := range items {
		out[i] = catalogItemToView(it, true)
	}
	return out, nil
}

// Catalog_Unpublish implements CatalogAPI.
func (a *API) Catalog_Unpublish(ctx context.Context, catalogID string) error {
	if a.client == nil {
		return corefleet.ErrFleetDisabled
	}
	if err := a.client.Unpublish(ctx, catalogID); err != nil {
		return err
	}
	if a.emitter != nil {
		_ = a.emitter.EmitFleetEvent(ctx, contextaudit.KindFleetCatalogUnpublished,
			contextaudit.FleetCatalogUnpublishedPayload{CatalogID: catalogID})
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func catalogItemToView(it corefleet.CatalogItem, installed bool) CatalogItemView {
	v := CatalogItemView{
		ID:          it.ID,
		Kind:        corefleet.CapabilityKindForCatalog(it.Kind), // fleet "pack" → "agent_pack"
		Slug:        it.Slug,
		Version:     it.Version,
		Description: it.Description,
		Visibility:  string(it.Visibility),
		Installed:   installed,
	}
	if !it.PublishedAt.IsZero() {
		v.PublishedAt = it.PublishedAt.UTC().Format(time.RFC3339)
	}
	return v
}
