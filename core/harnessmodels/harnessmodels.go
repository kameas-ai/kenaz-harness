// Package harnessmodels resolves the harness's model SLOTS — the default
// sub-agent model and the per-role models (advisor, compaction, branch
// advisor, risk rater) — from four layers, and says which layer decided.
//
// Why it exists (fleet-workbench-next-plan-2026-11-10 B11): in a served
// workbench every slot was empty. Settings are read-only there (audit
// HR-4), the only provider is the host-supplied "kenaz-host" profile, and
// every consumer that looked for "a configured profile" looked in the
// personal provider store, which a workbench never has. The first
// kenaz__subagent_dispatch therefore failed with "no default LLM profile
// configured" and left an orphan child session behind.
//
// Layers, highest precedence first:
//
//  1. org      — the signed fleet bundle's org_config.harness_models
//     section (ApplyOrg). An organisation-managed value; it beats the
//     user's own setting the same way every other org_config section does.
//  2. settings — the user's explicit Settings value for the slot (desktop;
//     a served workbench cannot write settings, but a settings.json may
//     still carry one).
//  3. host     — KENAZ_HARNESS_SUBAGENT_MODEL / KENAZ_HARNESS_ROLE_MODELS
//     from the KENAZMETA kenaz-meta.env carrier (ParseHostEnv).
//  4. derived  — computed from the session's provider and its model list
//     via the provider-owned size tiers in core/llm/capabilities: worker
//     roles take a "medium" model (falling back to "small"), compaction
//     takes the cheapest ("small", falling back to "medium"), the advisor
//     and the risk rater take the session's own model.
//
// org and host values name a MODEL ID on the session's provider; they are
// validated against that provider's model list (ValidateModels) and an
// unknown id is dropped — for org with an ack error, for host with a log
// line — so a typo can never route a request to a model the credential
// cannot use.
//
// The package is pure: no settings, fleet or rpc imports, so the
// precedence table is testable in isolation.
package harnessmodels

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

// Slot names one model slot. The string values are the wire names used by
// org_config.harness_models (roles keys), KENAZ_HARNESS_ROLE_MODELS and the
// Settings_ModelSlots RPC.
type Slot string

const (
	SlotSubagentDefault Slot = "subagent_default"
	SlotAdvisor         Slot = "advisor"
	SlotCompaction      Slot = "compaction"
	SlotBranchAdvisor   Slot = "branch_advisor"
	SlotRiskRater       Slot = "risk_rater"
)

// AllSlots is the display order.
var AllSlots = []Slot{SlotSubagentDefault, SlotAdvisor, SlotCompaction, SlotBranchAdvisor, SlotRiskRater}

// RoleSlots are the slots carried under "roles" on the wire.
var RoleSlots = []Slot{SlotAdvisor, SlotCompaction, SlotBranchAdvisor, SlotRiskRater}

func isRole(s Slot) bool {
	for _, r := range RoleSlots {
		if r == s {
			return true
		}
	}
	return false
}

// Source says which layer decided a slot.
type Source string

const (
	SourceOrg      Source = "org"
	SourceSettings Source = "settings"
	SourceHost     Source = "host"
	SourceDerived  Source = "derived"
	// SourceProfile: a sub-agent profile's own declared model, offered
	// verbatim by the provider (MapModel's exact-match case).
	SourceProfile Source = "profile"
	// SourceNone: nothing resolved (no provider to derive from).
	SourceNone Source = "none"
)

// Env vars the served harness reads from kenaz-meta.env.
const (
	// EnvSubagentModel is the default sub-agent model id.
	EnvSubagentModel = "KENAZ_HARNESS_SUBAGENT_MODEL"
	// EnvRoleModels is a JSON object of role → model id, e.g.
	// {"compaction":"claude-haiku-4-5","advisor":"claude-sonnet-4-5"}.
	EnvRoleModels = "KENAZ_HARNESS_ROLE_MODELS"
)

// Ref is a (provider profile, model) pair.
type Ref struct {
	ProviderID string `json:"providerId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
}

// IsZero reports whether neither half is set.
func (r Ref) IsZero() bool { return r.ProviderID == "" && r.ModelID == "" }

// Effective is one resolved slot.
type Effective struct {
	Slot       Slot   `json:"slot"`
	ProviderID string `json:"providerId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
	Source     Source `json:"source"`
}

