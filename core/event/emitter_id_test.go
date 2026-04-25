package event

import (
	"errors"
	"testing"
)

func TestValidateEmitterID_Allowlist(t *testing.T) {
	good := []string{
		"llm/",
		"llm/anthropic",
		"llm/openai",
		"mcp/client",
		"mcp/server",
		"mcp/client/socket-7",
		"a2a/",
		"scheduler/",
		"bundle/",
		"trust/",
		"context/",
		"session/",
		"event-log/",
		"storage/",
	}
	for _, eid := range good {
		if err := ValidateEmitterID(EmitterID(eid)); err != nil {
			t.Errorf("expected %q to validate, got %v", eid, err)
		}
	}

	bad := []string{
		"",
		"unknown/",
		"unknown",
		"llm",        // no slash
		"llmanything", // not actually prefixed by "llm/"
		"foo/bar",
		"mcp/clien",  // truncated
		"mcp/clients", // not "mcp/client" or "mcp/client/..."
		"trust",
		" llm/",
		"llm/anth\nropic", // control char
	}
	for _, eid := range bad {
		err := ValidateEmitterID(EmitterID(eid))
		if err == nil {
			t.Errorf("expected %q to be rejected", eid)
			continue
		}
		if !errors.Is(err, ErrUnknownEmitter) {
			t.Errorf("expected ErrUnknownEmitter for %q, got %v", eid, err)
		}
	}
}

func TestRegisterEmitterPrefix(t *testing.T) {
	prefix := "test-mission/"
	if err := ValidateEmitterID(EmitterID("test-mission/abc")); err == nil {
		t.Fatalf("prefix should be unregistered initially")
	}
	if err := RegisterEmitterPrefix(prefix); err != nil {
		t.Fatalf("RegisterEmitterPrefix: %v", err)
	}
	if err := ValidateEmitterID(EmitterID("test-mission/abc")); err != nil {
		t.Fatalf("registered prefix should accept: %v", err)
	}
	// Idempotent.
	if err := RegisterEmitterPrefix(prefix); err != nil {
		t.Fatalf("re-register should be idempotent: %v", err)
	}
}

func TestRegisteredEmitterPrefixesReturnsSorted(t *testing.T) {
	got := RegisteredEmitterPrefixes()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("prefixes not sorted: %q > %q", got[i-1], got[i])
		}
	}
}
