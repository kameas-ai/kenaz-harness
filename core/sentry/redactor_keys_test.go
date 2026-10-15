package sentry_test

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/sentry"
)

// Current provider key shapes, assembled at runtime (no literal in the
// repo). OpenRouter keys reached crash reports whole until 2026-10-14.
func TestRedactString_CurrentProviderKeyShapes(t *testing.T) {
	body := strings.Repeat("0a1B-_", 8)
	for key, marker := range map[string]string{
		"sk" + "-or-v1-" + strings.Repeat("0123456789abcdef", 4): "[REDACTED:openrouter-key]",
		"sk" + "-svcacct-" + body:                                 "[REDACTED:openai-key]",
		"sk" + "-admin-" + body:                                   "[REDACTED:openai-key]",
	} {
		got := sentry.RedactString("key=" + key)
		if strings.Contains(got, key[8:20]) || !strings.Contains(got, marker) {
			t.Errorf("%.12s…: got %q, want %s and no key bytes", key, got, marker)
		}
	}
}