// Ref returns the resolved pair.
func (e Effective) Ref() Ref { return Ref{ProviderID: e.ProviderID, ModelID: e.ModelID} }

// Roles is the wire shape of the "roles" object.
type Roles struct {
	Advisor       string `json:"advisor,omitempty"`
	Compaction    string `json:"compaction,omitempty"`
	BranchAdvisor string `json:"branch_advisor,omitempty"`
	RiskRater     string `json:"risk_rater,omitempty"`
}

// Overrides is one layer's model ids. It is also the wire shape of
// org_config.harness_models:
//
//	{"subagent_default":"claude-sonnet-4-5",
//	 "roles":{"advisor":"…","compaction":"…","branch_advisor":"…","risk_rater":"…"}}
//
// Every field is optional; an empty string means "this layer has no
// opinion" and the next layer decides.
type Overrides struct {
	SubagentDefault string `json:"subagent_default,omitempty"`
	Roles           Roles  `json:"roles,omitempty"`
}

// Get returns the layer's model id for slot ("" when unset).
func (o Overrides) Get(s Slot) string {
	switch s {
	case SlotSubagentDefault:
		return o.SubagentDefault
	case SlotAdvisor:
		return o.Roles.Advisor
	case SlotCompaction:
		return o.Roles.Compaction
	case SlotBranchAdvisor:
		return o.Roles.BranchAdvisor
	case SlotRiskRater:
		return o.Roles.RiskRater
	}
	return ""
}

func (o *Overrides) set(s Slot, v string) {
	switch s {
	case SlotSubagentDefault:
		o.SubagentDefault = v
	case SlotAdvisor:
		o.Roles.Advisor = v
	case SlotCompaction:
		o.Roles.Compaction = v
	case SlotBranchAdvisor:
		o.Roles.BranchAdvisor = v
	case SlotRiskRater:
		o.Roles.RiskRater = v
	}
}

// IsZero reports whether the layer sets nothing.
func (o Overrides) IsZero() bool {
	for _, s := range AllSlots {
		if o.Get(s) != "" {
			return false
		}
	}
	return true
}

// maxModelIDLen bounds an id accepted from env or bundle.
const maxModelIDLen = 128

func validModelIDShape(id string) bool {
	if id == "" || len(id) > maxModelIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/', r == ':', r == '~', r == '@':
		default:
			return false
		}
	}
	return true
}

// decodeRoleMap turns a role → model JSON object into Overrides, reporting
// unknown role keys and malformed ids as errors (the known, well-formed
// entries are still returned).
func decodeRoleMap(m map[string]string) (Overrides, []error) {
	var out Overrides
	var errs []error
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.TrimSpace(m[k])
		s := Slot(k)
		if !isRole(s) {
			errs = append(errs, fmt.Errorf("unknown role %q (want one of advisor, compaction, branch_advisor, risk_rater)", k))
			continue
		}
		if v == "" {
			continue
		}
		if !validModelIDShape(v) {
			errs = append(errs, fmt.Errorf("role %q: malformed model id", k))
			continue
		}
		out.set(s, v)
	}
	return out, errs
}

// ParseHostEnv reads the host layer from the process environment. A
// malformed KENAZ_HARNESS_ROLE_MODELS is reported and ignored (the
// workbench still boots); the well-formed parts apply.
func ParseHostEnv(getenv func(string) string) (Overrides, []error) {
	var out Overrides
	var errs []error
	if getenv == nil {
		return out, nil
	}
	if v := strings.TrimSpace(getenv(EnvSubagentModel)); v != "" {
		if validModelIDShape(v) {
			out.SubagentDefault = v
		} else {
			errs = append(errs, fmt.Errorf("%s: malformed model id", EnvSubagentModel))
		}
	}
	if raw := strings.TrimSpace(getenv(EnvRoleModels)); raw != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			errs = append(errs, fmt.Errorf("%s: not a JSON object of role → model id", EnvRoleModels))
		} else {
			roles, rerrs := decodeRoleMap(m)
			out.Roles = roles.Roles
			for _, e := range rerrs {
				errs = append(errs, fmt.Errorf("%s: %w", EnvRoleModels, e))
			}
		}
	}
	return out, errs
}

