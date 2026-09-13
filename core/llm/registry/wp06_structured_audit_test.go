package registry

// wp06_structured_audit_test.go — structured-output-is-reachable-01PMZE14
// WP06: audit.KindLLMStructuredResponse gets its first emitter, and
// structured.SchemaHash gets its first production caller.
//
// AC-008: three drives (valid first try -> "passed"/Attempts:1,
// invalid-then-repaired -> "retry_passed"/Attempts:2, Mode:"json" ->
// "skipped") asserting FormatMode, ValidationOutcome, Attempts and
// SchemaHash equal to the real SHA-256 digest — never merely non-empty.
//
// AC-016: SchemaHash equals structured.SchemaHash(schema) and the call
// site is core/llm/registry (this package), not a hand-rolled digest.

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/structured"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// recordingAuditEmitter is a race-safe fake contextaudit.Emitter
// (CLAUDE.md § Race-safe test fakes — structuredStream.Final() runs off
// the stream's own goroutine relative to the test body in real usage;
// even though these tests call Final() synchronously, the mutex +
// snapshot pattern is the mandated shape for any fake that could be
// written from a goroutine, and costs nothing here).
type recordingAuditEmitter struct {
	mu     sync.Mutex
	events []contextaudit.Event
}

func (e *recordingAuditEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
	return nil
}

func (e *recordingAuditEmitter) snapshot() []contextaudit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contextaudit.Event, len(e.events))
	copy(out, e.events)
	return out
}

// decodeLLMStructuredResponse unmarshals ev.Payload into the typed
// payload struct so assertions read real fields, not raw JSON.
func decodeLLMStructuredResponse(t *testing.T, ev contextaudit.Event) contextaudit.LLMStructuredResponsePayload {
	t.Helper()
	var p contextaudit.LLMStructuredResponsePayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("decode LLMStructuredResponsePayload: %v", err)
	}
	return p
}

func TestRegistry_StructuredOutputAudit_PassedFirstTry(t *testing.T) {
	r, adapter := newRegForStructured(t)
	em := &recordingAuditEmitter{}
	r.audit = em
	adapter.respByCall = []llm.Response{jsonResp(`{"name":"ok"}`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 audit event, got %d", len(evs))
	}
	if evs[0].Kind != contextaudit.KindLLMStructuredResponse {
		t.Errorf("Kind = %q, want %q", evs[0].Kind, contextaudit.KindLLMStructuredResponse)
	}
	p := decodeLLMStructuredResponse(t, evs[0])
	if p.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", p.Provider, "anthropic")
	}
	if p.Model != "claude-sonnet-4-7" {
		t.Errorf("Model = %q, want %q", p.Model, "claude-sonnet-4-7")
	}
	if p.FormatMode != "json_schema" {
		t.Errorf("FormatMode = %q, want %q", p.FormatMode, "json_schema")
	}
	if p.ValidationOutcome != "passed" {
		t.Errorf("ValidationOutcome = %q, want %q", p.ValidationOutcome, "passed")
	}
	if p.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (hardcoding is the class this mission exists to end)", p.Attempts)
	}
	wantHash := structured.SchemaHash([]byte(nameRequiredSchema))
	if wantHash == "" {
		t.Fatal("test bug: structured.SchemaHash returned empty for a non-empty schema")
	}
	if p.SchemaHash != wantHash {
		t.Errorf("SchemaHash = %q, want %q (structured.SchemaHash(schema) — asserting equality, not mere non-emptiness)", p.SchemaHash, wantHash)
	}
}

func TestRegistry_StructuredOutputAudit_RetryPassed(t *testing.T) {
	r, adapter := newRegForStructured(t)
	em := &recordingAuditEmitter{}
	r.audit = em
	adapter.respByCall = []llm.Response{
		jsonResp(`{}`),
		jsonResp(`{"name":"fixed"}`),
	}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 audit event (one per Final(), not per adapter call), got %d", len(evs))
	}
	p := decodeLLMStructuredResponse(t, evs[0])
	if p.ValidationOutcome != "retry_passed" {
		t.Errorf("ValidationOutcome = %q, want %q", p.ValidationOutcome, "retry_passed")
	}
	if p.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 (the real count: 1 repair fired)", p.Attempts)
	}
	if n := atomic.LoadInt32(&adapter.calls); n != 2 {
		t.Fatalf("expected exactly 2 adapter calls, got %d", n)
	}
}

