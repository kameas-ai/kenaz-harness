package events_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/events"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

func TestAllKinds_Closed(t *testing.T) {
	if len(events.AllKinds()) != 10 {
		t.Errorf("expected 10 kinds, got %d", len(events.AllKinds()))
	}
}

func TestNewResolutionEvent_RedactionSafe(t *testing.T) {
	r := ref.CredentialReference{
		Kind:       ref.RefKeychain,
		Locator:    "anthropic-api-key",
		ConsumerID: "llm:anthropic",
	}
	e := events.NewResolutionEvent(r, "llm:anthropic")
	js, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "anthropic-api-key") {
		t.Errorf("event JSON contains locator: %s", js)
	}
	if !strings.Contains(string(js), r.ID()) {
		t.Errorf("event JSON missing redaction-safe id: %s", js)
	}
}

func TestBufferingLogger_AppendDrain(t *testing.T) {
	b := events.NewBufferingLogger()
	r := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	e := events.NewResolutionEvent(r, "")
	if err := b.Append(context.Background(), events.KindResolutionRequested, e); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 1 {
		t.Errorf("Len=%d", b.Len())
	}
	d := b.Drain()
	if len(d) != 1 {
		t.Errorf("Drain len=%d", len(d))
	}
	if b.Len() != 0 {
		t.Error("Drain must reset buffer")
	}
}
