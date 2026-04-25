package lint

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestAnalyzer_PositiveAndNegative drives the go/analysis test
// harness against the fixture packages under ./testdata. Expected
// diagnostics are encoded as `// want "..."` comments inside the
// fixtures (the analysistest convention).
func TestAnalyzer_PositiveAndNegative(t *testing.T) {
	dir := analysistest.TestData()
	analysistest.Run(t, dir, Analyzer,
		"core/secrets/badfields",
		"core/secrets/goodfields",
		"core/secrets/badfunc",
		"core/secrets/badconv",
		"core/secrets/nosuppressnotest",
		"otherpkg",
	)
}

// TestNameRE_Smoke gives quick coverage on the credential-shape
// regular expression independent of the analyzer.
func TestNameRE_Smoke(t *testing.T) {
	hits := []string{
		"Secret", "Credential", "APIKey", "ApiKey", "Api_Key",
		"Token", "AccessToken", "RefreshToken", "Password",
		"AnthropicKey", "DBKey",
	}
	for _, h := range hits {
		if !nameRE.MatchString(h) {
			t.Errorf("expected nameRE hit: %q", h)
		}
	}
	misses := []string{
		"Buffer", "Reader", "Name", "ID", "Status",
	}
	for _, m := range misses {
		if nameRE.MatchString(m) {
			t.Errorf("unexpected nameRE hit: %q", m)
		}
	}
}

func TestInSecretsTree(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"github.com/sigil-tech/kaneaz-harness/core/secrets", true},
		{"github.com/sigil-tech/kaneaz-harness/core/secrets/ref", true},
		{"core/secrets", true},
		{"core/secrets/lint", true},
		{"github.com/sigil-tech/kaneaz-harness/core/event", false},
		{"otherpkg", false},
	}
	for _, c := range cases {
		if got := inSecretsTree(c.path); got != c.want {
			t.Errorf("inSecretsTree(%q)=%v want %v", c.path, got, c.want)
		}
	}
}
