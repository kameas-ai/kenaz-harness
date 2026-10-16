package llm

import "sort"

// Many keys per provider (provider-credentials-client-01PCCLI01 §7a H2).
//
// A user can hold several personal keys plus every org and team key they
// are entitled to, more than one per provider. Every key is listed and
// labelled by scope; the org's designated default is preselected per
// provider; the user picks a key per provider profile and per chat.
//
// Where each selection lives:
//   - per profile: ProviderProfile.Cred (Source + CredentialID for a Fleet
//     key, Locator for a local one), persisted in providers.json;
//   - per chat: the frontend's per-session stash
//     (frontend/src/lib/sessionModelStash.ts, key credentialId) next to the
//     chat's provider and model.
//
// Whether the user may pick at all is decided by userKeySelectionAllowed,
// which fails closed until an explicit policy allows it.

// CredentialKey is display metadata for one key a profile can use. It never
// carries key material or a keychain locator.
type CredentialKey struct {
	// ID is the key's stable identifier: the Fleet credential id for a
	// Fleet key, "local:<profileID>" for a key entered on this device.
	ID string `json:"id"`
	// Provider is the provider kind the key is for (ProviderProfile.Kind).
	Provider string `json:"provider"`
	// Source is local | user_sync | org | team.
	Source CredentialSource `json:"source"`
	// ScopeName is the org or team display name for an org/team key;
	// empty when unknown or for a personal key.
	ScopeName string `json:"scope_name,omitempty"`
	// ProfileIDs are the provider profiles currently bound to this key.
	ProfileIDs []string `json:"profile_ids"`
	// OrgDefault marks the org-designated default key for Provider.
	OrgDefault bool `json:"org_default,omitempty"`
	// Preselected marks the key the UI preselects for Provider: the org
	// default when there is one, otherwise the first personal key.
	Preselected bool `json:"preselected,omitempty"`
	// UserSelectable reports whether the user may pick this key. False
	// unless an explicit org policy allows user selection (fail closed).
	UserSelectable bool `json:"user_selectable"`
}

// LocalKeyID is the CredentialKey.ID of the local key bound to profileID.
func LocalKeyID(profileID string) string { return "local:" + profileID }

// CredentialPolicy is the org's credential policy as Fleet's
// GET /me/credentials/policy returns it (delivered from WP05). A nil
// *CredentialPolicy means the policy is not known, and every decision that
// depends on it fails closed.
type CredentialPolicy struct {
	// AllowUserSelection is the contract's allow_user_selection. It
	// defaults to FALSE: unless the org explicitly allows it, the user
	// does not pick among keys.
	AllowUserSelection bool
	// OrgDefaults maps a provider kind to the credential id the org
	// designated as that provider's default.
	OrgDefaults map[string]string
}

// BuildKeyList returns every key the given profiles reference, one row per
// distinct key, grouped by provider. policy is the org credential policy,
// or nil when it is not known (WP04 passes nil: nothing fetches it yet). scopeNames maps a Fleet credential id to the org
// or team display name; nil is fine.
func BuildKeyList(profs []ProviderProfile, policy *CredentialPolicy, scopeNames map[string]string) []CredentialKey {
	var orgDefaults map[string]string
	if policy != nil {
		orgDefaults = policy.OrgDefaults
	}
	byID := map[string]*CredentialKey{}
	var order []string
	for _, p := range profs {
		if p.Cred.Kind == "" && p.Cred.Locator == "" && p.Cred.CredentialID == "" {
			continue // no credential (e.g. a local runtime with auth none)
		}
		src := p.Cred.Source.Effective()
		id := LocalKeyID(p.ID)
		if src.IsFleet() {
			id = p.Cred.CredentialID
		}
		k, ok := byID[id]
		if !ok {
			k = &CredentialKey{ID: id, Provider: p.Kind, Source: src}
			if src.IsFleet() {
				k.ScopeName = scopeNames[id]
				k.OrgDefault = orgDefaults[p.Kind] == id && src != CredSourceUserSync
			}
			byID[id] = k
			order = append(order, id)
		}
		k.ProfileIDs = append(k.ProfileIDs, p.ID)
	}
	out := make([]CredentialKey, 0, len(order))
	for _, id := range order {
		k := byID[id]
		sort.Strings(k.ProfileIDs)
		k.UserSelectable = userKeySelectionAllowed(policy, *k)
		out = append(out, *k)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].ID < out[j].ID
	})
	markPreselected(out)
	return out
}

// markPreselected sets Preselected on one key per provider: the org default
// if present, otherwise the first personal key, otherwise the first key.
func markPreselected(keys []CredentialKey) {
	pick := map[string]int{}
	rank := func(k CredentialKey) int {
		switch {
		case k.OrgDefault:
			return 0
		case !k.Source.IsFleet() || k.Source == CredSourceUserSync:
			return 1
		default:
			return 2
		}
	}
	for i, k := range keys {
		j, ok := pick[k.Provider]
		if !ok || rank(k) < rank(keys[j]) {
			pick[k.Provider] = i
		}
	}
	for _, i := range pick {
		keys[i].Preselected = true
	}
}

// userKeySelectionAllowed is the enforcement hook for org policy over key
// selection (spec §5 WP12, §7a H2, §8 "a user override only if org
// allow_user_selection is set").
//
// FAIL CLOSED: the contract's allow_user_selection defaults to false, so a
// key is user-selectable only when an explicit policy says so. With no
// policy (nil — the state of every install until WP05 fetches it) nothing
// is selectable and the default key is used. WP12 adds the Cedar
// Credential family's "user-chosen keys forbidden" evaluation here; it can
// only narrow what this allows, never widen it.
func userKeySelectionAllowed(policy *CredentialPolicy, _ CredentialKey) bool {
	return policy != nil && policy.AllowUserSelection
}
