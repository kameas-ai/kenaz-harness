package telemetry_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/telemetry"
)

// TestInit_GenAISpanLandsInLocalStore: with no OTLP endpoint and no Fleet
// lane, a gen_ai.chat span emitted through the process provider that
// telemetry.Init installs lands in the real-sqlite telemetry_spans table —
// the "local exporter always" half of llm-call-telemetry-01LLMTEL01 —
// with its gen_ai.* attributes and no content.
//
// Not parallel: telemetry.Init sets the OTel global provider, which
// genaitrace emits through; sequential top-level tests finish before the
// package's parallel ones resume.
func TestInit_GenAISpanLandsInLocalStore(t *testing.T) {
	dataDir := t.TempDir()
	db := openStorageAt(t, dataDir)
	tel, err := telemetry.Init(context.Background(), telemetry.Config{DataDir: dataDir, Storage: db})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	const prompt = "SECRET-LOCAL-PROMPT"
	call := genaitrace.CallFromRequest(llm.GenerationRequest{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, prompt)},
	}, llm.ProviderProfile{Kind: "anthropic", Model: "claude-sonnet-4-5"})
	s, err := genaitrace.Instrument(context.Background(), call, nil, func(context.Context) (llm.Stream, error) {
		return &finished{resp: llm.Response{FinishReason: "end_turn", Usage: llm.Usage{InputTokens: 12, OutputTokens: 3},
			Meta: &llm.CallMeta{ResponseID: "msg_local_1"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	_, _ = s.Final()
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if !waitForRow(t, db, "SELECT COUNT(*) FROM telemetry_spans WHERE name='gen_ai.chat'", 2*time.Second) {
		t.Fatal("gen_ai.chat span did not land in telemetry_spans")
	}
	var kind, attrs string
	if err := db.Reader().QueryRow(context.Background(),
		`SELECT kind, attributes_json FROM telemetry_spans WHERE name='gen_ai.chat' LIMIT 1`).Scan(&kind, &attrs); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if kind != "client" {
		t.Errorf("kind = %q, want client", kind)
	}
	for _, want := range []string{"gen_ai.usage.input_tokens", "gen_ai.provider.name", "anthropic", "msg_local_1", "end_turn"} {
		if !strings.Contains(attrs, want) {
			t.Errorf("attributes_json lacks %q: %s", want, attrs)
		}
	}
	if strings.Contains(attrs, prompt) {
		t.Errorf("attributes_json carries prompt content: %s", attrs)
	}
}

type finished struct{ resp llm.Response }

func (f *finished) Events() <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}
func (f *finished) Cancel() error                { return nil }
func (f *finished) Final() (llm.Response, error) { return f.resp, nil }
