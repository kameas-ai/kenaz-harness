package capabilities

import (
	"context"
	"fmt"
	"sort"

	"github.com/kameas-ai/kenaz-harness/core/install"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// SkillStore is the consumer-side read of installed skills (the
// *slashcmd.SkillStore slashcmd.LiveRegister writes).
type SkillStore interface {
	List() ([]coreslashcmd.Skill, error)
}

// SkillRegistry is the slash registry — the runtime consumer that
// dispatches an installed skill's trigger.
type SkillRegistry interface {
	Lookup(name string) (coreslashcmd.Command, bool)
}

// SkillInstaller is the slashcmd view's skill surface the provider drives.
type SkillInstaller interface {
	SkillInstallPayload(ctx context.Context, catalogID, version string, payload []byte) error
	SkillUninstall(ctx context.Context, skillID string) error
}

// SkillProvider is install.Provider for fleet catalog skills (WP05):
// Verify fetches the signed payload, the framework verifies it once, and
// Install hands the same bytes to slashcmd.LiveRegister through the slashcmd
// view (which also records fleet.skill_installed). Installed state is the
// skill store + slash registry — never installed/ (WP01).
type SkillProvider struct {
	catalog   FleetCatalog
	store     SkillStore
	registry  SkillRegistry
	installer SkillInstaller
}

// NewSkillProvider wires the skill provider. registry may be nil (state then
// reads the store alone).
func NewSkillProvider(cat FleetCatalog, store SkillStore, registry SkillRegistry, installer SkillInstaller) *SkillProvider {
	return &SkillProvider{catalog: cat, store: store, registry: registry, installer: installer}
}

var _ install.Provider = (*SkillProvider)(nil)

const skillConsumer = "slash registry"

// Kind implements install.Provider.
func (p *SkillProvider) Kind() install.Kind { return install.KindSkill }

// List implements install.Provider: the fleet catalog's skills with
// consumer-derived state, plus every installed skill the catalog did not
// list (offline, withdrawn, org-mandated) so an installed skill never
// disappears from the surface. A catalog that cannot be listed is a reason
// row; local state still lists (P-5).
func (p *SkillProvider) List(ctx context.Context, _ install.Filter) (install.Listing, error) {
	out := install.Listing{Items: []install.Item{}}
	installed := p.installedByKey()

	entries, err := p.catalog.List(ctx, string(install.KindSkill))
	if err != nil {
		out.Unavailable = fleetUnavailable(p.catalog, err)
		entries = nil
	}
	newest := newestByID(entries)
	seen := map[string]bool{}
	ids := make([]string, 0, len(newest))
	for id := range newest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := newest[id]
		seen[id] = true
		it := install.Item{
			Kind: install.KindSkill, ID: e.ID, Version: e.Version,
			Name: firstNonEmpty(e.Slug, e.ID), Description: e.Description,
			Source: catalogSource(e.Visibility),
		}
		applyLifecycle(&it, e)
		if sk, ok := installed[e.ID]; ok {
			it.State = p.stateOf(sk)
			it.State.UpdateAvailable = it.State.Installed && versionLess(sk.Version, e.Version)
			applyMandated(&it, sk)
		} else {
			it.State = install.State{Consumer: skillConsumer}
		}
		out.Items = append(out.Items, it)
	}
	for key, sk := range installed {
		if seen[key] {
			continue
		}
		seen[key] = true
		it := install.Item{
			Kind: install.KindSkill, ID: key, Version: sk.Version,
			Name: sk.EffectiveTrigger(), Description: sk.Description,
			Source: install.SourceTeamCatalog, State: p.stateOf(sk),
		}
		applyMandated(&it, sk)
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// Detail implements install.Provider.
func (p *SkillProvider) Detail(ctx context.Context, id string) (install.Item, error) {
	l, err := p.List(ctx, install.Filter{})
	if err != nil {
		return install.Item{}, err
	}
	for _, it := range l.Items {
		if it.ID == id {
			return it, nil
		}
	}
	// A store ID (SkillsPanel addresses skills by it) resolves to its row.
	if sk, ok := p.find(id); ok {
		key := skillKey(sk)
		for _, it := range l.Items {
			if it.ID == key {
				return it, nil
			}
		}
	}
	return install.Item{}, fmt.Errorf("%w: skill %q", install.ErrNotFound, id)
}

// Requirements implements install.Provider: a skill needs nothing but its
// payload.
func (p *SkillProvider) Requirements(context.Context, string) ([]install.Requirement, error) {
	return nil, nil
}

// Verify implements install.Provider: fetch the signed payload once.
func (p *SkillProvider) Verify(ctx context.Context, ref install.Ref) (install.Verification, error) {
	if ref.Version == "" {
		return install.Verification{}, fmt.Errorf("install skill %q: a catalog version is required", ref.ID)
	}
	payload, sig, err := p.catalog.Fetch(ctx, ref.ID, ref.Version)
	if err != nil {
		return install.Verification{}, err
	}
	return install.Verification{Method: install.VerifySignature, Payload: payload, Signature: sig}, nil
}

// Install implements install.Provider: LiveRegister the verified bytes.
func (p *SkillProvider) Install(ctx context.Context, req install.InstallRequest) (any, error) {
	return nil, p.installer.SkillInstallPayload(ctx, req.Ref.ID, req.Ref.Version, req.Verification.Payload)
}

// Uninstall implements install.Provider. id may be the catalog id (this
// surface's catalog rows) or the store id (Settings › Skills); the
// slashcmd view resolves either.
func (p *SkillProvider) Uninstall(ctx context.Context, id string) error {
	return p.installer.SkillUninstall(ctx, id)
}

// InstalledState implements install.Provider: the skill is in the store and
// its trigger is registered with the slash registry.
func (p *SkillProvider) InstalledState(_ context.Context, id string) (install.State, error) {
	sk, ok := p.find(id)
	if !ok {
		return install.State{Consumer: skillConsumer}, nil
	}
	return p.stateOf(sk), nil
}

// Update implements install.Provider: the newest catalog version, when it is
// newer than the installed one.
func (p *SkillProvider) Update(ctx context.Context, id string) (install.Ref, error) {
	sk, ok := p.find(id)
	if !ok {
		return install.Ref{}, fmt.Errorf("%w: skill %q is not installed", install.ErrNoUpdate, id)
	}
	entries, err := p.catalog.List(ctx, string(install.KindSkill))
	if err != nil {
		return install.Ref{}, err
	}
	e, ok := newestByID(entries)[skillKey(sk)]
	if !ok || !versionLess(sk.Version, e.Version) {
		return install.Ref{}, fmt.Errorf("%w: skill %q is at its newest version", install.ErrNoUpdate, id)
	}
	return install.Ref{Kind: install.KindSkill, ID: e.ID, Version: e.Version}, nil
}

func (p *SkillProvider) stateOf(sk coreslashcmd.Skill) install.State {
	st := install.State{Installed: true, Version: sk.Version, Consumer: skillConsumer}
	if sk.Disabled {
		// Installed and deliberately switched off: the registry does not
		// dispatch it, and that is the user's choice, not a failed install.
		st.Detail = "disabled"
		return st
	}
	if p.registry != nil {
		if _, ok := p.registry.Lookup(sk.EffectiveTrigger()); !ok {
			// Persisted but not dispatchable — the consumer does not have it.
			st.Installed = false
			st.Detail = "stored but not registered"
		}
	}
	return st
}

// installedByKey maps each stored skill under its framework id: the catalog
// id when it came from the catalog, else its store id (mandated skills).
func (p *SkillProvider) installedByKey() map[string]coreslashcmd.Skill {
	out := map[string]coreslashcmd.Skill{}
	if p.store == nil {
		return out
	}
	skills, err := p.store.List()
	if err != nil {
		return out
	}
	for _, sk := range skills {
		out[skillKey(sk)] = sk
	}
	return out
}

// find resolves id as a catalog id first (review F3's order, see
// fleet.ResolveSkillStoreID), then as a store id.
func (p *SkillProvider) find(id string) (coreslashcmd.Skill, bool) {
	if p.store == nil {
		return coreslashcmd.Skill{}, false
	}
	skills, err := p.store.List()
	if err != nil {
		return coreslashcmd.Skill{}, false
	}
	for _, sk := range skills {
		if sk.CatalogID != "" && sk.CatalogID == id {
			return sk, true
		}
	}
	for _, sk := range skills {
		if sk.ID == id {
			return sk, true
		}
	}
	return coreslashcmd.Skill{}, false
}

func skillKey(sk coreslashcmd.Skill) string {
	if sk.CatalogID != "" {
		return sk.CatalogID
	}
	return sk.ID
}

func applyMandated(it *install.Item, sk coreslashcmd.Skill) {
	if sk.Source == coreslashcmd.SkillSourceMandated || sk.OrgManaged {
		it.Source = install.SourceOrgCatalog
		it.ReadOnly = true
		it.ReadOnlyReason = "Required by your org"
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