func TestRegistry_StructuredOutputAudit_FailedAfterRepair(t *testing.T) {
	r, adapter := newRegForStructured(t)
	em := &recordingAuditEmitter{}
	r.audit = em
	adapter.respByCall = []llm.Response{
		jsonResp(`{}`),
		jsonResp(`{}`),
	}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr == nil {
		t.Fatal("expected ErrResponseValidationFailed, got nil")
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 audit event even on a validation failure (audited regardless of outcome), got %d", len(evs))
	}
	p := decodeLLMStructuredResponse(t, evs[0])
	if p.ValidationOutcome != "failed" {
		t.Errorf("ValidationOutcome = %q, want %q", p.ValidationOutcome, "failed")
	}
	if p.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 (the retry fired even though it also failed)", p.Attempts)
	}
}

// TestRegistry_StructuredOutputAudit_JSONModeSkipped verifies Mode:"json"
// (no schema) audits as "skipped" with an empty SchemaHash, per the
// payload doc comment's stated convention.
func TestRegistry_StructuredOutputAudit_JSONModeSkipped(t *testing.T) {
	r, adapter := newRegForStructured(t)
	em := &recordingAuditEmitter{}
	r.audit = em
	adapter.respByCall = []llm.Response{jsonResp(`{"anything":"goes"}`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json"},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}

	evs := em.snapshot()
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 audit event, got %d", len(evs))
	}
	p := decodeLLMStructuredResponse(t, evs[0])
	if p.FormatMode != "json" {
		t.Errorf("FormatMode = %q, want %q", p.FormatMode, "json")
	}
	if p.ValidationOutcome != "skipped" {
		t.Errorf("ValidationOutcome = %q, want %q (Mode=json has no schema to validate)", p.ValidationOutcome, "skipped")
	}
	if p.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", p.Attempts)
	}
	if p.SchemaHash != "" {
		t.Errorf("SchemaHash = %q, want empty (Mode=json supplies no schema)", p.SchemaHash)
	}
}

// TestRegistry_StructuredOutputAudit_GrammarModeNotAudited pins that
// grammar mode — enforced by the runtime's token sampler, not this
// package's validation loop — never reaches structuredStream and so
// never emits KindLLMStructuredResponse. This is a scope boundary, not
// an oversight: auditing a mode this package never validates would
// misrepresent what "ValidationOutcome" means.
func TestRegistry_StructuredOutputAudit_GrammarModeNotAudited(t *testing.T) {
	const key = "TEST_REG_STRUCTURED_AUDIT_GRAMMAR_KEY"
	t.Setenv(key, "secret-bytes")
	r, _ := newReg(t)
	r.resolver = credref.New(secrets.NewMemoryBackend())
	em := &recordingAuditEmitter{}
	r.audit = em
	adapter := &fakeAdapter{kind: "ollama"}
	r.RegisterAdapter(adapter)
	prof := llm.ProviderProfile{
		ID: "p", Kind: "ollama", Model: "llama3.1",
		Cred:            llm.CredentialReference{Kind: "env", Locator: key},
		CapabilityHints: map[llm.Capability]bool{llm.CapGrammar: true},
	}
	if err := r.LoadProfiles([]llm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	adapter.respByCall = []llm.Response{jsonResp(`not json at all`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "grammar"},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr != nil {
		t.Fatalf("Final: unexpected error: %v", ferr)
	}
	if evs := em.snapshot(); len(evs) != 0 {
		t.Fatalf("expected 0 audit events for grammar mode, got %d", len(evs))
	}
}

// TestRegistry_StructuredOutputAudit_NilEmitterIsNoop verifies that a
// Registry with no Options.Audit set behaves exactly as it did before
// this WP — nil is a fully supported, zero-cost configuration.
func TestRegistry_StructuredOutputAudit_NilEmitterIsNoop(t *testing.T) {
	r, adapter := newRegForStructured(t) // r.audit left nil
	adapter.respByCall = []llm.Response{jsonResp(`{"name":"ok"}`)}

	req := llm.GenerationRequest{
		ProfileID:      "p",
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: []byte(nameRequiredSchema)},
	}
	stream, err := r.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	if _, ferr := stream.Final(); ferr != nil {
		t.Fatalf("Final: unexpected error with nil audit emitter: %v", ferr)
	}
}
