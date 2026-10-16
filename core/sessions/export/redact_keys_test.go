package export

import (
	"strings"
	"testing"
)

// OpenRouter keys (sk-or-v1-<hex>) were exported whole until 2026-10-14:
// the bare sk- shape stops at the '-' after "or". Assembled at runtime.
func TestRedact_OpenRouterKey(t *testing.T) {
	t.Parallel()
	key := "sk" + "-or-v1-" + strings.Repeat("0123456789abcdef", 4)
	got, matches := RedactValue("my openrouter key is " + key + ".")
	if strings.Contains(got, key[8:]) {
		t.Errorf("OpenRouter key bytes survived: %s", got)
	}
	if !strings.Contains(got, "<REDACTED:openrouter-key>") || len(matches) == 0 {
		t.Errorf("want <REDACTED:openrouter-key> marker and a match; got %q (%d matches)", got, len(matches))
	}
}
