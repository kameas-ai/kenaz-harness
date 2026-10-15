package logstore

import (
	"strings"
	"testing"
)

// Current provider keys and GitHub tokens reached the Logs view whole
// before 2026-10-15: the only sk- rule was alphanumeric-only. Assembled at
// runtime so the repo holds no scanner-flaggable literal.
func TestRedactMessage_CurrentKeyShapes(t *testing.T) {
	body := strings.Repeat("0a1B-_", 8)
	for _, key := range []string{
		"sk" + "-ant-api03-" + body,
		"sk" + "-proj-" + body,
		"sk" + "-svcacct-" + body,
		"sk" + "-or-v1-" + strings.Repeat("0123456789abcdef", 4),
		"gh" + "p_" + strings.Repeat("A1b2", 10),
		"github" + "_pat_" + strings.Repeat("A1_b", 8),
	} {
		got := RedactMessage("mcp stderr: auth failed for " + key + " (401)")
		if strings.Contains(got, key[6:18]) || !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%.10s…: got %q", key, got)
		}
	}
}
