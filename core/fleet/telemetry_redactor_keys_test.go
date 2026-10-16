package fleet

import (
	"strings"
	"testing"
)

// providerKeyCorpus is every current provider key shape whose body holds
// '-' or '_' (which the bare sk-<alnum> rule cannot match), assembled at
// runtime so the repo carries no scanner-flaggable literal. OpenRouter keys
// were exported whole until 2026-10-14.
func providerKeyCorpus() map[string]string {
	body := strings.Repeat("0a1B-_", 8)
	return map[string]string{
		"sk" + "-or-v1-" + strings.Repeat("0123456789abcdef", 4): "[REDACTED:openrouter-key]",
		"sk" + "-svcacct-" + body:                                "[REDACTED:openai-key]",
		"sk" + "-admin-" + body:                                  "[REDACTED:openai-key]",
		"sk" + "-proj-" + body:                                   "[REDACTED:openai-key]",
		"gh" + "p_" + strings.Repeat("A1b2", 10):                 "[REDACTED:github-token]",
		"github" + "_pat_" + strings.Repeat("A1_b", 8):           "[REDACTED:github-token]",
	}
}

func TestTelemetryRedactor_CurrentProviderKeyShapes(t *testing.T) {
	r := NewTelemetryRedactor()
	for key, marker := range providerKeyCorpus() {
		got := r.RedactString("Authorization: token " + key + " end")
		if strings.Contains(got, key[8:20]) || !strings.Contains(got, marker) {
			t.Errorf("%.12s…: got %q, want %s and no key bytes", key, got, marker)
		}
	}
}
