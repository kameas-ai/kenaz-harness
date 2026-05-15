package custom

import (
	"testing"
)

// TestLoadEmbeddedTemplates verifies go:embed loads the YAML and returns
// exactly 9 templates with the required IDs.
func TestLoadEmbeddedTemplates(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}

	want := []string{
		"vllm", "together", "groq", "fireworks", "anyscale",
		"deepseek", "mistral", "llamacpp", "litellm",
	}
	if got := r.Len(); got != len(want) {
		t.Errorf("Len() = %d, want %d", got, len(want))
	}
	for _, id := range want {
		if r.Lookup(id) == nil {
			t.Errorf("Lookup(%q) = nil, want non-nil", id)
		}
	}
}

// TestResolveExplicit verifies explicit ID takes priority over glob.
func TestResolveExplicit(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}

	// Explicit groq ID should return groq regardless of host.
	got := r.Resolve("https://api.together.xyz", "groq")
	if got == nil {
		t.Fatal("Resolve with explicit id=groq returned nil")
	}
	if got.ID != "groq" {
		t.Errorf("Resolve explicit: got id=%q, want groq", got.ID)
	}
}

// TestResolveGlob verifies glob matching for api.together.xyz.
func TestResolveGlob(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}

	got := r.Resolve("https://api.together.xyz/v1", "")
	if got == nil {
		t.Fatal("Resolve glob for api.together.xyz returned nil")
	}
	if got.ID != "together" {
		t.Errorf("Resolve glob: got id=%q, want together", got.ID)
	}
}

// TestResolveNil verifies unknown host returns nil.
func TestResolveNil(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}
	if got := r.Resolve("https://unknown.example.com/v1", ""); got != nil {
		t.Errorf("Resolve unknown host: got %+v, want nil", got)
	}
}

// TestResolveUnknownExplicitID verifies unknown explicit ID returns nil.
func TestResolveUnknownExplicitID(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}
	if got := r.Resolve("https://api.groq.com", "nope"); got != nil {
		t.Errorf("Resolve unknown id: got %+v, want nil", got)
	}
}

// TestValidateRejectsDuplicateIDs verifies Validate catches duplicate ids.
func TestValidateRejectsDuplicateIDs(t *testing.T) {
	templates := []Template{
		{ID: "dup", Name: "Dup", BaseURL: "http://localhost:8000/v1", AuthScheme: AuthSchemeNone},
		{ID: "dup", Name: "Dup2", BaseURL: "http://localhost:8001/v1", AuthScheme: AuthSchemeNone},
	}
	_, err := newRegistry(templates)
	if err == nil {
		t.Error("expected error for duplicate id, got nil")
	}
}

// TestValidateRejectsMissingFields verifies Validate catches missing required fields.
func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		t    Template
	}{
		{"no id", Template{Name: "X", BaseURL: "http://localhost/v1", AuthScheme: AuthSchemeNone}},
		{"no name", Template{ID: "x", BaseURL: "http://localhost/v1", AuthScheme: AuthSchemeNone}},
		{"no base_url", Template{ID: "x", Name: "X", AuthScheme: AuthSchemeNone}},
		{"no auth_scheme", Template{ID: "x", Name: "X", BaseURL: "http://localhost/v1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newRegistry([]Template{tc.t})
			if err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestValidateRejectsAmbiguousGlob verifies that two non-explicit templates
// matching the fixture host api.together.xyz is rejected.
func TestValidateRejectsAmbiguousGlob(t *testing.T) {
	templates := []Template{
		{ID: "a", Name: "A", BaseURL: "http://x/v1", AuthScheme: AuthSchemeNone, Glob: "api.together.xyz"},
		{ID: "b", Name: "B", BaseURL: "http://y/v1", AuthScheme: AuthSchemeNone, Glob: "api.together.xyz"},
	}
	_, err := newRegistry(templates)
	if err == nil {
		t.Error("expected error for ambiguous glob, got nil")
	}
}

// TestAllReturnsAllTemplates verifies All() returns all entries.
func TestAllReturnsAllTemplates(t *testing.T) {
	r, err := LoadEmbeddedTemplates()
	if err != nil {
		t.Fatalf("LoadEmbeddedTemplates: %v", err)
	}
	all := r.All()
	if len(all) != 9 {
		t.Errorf("All() len = %d, want 9", len(all))
	}
}

// TestSummarize verifies the Summarize helper shape.
func TestSummarize(t *testing.T) {
	tmpl := Template{
		ID:         "groq",
		Name:       "Groq",
		BaseURL:    "https://api.groq.com/openai/v1",
		AuthScheme: AuthSchemeBearer,
		Notes:      "some notes",
	}
	s := Summarize(tmpl)
	if s.ID != "groq" || s.Name != "Groq" || s.BaseURL != tmpl.BaseURL || s.AuthScheme != AuthSchemeBearer {
		t.Errorf("Summarize mismatch: %+v", s)
	}
}

// TestAuthSchemeValues verifies all 4 AuthScheme constants are distinct.
func TestAuthSchemeValues(t *testing.T) {
	schemes := map[AuthScheme]bool{
		AuthSchemeBearer:       true,
		AuthSchemeAPIKeyHeader: true,
		AuthSchemeCustom:       true,
		AuthSchemeNone:         true,
	}
	if len(schemes) != 4 {
		t.Errorf("expected 4 distinct AuthScheme values, got %d", len(schemes))
	}
}

// TestHostFromURL verifies hostFromURL handles various inputs.
func TestHostFromURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://api.groq.com/openai/v1", "api.groq.com"},
		{"http://localhost:8000/v1", "localhost"},
		{"api.groq.com", "api.groq.com"},
		{"", ""},
	}
	for _, tc := range cases {
		got := hostFromURL(tc.in)
		if got != tc.want {
			t.Errorf("hostFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