// ParseOrgPayload decodes an org_config.harness_models payload. Unknown
// top-level keys and unknown role keys are errors (an org admin's typo
// must reach the ack, not silently do nothing); the recognised entries are
// still returned so one bad key does not discard the rest.
func ParseOrgPayload(raw json.RawMessage) (Overrides, []error) {
	var out Overrides
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return out, []error{fmt.Errorf("harness_models: payload is not a JSON object")}
	}
	var errs []error
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := top[k]
		switch k {
		case "subagent_default":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				errs = append(errs, fmt.Errorf("harness_models: subagent_default must be a string"))
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if !validModelIDShape(s) {
				errs = append(errs, fmt.Errorf("harness_models: subagent_default: malformed model id"))
				continue
			}
			out.SubagentDefault = s
		case "roles":
			var m map[string]string
			if err := json.Unmarshal(v, &m); err != nil {
				errs = append(errs, fmt.Errorf("harness_models: roles must be an object of role → model id"))
				continue
			}
			roles, rerrs := decodeRoleMap(m)
			out.Roles = roles.Roles
			for _, e := range rerrs {
				errs = append(errs, fmt.Errorf("harness_models: %w", e))
			}
		default:
			errs = append(errs, fmt.Errorf("harness_models: unknown key %q", k))
		}
	}
	return out, errs
}

// ErrUnknownModel marks an override naming a model the provider does not
// offer.
var ErrUnknownModel = errors.New("model not offered by the provider")

// ValidateModels drops every entry of o whose model id is not in
// available, returning the kept entries and one error per dropped entry.
// An empty available list keeps nothing — there is no provider to honour
// the value against.
func ValidateModels(o Overrides, available []string) (Overrides, []error) {
	set := make(map[string]bool, len(available))
	for _, m := range available {
		set[m] = true
	}
	var kept Overrides
	var errs []error
	for _, s := range AllSlots {
		v := o.Get(s)
		if v == "" {
			continue
		}
		if !set[v] {
			errs = append(errs, fmt.Errorf("%s: %q: %w (offered: %s)", s, v, ErrUnknownModel, strings.Join(available, ", ")))
			continue
		}
		kept.set(s, v)
	}
	return kept, errs
}

// Inputs is what one resolution looks at besides the org/host layers the
// Resolver holds.
type Inputs struct {
	// Provider is the session's provider profile. nil means "nothing to
	// derive from and nothing to validate against" — every slot below the
	// settings layer resolves to SourceNone.
	Provider *corellm.ProviderProfile
	// Settings carries the user's explicit per-slot choices. A zero Ref is
	// "no choice".
	Settings map[Slot]Ref
}

// Resolver holds the org and host layers and resolves slots on demand.
// Safe for concurrent use.
type Resolver struct {
	mu   sync.Mutex
	host Overrides
	org  Overrides

	catalog *capabilities.Catalog
	log     *slog.Logger
	logged  map[string]bool
}

// NewResolver builds a Resolver with the host layer already parsed.
// log may be nil (slog.Default()).
func NewResolver(host Overrides, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.Default()
	}
	cat, err := capabilities.LoadDefault()
	if err != nil {
		log.Warn("harness.models.catalog_unavailable", "err", err.Error())
	}
	return &Resolver{host: host, catalog: cat, log: log, logged: map[string]bool{}}
}

// SetOrg replaces the org layer (already validated by the caller).
func (r *Resolver) SetOrg(o Overrides) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.org = o
	r.mu.Unlock()
}

