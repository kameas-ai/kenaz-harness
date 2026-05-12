package secretref

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event/kind"
)

func TestResolvedEvent_JSONRoundTrip(t *testing.T) {
	e := NewResolvedEvent(
		"ses_abc",
		"chat",
		"web_fetch",
		"user:example-api-token",
		"api.example.com",
		"permit-rule-01",
		OutcomePermit,
		"session exposed",
		"",
	)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var e2 ResolvedEvent
	if err := json.Unmarshal(raw, &e2); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if e2.SessionID != e.SessionID {
		t.Errorf("SessionID: got %q want %q", e2.SessionID, e.SessionID)
	}
	if e2.Locator != e.Locator {
		t.Errorf("Locator: got %q want %q", e2.Locator, e.Locator)
	}
	if e2.Outcome != OutcomePermit {
		t.Errorf("Outcome: got %q want %q", e2.Outcome, OutcomePermit)
	}
	if e2.DestinationHost != "api.example.com" {
		t.Errorf("DestinationHost: got %q", e2.DestinationHost)
	}
	// Verify ResolvedAt round-trips with UTC timezone.
	if e2.ResolvedAt.IsZero() {
		t.Error("ResolvedAt is zero after round-trip")
	}
}

// TestNoPlaintextKeys asserts that the marshalled payload does not
// contain any of the privacy-sensitive field names that would indicate
// credential bytes, hashes, or fingerprints leaked into the event.
func TestNoPlaintextKeys(t *testing.T) {
	forbidden := []string{`"secret"`, `"value"`, `"bytes"`, `"hash"`, `"fingerprint"`}
	e := NewResolvedEvent(
		"ses_abc", "chat", "bash",
		"user:my-token", "", "deny-rule-01",
		OutcomeDenied, "agent_kind=untrusted", "",
	)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	payload := string(raw)
	for _, key := range forbidden {
		if strings.Contains(payload, key) {
			t.Errorf("marshalled payload contains forbidden JSON key %q: %s", key, payload)
		}
	}
}

// TestKindRegistered ensures the kind is accessible from the registry.
func TestKindRegistered(t *testing.T) {
	if !kind.IsRegistered(kind.KindSecretReferenceResolved) {
		t.Error("KindSecretReferenceResolved not registered in kind registry")
	}
}

// TestOutcomes verifies all Outcome constants are non-empty strings.
func TestOutcomes(t *testing.T) {
	outcomes := []Outcome{
		OutcomePermit, OutcomeDenied, OutcomeUnknownLocator,
		OutcomeBudgetExhausted, OutcomeUnavailable, OutcomeInvalidReference,
	}
	seen := make(map[Outcome]bool)
	for _, o := range outcomes {
		if o == "" {
			t.Error("empty Outcome constant")
		}
		if seen[o] {
			t.Errorf("duplicate Outcome constant: %q", o)
		}
		seen[o] = true
	}
}

func TestResolvedAt_isUTC(t *testing.T) {
	before := time.Now().UTC()
	e := NewResolvedEvent("s", "a", "t", "l", "", "", OutcomePermit, "", "")
	after := time.Now().UTC()
	if e.ResolvedAt.Before(before) || e.ResolvedAt.After(after) {
		t.Errorf("ResolvedAt %v not in [%v, %v]", e.ResolvedAt, before, after)
	}
	if e.ResolvedAt.Location() != time.UTC {
		t.Errorf("ResolvedAt not UTC: %v", e.ResolvedAt.Location())
	}
}
