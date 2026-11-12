package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/harnessmodels"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// OrgConfigHarnessModels is the org_config key of the harness model-slot
// section (fleet-workbench-next-plan-2026-11-10 B11). Wire shape:
// harnessmodels.Overrides; see docs/served-mode-boundary.md.
const OrgConfigHarnessModels = "harness_models"

// ModelSlots is the per-process model-slot state: the resolver (host + org
// layers) and the session provider a served workbench derives from.
type ModelSlots struct {
	resolver *harnessmodels.Resolver
	provider func() *corellm.ProviderProfile

	mu      sync.Mutex
	orgErrs []string
	orgSet  bool
}

// ModelSlotsView is the Settings_ModelSlots result.
type ModelSlotsView struct {
	// ProviderID is the session provider the derived values come from
	// ("" when there is none — the desktop app without org config).
	ProviderID string `json:"providerId,omitempty"`
	// Models is that provider's model list.
	Models []string `json:"models,omitempty"`
	// Slots is every slot in display order with its source.
	Slots []harnessmodels.Effective `json:"slots"`
	// OrgErrors are the reasons entries of the last org_config.harness_models
	// section were refused (also returned in the bundle ack).
	OrgErrors []string `json:"orgErrors,omitempty"`
}

// SetModelSlots installs the model-slot resolver. provider returns the
// session provider to derive from and validate against (the served
// harness's host profile); nil — the desktop path — derives nothing.
func (a *API) SetModelSlots(r *harnessmodels.Resolver, provider func() *corellm.ProviderProfile) *ModelSlots {
	if a.fleet == nil {
		a.fleet = newFleetState()
	}
	ms := &ModelSlots{resolver: r, provider: provider}
	a.fleet.mu.Lock()
	a.fleet.modelSlots = ms
	a.fleet.mu.Unlock()
	return ms
}

func (a *API) modelSlots() *ModelSlots {
	if a == nil || a.fleet == nil {
		return nil
	}
	a.fleet.mu.RLock()
	defer a.fleet.mu.RUnlock()
	return a.fleet.modelSlots
}

func (m *ModelSlots) sessionProvider() *corellm.ProviderProfile {
	if m == nil || m.provider == nil {
		return nil
	}
	return m.provider()
}

// settingsRefs maps the user's explicit Settings fields onto slots. The
// default sub-agent slot has no Settings field.
func settingsRefs(s Settings) map[harnessmodels.Slot]harnessmodels.Ref {
	conv := func(r ProviderProfileRef) harnessmodels.Ref {
		return harnessmodels.Ref{ProviderID: r.ProviderID, ModelID: r.ModelID}
	}
	return map[harnessmodels.Slot]harnessmodels.Ref{
		harnessmodels.SlotAdvisor:       conv(s.AdvisorModel),
		harnessmodels.SlotCompaction:    conv(s.CompactionModel),
		harnessmodels.SlotBranchAdvisor: conv(s.BranchAdvisorDefaultModel),
		harnessmodels.SlotRiskRater:     conv(s.RiskRaterModel),
	}
}

func (a *API) modelSlotInputs(m *ModelSlots) harnessmodels.Inputs {
	in := harnessmodels.Inputs{Provider: m.sessionProvider()}
	if a.store != nil {
		if s, err := a.store.LoadAll(); err == nil {
			in.Settings = settingsRefs(s)
		}
	}
	return in
}

// ResolveModelSlot resolves one slot. ok is false when nothing resolved
// (no resolver installed, or no layer has a value).
func (a *API) ResolveModelSlot(slot harnessmodels.Slot) (harnessmodels.Effective, bool) {
	m := a.modelSlots()
	if m == nil || m.resolver == nil {
		return harnessmodels.Effective{Slot: slot, Source: harnessmodels.SourceNone}, false
	}
	e := m.resolver.Resolve(slot, a.modelSlotInputs(m))
	return e, e.Source != harnessmodels.SourceNone && e.ModelID != ""
}

// SessionProvider returns the served session provider, or nil.
func (a *API) SessionProvider() *corellm.ProviderProfile {
	return a.modelSlots().sessionProvider()
}

