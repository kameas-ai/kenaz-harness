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
	keys := BuildKeyList(profs, &CredentialPolicy{AllowUserSelection: true, OrgDefaults: map[string]string{"openai": "c-org"}}, map[string]string{"c-org": "Acme", "c-team": "Research"})
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

func TestBuildKeyList_NoOrgDefaultPrefersPersonal(t *testing.T) {
	keys := BuildKeyList([]ProviderProfile{
		{ID: "org-1", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Source: CredSourceOrg, CredentialID: "c-org"}},
		{ID: "mine", Kind: "openai", Cred: CredentialReference{Kind: "keychain", Locator: "kenaz-harness/mine"}},
	}, nil, nil)
	for _, k := range keys {
		if k.Preselected != (k.ID == LocalKeyID("mine")) {
			t.Errorf("%s preselected=%v", k.ID, k.Preselected)
		}
		if k.OrgDefault {
			t.Errorf("%s marked org default with no defaults supplied", k.ID)
		}
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
