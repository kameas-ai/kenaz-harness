package fallback

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadBundled(t *testing.T) {
	t.Parallel()

	chains, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled() error = %v", err)
	}
	if len(chains) < 2 {
		t.Errorf("LoadBundled() returned %d chains, want >= 2", len(chains))
	}

	// All bundled chains must validate.
	for _, c := range chains {
		if err := c.Validate(); err != nil {
			t.Errorf("bundled chain %q Validate() = %v", c.ID, err)
		}
	}
}

func TestLoadBundledYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	chains, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled() error = %v", err)
	}
	if len(chains) == 0 {
		t.Skip("no bundled chains")
	}

	for _, orig := range chains {
		orig := orig
		t.Run(orig.ID, func(t *testing.T) {
			t.Parallel()

			// Marshal the original chain back to YAML then re-parse.
			data, err := yaml.Marshal(orig)
			if err != nil {
				t.Fatalf("yaml.Marshal(%q) error = %v", orig.ID, err)
			}
			got, err := parseChain(data)
			if err != nil {
				t.Fatalf("parseChain(marshal(%q)) error = %v", orig.ID, err)
			}
			if got.ID != orig.ID {
				t.Errorf("round-trip ID: got %q, want %q", got.ID, orig.ID)
			}
			if got.Name != orig.Name {
				t.Errorf("round-trip Name: got %q, want %q", got.Name, orig.Name)
			}
			if len(got.Entries) != len(orig.Entries) {
				t.Errorf("round-trip Entries len: got %d, want %d",
					len(got.Entries), len(orig.Entries))
			}
			for i, e := range orig.Entries {
				if i >= len(got.Entries) {
					break
				}
				if got.Entries[i].ProviderID != e.ProviderID {
					t.Errorf("entry[%d] ProviderID: got %q, want %q",
						i, got.Entries[i].ProviderID, e.ProviderID)
				}
			}
		})
	}
}