// Org returns the current org layer.
func (r *Resolver) Org() Overrides {
	if r == nil {
		return Overrides{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.org
}

// Host returns the host layer.
func (r *Resolver) Host() Overrides {
	if r == nil {
		return Overrides{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.host
}

func contains(list []string, v string) bool {
	for _, m := range list {
		if m == v {
			return true
		}
	}
	return false
}

// sessionModel is the model a request on prof is sent with by default.
func sessionModel(prof corellm.ProviderProfile) string {
	if m := prof.DispatchModel(""); m != "" {
		return m
	}
	if ms := prof.AvailableModels(); len(ms) > 0 {
		return ms[0]
	}
	return ""
}

// tierOf asks the provider-owned capability data for a model's size tier.
func (r *Resolver) tierOf(kind, model string) string {
	if r.catalog == nil || model == "" {
		return ""
	}
	if t, ok := r.catalog.Tier(kind, model); ok {
		return t
	}
	if t, ok := r.catalog.TierAny(model); ok {
		return t
	}
	return ""
}

// firstOfTier returns the first available model of the first tier in
// prefs that has one.
func (r *Resolver) firstOfTier(prof corellm.ProviderProfile, prefs ...string) string {
	models := prof.AvailableModels()
	for _, want := range prefs {
		for _, m := range models {
			if r.tierOf(prof.Kind, m) == want {
				return m
			}
		}
	}
	return ""
}

// derive computes the derived model for slot on prof.
func (r *Resolver) derive(slot Slot, prof corellm.ProviderProfile) string {
	var m string
	switch slot {
	case SlotSubagentDefault, SlotBranchAdvisor:
		m = r.firstOfTier(prof, "medium", "small")
	case SlotCompaction:
		m = r.firstOfTier(prof, "small", "medium")
	case SlotAdvisor, SlotRiskRater:
		m = sessionModel(prof)
	}
	if m == "" {
		m = sessionModel(prof)
	}
	return m
}

// Resolve resolves one slot.
func (r *Resolver) Resolve(slot Slot, in Inputs) Effective {
	out := Effective{Slot: slot, Source: SourceNone}
	if r == nil {
		return out
	}
	r.mu.Lock()
	org, host := r.org, r.host
	r.mu.Unlock()

	var providerID string
	var available []string
	if in.Provider != nil {
		providerID = in.Provider.ID
		available = in.Provider.AvailableModels()
	}
	if v := org.Get(slot); v != "" && providerID != "" && contains(available, v) {
		return Effective{Slot: slot, ProviderID: providerID, ModelID: v, Source: SourceOrg}
	}
	if ref, ok := in.Settings[slot]; ok && !ref.IsZero() {
		return Effective{Slot: slot, ProviderID: ref.ProviderID, ModelID: ref.ModelID, Source: SourceSettings}
	}
	if v := host.Get(slot); v != "" && providerID != "" && contains(available, v) {
		return Effective{Slot: slot, ProviderID: providerID, ModelID: v, Source: SourceHost}
	}
	if in.Provider == nil {
		return out
	}
	m := r.derive(slot, *in.Provider)
	if m == "" {
		return out
	}
	r.logDerivedOnce(slot, providerID, m)
	return Effective{Slot: slot, ProviderID: providerID, ModelID: m, Source: SourceDerived}
}

// ResolveAll resolves every slot in display order.
func (r *Resolver) ResolveAll(in Inputs) []Effective {
	out := make([]Effective, 0, len(AllSlots))
	for _, s := range AllSlots {
		out = append(out, r.Resolve(s, in))
	}
	return out
}

func (r *Resolver) logDerivedOnce(slot Slot, providerID, model string) {
	key := string(slot) + "\x00" + providerID + "\x00" + model
	r.mu.Lock()
	seen := r.logged[key]
	r.logged[key] = true
	r.mu.Unlock()
	if seen {
		return
	}
	r.log.Info("harness.models.derived", "slot", string(slot), "profile_id", providerID, "model", model)
}

// MapModel maps a sub-agent profile's declared model onto prof's model
// list: an exact match is kept; otherwise the declared model's size tier
// (from the capability data, which knows OpenRouter-shaped ids like
// "<vendor>/<model>") picks the first provider model of the same tier.
// ok is false when neither works.
func (r *Resolver) MapModel(declared string, prof corellm.ProviderProfile) (string, bool) {
	if r == nil || declared == "" {
		return "", false
	}
	models := prof.AvailableModels()
	if contains(models, declared) {
		return declared, true
	}
	tier, ok := r.tierAnyOnly(declared)
	if !ok || tier == "" {
		return "", false
	}
	if m := r.firstOfTier(prof, tier); m != "" {
		return m, true
	}
	return "", false
}

// tierAnyOnly classifies a model without a provider default rung (a
// declared OpenRouter-shaped id must not fall into some provider's
// "defaults: tier").
func (r *Resolver) tierAnyOnly(model string) (string, bool) {
	if r.catalog == nil {
		return "", false
	}
	return r.catalog.TierAny(model)
}
