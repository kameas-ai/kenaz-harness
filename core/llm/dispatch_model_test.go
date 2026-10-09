package llm

import "testing"

// DispatchModel must report what the registry actually sends: the
// override, else Model — never Models[0], which no adapter dispatches.
func TestProviderProfile_DispatchModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		prof     ProviderProfile
		override string
		want     string
	}{
		{"override wins", ProviderProfile{Model: "a", Models: []string{"b"}}, "c", "c"},
		{"profile default", ProviderProfile{Model: "a", Models: []string{"b", "a"}}, "", "a"},
		{"no default is unset, not Models[0]", ProviderProfile{Models: []string{"b"}}, "", ""},
	}
	for _, tc := range cases {
		if got := tc.prof.DispatchModel(tc.override); got != tc.want {
			t.Errorf("%s: DispatchModel(%q) = %q, want %q", tc.name, tc.override, got, tc.want)
		}
	}
}
