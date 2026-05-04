package kind

import (
	"errors"
	"testing"
)

func TestBuiltInsRegistered(t *testing.T) {
	for _, k := range builtIn {
		if !IsRegistered(k) {
			t.Errorf("built-in %q should be registered", k)
		}
	}
}

func TestPermissionKindsRegistered(t *testing.T) {
	permKinds := []Kind{
		KindPermissionGranted,
		KindPermissionDenied,
		KindPermissionPrompted,
		KindPermissionTimeout,
		KindPermissionRevoked,
		KindBashPermission,
		KindFilesystemPermission,
		KindCredentialPermission,
		KindToolPermission,
	}
	for _, k := range permKinds {
		if !IsRegistered(k) {
			t.Errorf("permission kind %q should be registered on init", k)
		}
	}
}

func TestMCPRecipeTestedRegistered(t *testing.T) {
	if !IsRegistered(KindMCPRecipeTested) {
		t.Errorf("KindMCPRecipeTested %q should be registered as a built-in", KindMCPRecipeTested)
	}
	if err := Validate(KindMCPRecipeTested); err != nil {
		t.Errorf("KindMCPRecipeTested should be valid: %v", err)
	}
}

func TestValidateGoodKinds(t *testing.T) {
	good := []Kind{
		"llm.request.started",
		"mcp.tool.invoked",
		"mcp.client.connect-attempt",
		"a2a.card.received",
		"event-log.retention.started",
		"scheduler.cron-fired",
		"foo.bar",
	}
	for _, k := range good {
		if err := Validate(k); err != nil {
			t.Errorf("%q should validate, got %v", k, err)
		}
	}
}

func TestValidateBadKinds(t *testing.T) {
	bad := []Kind{
		"",
		"nodot",
		".leading",
		"trailing.",
		"double..dot",
		"has space.x",
		"x.has space",
		"foo/bar",     // slash not allowed
		"FOO.bar?",    // bad char
	}
	for _, k := range bad {
		err := Validate(k)
		if err == nil {
			t.Errorf("%q should fail validation", k)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("expected ErrInvalid for %q, got %v", k, err)
		}
	}
}

func TestRegisterIdempotent(t *testing.T) {
	k := Kind("test-mission.something.happened")
	if IsRegistered(k) {
		t.Fatalf("kind should not be pre-registered")
	}
	if err := Register(k); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !IsRegistered(k) {
		t.Fatalf("kind should be registered after Register")
	}
	if err := Register(k); err != nil {
		t.Fatalf("re-register should be idempotent: %v", err)
	}
}

func TestUnknownButWellFormedKindAccepted(t *testing.T) {
	// Forward-compat: Validate accepts unknown well-formed kinds.
	k := Kind("future-mission.unknown.kind-v9")
	if err := Validate(k); err != nil {
		t.Fatalf("forward-compat unknown kind should validate: %v", err)
	}
	if IsRegistered(k) {
		t.Fatalf("unknown kind should NOT report IsRegistered until Register is called")
	}
}

// TestMCPRecipeKindsRegistered asserts that the three MCP recipe
// lifecycle audit kinds introduced by mission mcp-server-install-01KQ8TDP
// (WP10) are registered as built-ins at package init time.
func TestMCPRecipeKindsRegistered(t *testing.T) {
	mcpKinds := []Kind{
		KindMCPRecipeAdded,
		KindMCPRecipeRemoved,
		KindMCPRecipeTested,
	}
	for _, k := range mcpKinds {
		if !IsRegistered(k) {
			t.Errorf("MCP recipe kind %q should be registered as a built-in", k)
		}
		if err := Validate(k); err != nil {
			t.Errorf("MCP recipe kind %q should validate: %v", k, err)
		}
	}
}

// TestMCPRecipeKindsInBuiltIn asserts that all three MCP kinds appear
// in the builtIn slice (regression guard against accidental removal).
func TestMCPRecipeKindsInBuiltIn(t *testing.T) {
	want := map[Kind]bool{
		KindMCPRecipeAdded:   false,
		KindMCPRecipeRemoved: false,
		KindMCPRecipeTested:  false,
	}
	for _, k := range builtIn {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("kind %q missing from builtIn slice", k)
		}
	}
}
