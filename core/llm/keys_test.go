package llm

import "testing"

func TestBuildKeyList_ManyKeysPerProvider(t *testing.T) {
	profs := []ProviderProfile{
		{ID: "mine-a", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine-a"}},
		{ID: "mine-b", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine-b"}},
		{ID: "org-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceOrg, CredentialID: "c-org"}},
		{ID: "org-2", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceOrg, CredentialID: "c-org"}},
		{ID: "team-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceTeam, CredentialID: "c-team"}},
		{ID: "anth", Kind: "anthropic", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/anth"}},
		{ID: "local-runtime", Kind: "ollama"},
	}
	keys := BuildKeyList(profs, &CredentialPolicy{AllowUserSelection: true, AllowUserCredentials: true, OrgDefaults: map[string]string{"openai": "c-org"}}, map[string]string{"c-org": "Acme", "c-team": "Research"})
	byID := map[string]CredentialKey{}
	for _, k := range keys {
		byID[k.ID] = k
	}
	if len(keys) != 5 {
		t.Fatalf("got %d keys, want 5: %+v", len(keys), keys)
	}
	org := byID["c-org"]
	if org.Source != CredSourceOrg || org.ScopeName != "Acme" || !org.OrgDefault || !org.Preselected {
		t.Errorf("org key = %+v", org)
	}
	if len(org.ProfileIDs) != 2 {
		t.Errorf("org key should be shared by two profiles: %v", org.ProfileIDs)
	}
	if byID["c-team"].Source != CredSourceTeam || byID["c-team"].ScopeName != "Research" || byID["c-team"].Preselected {
		t.Errorf("team key = %+v", byID["c-team"])
	}
	for _, id := range []string{LocalKeyID("mine-a"), LocalKeyID("mine-b")} {
		k := byID[id]
		if k.Source != CredSourceLocal || k.Preselected || !k.UserSelectable {
			t.Errorf("%s = %+v", id, k)
		}
	}
	if !byID[LocalKeyID("anth")].Preselected {
		t.Error("with no org default, the first personal key is preselected")
	}
	if _, ok := byID[LocalKeyID("local-runtime")]; ok {
		t.Error("a profile with no credential is not a key")
	}
}

// TestDefaultKeyPrecedence pins the contract order: profile key → team
// default → org default → the user's own key, the last only when
// allow_user_credentials. The old markPreselected preferred a personal key
// over any non-default org key and had no team tier at all.
func TestDefaultKeyPrecedence(t *testing.T) {
	profs := []ProviderProfile{
		{ID: "mine", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine"}},
		{ID: "org-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceOrg, CredentialID: "c-org"}},
		{ID: "team-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceTeam, CredentialID: "c-team"}},
	}
	all := &CredentialPolicy{
		AllowUserCredentials: true,
		TeamDefaults:         map[string]string{"openai": "c-team"},
		OrgDefaults:          map[string]string{"openai": "c-org"},
	}
	preselected := func(pol *CredentialPolicy) string {
		t.Helper()
		var got []string
		for _, k := range BuildKeyList(profs, pol, nil) {
			if k.Preselected {
				got = append(got, k.ID)
			}
		}
		if len(got) > 1 {
			t.Fatalf("more than one preselected key: %v", got)
		}
		if len(got) == 0 {
			return ""
		}
		return got[0]
	}
	cases := []struct {
		name string
		pol  *CredentialPolicy
		want string
	}{
		{"team default outranks org default", all, "c-team"},
		{"org default when no team default", &CredentialPolicy{AllowUserCredentials: true, OrgDefaults: all.OrgDefaults}, "c-org"},
		{"user's own key only when allowed", &CredentialPolicy{AllowUserCredentials: true}, LocalKeyID("mine")},
		{"user keys not allowed: no default", &CredentialPolicy{}, ""},
		{"policy unknown: fail closed", nil, ""},
		{"a default that names no listed key is skipped", &CredentialPolicy{TeamDefaults: map[string]string{"openai": "gone"}, OrgDefaults: all.OrgDefaults}, "c-org"},
	}
	for _, tc := range cases {
		if got := preselected(tc.pol); got != tc.want {
			t.Errorf("%s: preselected %q, want %q", tc.name, got, tc.want)
		}
	}

	// Step 1: a profile's own key wins over every default.
	keys := BuildKeyList(profs, all, nil)
	i, ok := defaultKeyFor(keys, "openai", "c-org", all)
	if !ok || keys[i].ID != "c-org" {
		t.Fatalf("profile key not honoured: %v %v", ok, keys[i].ID)
	}
	// A profile key for another provider does not leak across.
	if i, ok := defaultKeyFor(keys, "openai", LocalKeyID("elsewhere"), all); !ok || keys[i].ID != "c-team" {
		t.Fatalf("unknown profile key should fall through to the team default")
	}
}

// TestDefaultKey_OrgDefaultFlagNeedsMatchingSource: an org default id that
// is actually a team or synced key is not marked as the org default.
func TestDefaultKey_OrgDefaultFlagNeedsMatchingSource(t *testing.T) {
	keys := BuildKeyList([]ProviderProfile{
		{ID: "u", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceUserSync, CredentialID: "c-1"}},
	}, &CredentialPolicy{OrgDefaults: map[string]string{"openai": "c-1"}}, nil)
	if keys[0].OrgDefault {
		t.Fatal("a synced user key was marked the org default")
	}
}

func TestCredentialSource_ZeroValueIsLocal(t *testing.T) {
	var s CredentialSource
	if s.Effective() != CredSourceLocal || s.IsFleet() || !s.Valid() {
		t.Fatal("zero source must mean local")
	}
	if CredentialSource("partner").Valid() {
		t.Fatal("unknown source accepted")
	}
}

// TestBuildKeyList_SelectionFailsClosed: allow_user_selection defaults to
// false, so with no policy, or a policy that does not allow it, no key is
// user-selectable. The old hook returned true unconditionally.
func TestBuildKeyList_SelectionFailsClosed(t *testing.T) {
	profs := []ProviderProfile{
		{ID: "mine", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine"}},
		{ID: "org-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceOrg, CredentialID: "c-org"}},
	}
	for name, pol := range map[string]*CredentialPolicy{"nil policy": nil, "explicit false": {}} {
		for _, k := range BuildKeyList(profs, pol, nil) {
			if k.UserSelectable {
				t.Errorf("%s: %s is user-selectable", name, k.ID)
			}
		}
	}
	for _, k := range BuildKeyList(profs, &CredentialPolicy{AllowUserSelection: true}, nil) {
		if !k.UserSelectable {
			t.Errorf("explicit allow: %s not selectable", k.ID)
		}
	}
}
