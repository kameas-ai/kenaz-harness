package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestEmitter_ErrorAndRetryRedactGenericCredentialShapes: defence in
// depth for persisted error payloads — an error path that forgot to
// scrub still cannot persist a Bearer token or a ?api_key= value.
func TestEmitter_ErrorAndRetryRedactGenericCredentialShapes(t *testing.T) {
	sink := &MemorySink{}
	e := New(sink)
	leak := "upstream said: Authorization: Bearer SECRETtoken123 at /v1?api_key=SECRETquery9"
	_ = e.Error(context.Background(), llm.GenerationRequest{}, llm.ProviderProfile{}, errors.New(leak))
	_ = e.RetryAttempted(context.Background(), llm.GenerationRequest{}, llm.ProviderProfile{}, 1, 1, 1, leak)
	raw, _ := json.Marshal(sink.Entries())
	for _, s := range []string{"SECRETtoken123", "SECRETquery9"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("persisted entries leak %q: %s", s, raw)
		}
	}
}
