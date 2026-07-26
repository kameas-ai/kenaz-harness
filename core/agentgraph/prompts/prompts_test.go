package prompts

import (
	"strings"
	"testing"
)

// TestDefaultBaseConstitution_NonEmpty guards that the embed resolved.
func TestDefaultBaseConstitution_NonEmpty(t *testing.T) {
	if strings.TrimSpace(DefaultBaseConstitution()) == "" {
		t.Fatal("DefaultBaseConstitution() is empty; base.md embed failed")
	}
}

// TestDefaultBaseConstitution_TokenBudget is a rough guard so the shared
// constitution can't silently bloat and eat every model call's context
// budget. ~4 chars/token puts 3000 chars around 750 tokens.
func TestDefaultBaseConstitution_TokenBudget(t *testing.T) {
	const maxChars = 3000
	if n := len(DefaultBaseConstitution()); n >= maxChars {
		t.Errorf("DefaultBaseConstitution() = %d chars, want < %d", n, maxChars)
	}
}
