package audit_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// TestMCPHealthChangedPayload_Marshal verifies that KindMCPHealthChanged is
// declared and that MCPHealthChangedPayload round-trips through JSON without
// data loss. (mcp-server-health-ui-01KQ8TD6 WP07)
func TestMCPHealthChangedPayload_Marshal(t *testing.T) {
	payload := audit.MCPHealthChangedPayload{
		RecipeID:        "brave-search",
		PreviousState:   "running",
		NewState:        "failed",
		RestartAttempts: 2,
		ErrorClass:      "ping_timeout",
	}

	event, err := audit.Marshal(audit.KindMCPHealthChanged, payload, time.Now())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if event.Kind != audit.KindMCPHealthChanged {
		t.Errorf("kind: want %q, got %q", audit.KindMCPHealthChanged, event.Kind)
	}

	// Round-trip through JSON.
	var got audit.MCPHealthChangedPayload
	if err := json.Unmarshal(event.Payload, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RecipeID != "brave-search" {
		t.Errorf("RecipeID: want brave-search, got %q", got.RecipeID)
	}
	if got.NewState != "failed" {
		t.Errorf("NewState: want failed, got %q", got.NewState)
	}
	if got.ErrorClass != "ping_timeout" {
		t.Errorf("ErrorClass: want ping_timeout, got %q", got.ErrorClass)
	}
	if got.RestartAttempts != 2 {
		t.Errorf("RestartAttempts: want 2, got %d", got.RestartAttempts)
	}
}

// TestKindMCPHealthChanged_IsNonEmpty is a canary: if the constant is
// accidentally empty a downstream consumer can't distinguish it from
// a zero-value kind.
func TestKindMCPHealthChanged_IsNonEmpty(t *testing.T) {
	if audit.KindMCPHealthChanged == "" {
		t.Error("KindMCPHealthChanged must not be the empty string")
	}
}