// MapSubagentModel maps a sub-agent profile's declared model onto the
// session provider (see harnessmodels.Resolver.MapModel). An org or host
// subagent_default beats a tier-mapped declared model, but never an exact
// match: a profile naming a model the provider offers gets that model.
func (a *API) MapSubagentModel(declared string) (providerID, modelID string, source harnessmodels.Source, ok bool) {
	m := a.modelSlots()
	prof := m.sessionProvider()
	if m == nil || m.resolver == nil || prof == nil {
		return "", "", harnessmodels.SourceNone, false
	}
	for _, have := range prof.AvailableModels() {
		if declared != "" && have == declared {
			return prof.ID, declared, harnessmodels.SourceProfile, true
		}
	}
	def := m.resolver.Resolve(harnessmodels.SlotSubagentDefault, a.modelSlotInputs(m))
	if def.Source == harnessmodels.SourceOrg || def.Source == harnessmodels.SourceHost {
		return def.ProviderID, def.ModelID, def.Source, true
	}
	if mapped, mok := m.resolver.MapModel(declared, *prof); mok {
		return prof.ID, mapped, harnessmodels.SourceDerived, true
	}
	if def.ModelID != "" {
		return def.ProviderID, def.ModelID, def.Source, true
	}
	return "", "", harnessmodels.SourceNone, false
}

// EffectiveModelSlots is Settings_ModelSlots: every slot's effective value
// and where it came from. Read-only by design — served settings cannot be
// written (HR-4); the layers are set by Kenaz (host) and Fleet (org).
func (a *API) EffectiveModelSlots(_ context.Context) (ModelSlotsView, error) {
	m := a.modelSlots()
	if m == nil || m.resolver == nil {
		return ModelSlotsView{Slots: []harnessmodels.Effective{}}, nil
	}
	in := a.modelSlotInputs(m)
	out := ModelSlotsView{Slots: m.resolver.ResolveAll(in)}
	if in.Provider != nil {
		out.ProviderID = in.Provider.ID
		out.Models = append([]string(nil), in.Provider.AvailableModels()...)
	}
	m.mu.Lock()
	out.OrgErrors = append([]string(nil), m.orgErrs...)
	m.mu.Unlock()
	return out, nil
}

// applyOrgHarnessModels applies an org_config.harness_models payload:
// parse, validate against the session provider's models, install the
// valid entries. Every refused entry is returned (it reaches the ack).
// Without a session provider (desktop) the section is parsed for shape
// errors only and not applied — there is no provider to validate against,
// and the desktop user configures models in Settings.
func (m *ModelSlots) applyOrgHarnessModels(raw json.RawMessage) []error {
	if m == nil || m.resolver == nil {
		return nil
	}
	parsed, errs := harnessmodels.ParseOrgPayload(raw)
	prof := m.sessionProvider()
	var kept harnessmodels.Overrides
	if prof == nil {
		if !parsed.IsZero() {
			logging.L().Info("fleet.config.harness_models.no_session_provider")
		}
	} else {
		var verrs []error
		kept, verrs = harnessmodels.ValidateModels(parsed, prof.AvailableModels())
		errs = append(errs, verrs...)
	}
	m.resolver.SetOrg(kept)
	strs := make([]string, 0, len(errs))
	out := make([]error, 0, len(errs))
	for _, e := range errs {
		strs = append(strs, e.Error())
		out = append(out, fmt.Errorf("fleet/config: org_config kind %q: %w", OrgConfigHarnessModels, e))
	}
	m.mu.Lock()
	m.orgErrs, m.orgSet = strs, true
	m.mu.Unlock()
	if len(out) > 0 {
		logging.L().Warn("fleet.config.harness_models.refused", "count", len(out))
	} else {
		logging.L().Info("fleet.config.harness_models.applied")
	}
	return out
}

// clearOrgHarnessModels drops the org layer (the bundle carries no
// harness_models section).
func (m *ModelSlots) clearOrgHarnessModels() {
	if m == nil || m.resolver == nil {
		return
	}
	m.mu.Lock()
	wasSet := m.orgSet
	m.orgErrs, m.orgSet = nil, false
	m.mu.Unlock()
	m.resolver.SetOrg(harnessmodels.Overrides{})
	if wasSet {
		logging.L().Info("fleet.config.harness_models.cleared")
	}
}
