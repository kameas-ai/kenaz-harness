package contextbootstrap

import (
	"strings"
	"testing"
)

// Prefixed keys whose bodies hold '-' / '_' (OpenAI project, service
// account, admin; OpenRouter) slipped past the bare sk-<alnum> pattern into
// bootstrap source content until 2026-10-14. Assembled at runtime.
func TestStripSecrets_CurrentProviderKeyShapes(t *testing.T) {
	body := strings.Repeat("0a1B-_", 8)
	for _, key := range []string{
		"sk" + "-or-v1-" + strings.Repeat("0123456789abcdef", 4),
		"sk" + "-proj-" + body,
		"sk" + "-svcacct-" + body,
		"sk" + "-admin-" + body,
		"gh" + "p_" + strings.Repeat("A1b2", 10),
		"github" + "_pat_" + strings.Repeat("A1_b", 8),
	} {
		got := stripSecrets("OPENAI_API_KEY=" + key + "\n")
		if strings.Contains(got, key[8:20]) || !strings.Contains(got, redactedPlaceholder) {
			t.Errorf("%.12s…: got %q", key, got)
		}
	}
}
