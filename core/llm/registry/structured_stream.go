package registry

import (
	"context"
	"encoding/json"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/structured"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// structuredStream wraps the primary attempt's llm.Stream for a request
// that opted into a JSON response format (model-request-path-live-01PMDL01
// WP04). On Final(), it validates the terminal Response against the
// request's JSON schema via structured.Validate; on the first failure it
// fires exactly one corrective retry (via structured.WithRetry) naming the
// specific field violation, and surfaces the typed
// llm.ErrResponseValidationFailed if the retry also fails.
//
// It must wrap the *raw* retry-middleware stream (i.e. sit *inside* the
// auditedStream wrapper, not outside it) so that:
//   - the credential bytes used by a corrective retry call are still live
//     (auditedStream zeroes them only after its own Final()/Cancel() runs,
//     which happens after this wrapper's Final() returns);
//   - the audit trail's terminal response_final event reflects the
//     validated/repaired response, not the pre-repair one.
//
// Events() is a pure pass-through of the primary attempt's stream; the
// corrective retry (when it happens) is a Final()-time operation only — a
// structured-output repair inherently requires the complete response
// before it can be validated, so there is nothing meaningful to stream
// mid-repair.
type structuredStream struct {
	inner llm.Stream

	schema json.RawMessage
	mode   string // "json" | "json_schema" | "grammar"
	strict bool

	// retry performs exactly one live re-invocation of the adapter with a
	// corrective hint appended to the conversation, returning the new
	// terminal Response.
	retry func(ctx context.Context, priorResp llm.Response, hint string) (llm.Response, error)
	ctx   context.Context

	// provider/model identify the call for the audit trail
	// (structured-output-is-reachable-01PMZE14 WP06). Set by
	// Registry.Stream from the resolved ProviderProfile — prof.Kind and
	// prof.Model (the latter already resolved to the actual per-call
	// override, see registry.go's wantModel block), never from the raw
	// request, so the audit trail always names the model that actually
	// reached the wire.
	provider string
	model    string
	// audit is the optional structured-output audit sink. Nil is safe —
	// contextaudit.MustEmit no-ops on a nil emitter — so tests and
	// call sites that never set Options.Audit pay nothing.
	audit contextaudit.Emitter
}

func (s *structuredStream) Events() <-chan llm.StreamEvent { return s.inner.Events() }
func (s *structuredStream) Cancel() error                  { return s.inner.Cancel() }

// Final blocks on the inner stream's Final(), then validates + (at most
// once) repairs the response per the ResponseFormat contract.
func (s *structuredStream) Final() (llm.Response, error) {
	resp, err := s.inner.Final()
	if err != nil {
		return resp, err
	}

	// Grammar mode is enforced by the runtime's token-level constraints
	// (local llama.cpp/Ollama grammar sampler); there is no JSON-schema
	// text to re-validate here.
	if s.mode == "grammar" {
		return resp, nil
	}

	log := logging.L()

	// data is a closure-captured slot so retryCall (below) can hand back
	// the repaired Response alongside the raw bytes structured.WithRetry
	// wants to validate. retryAttempted is set the instant the retry
	// closure is invoked — independent of whether that retry's own
	// output later validates — so the audit trail's Attempts count
	// reflects a real second LLM call, not merely "the repair
	// succeeded" (structured-output-is-reachable-01PMZE14 WP06, AC-008:
	// "Attempts must be the real count... hardcoding 1 reproduces the
	// class this mission exists to end").
	var repaired *llm.Response
	var retryAttempted bool

	data, werr := structured.WithRetry(
		s.ctx,
		s.schema,
		func(_ context.Context) ([]byte, error) {
			return []byte(llm.Message{Content: resp.Content}.Text()), nil
		},
		func(ctx context.Context, hint string) ([]byte, error) {
			retryAttempted = true
			log.Debug("registry.stream.structured_repair_attempt",
				"mode", s.mode, "hint", hint)
			resp2, rerr := s.retry(ctx, resp, hint)
			if rerr != nil {
				return nil, rerr
			}
			repaired = &resp2
			return []byte(llm.Message{Content: resp2.Content}.Text()), nil
		},
		s.mode,
		s.strict,
		1, // exactly one corrective retry
	)
	_ = data // the validated bytes; the caller consumes the typed Response, not raw bytes.

	final := resp
	if repaired != nil {
		final = *repaired
	}
	s.emitAudit(werr, retryAttempted, final)

	if werr != nil {
		return llm.Response{}, werr
	}
	return final, nil
}

// emitAudit fires audit.KindLLMStructuredResponse once per Final() call
// that reaches schema validation (mode "json" or "json_schema"; grammar
// mode returns before this point and is never audited here — it is
// enforced by the runtime's token sampler, not by this validation loop).
//
// ValidationOutcome classification (audit.go's LLMStructuredResponsePayload
// doc comment is the source of truth for the four values):
//   - Mode "json" carries no schema to validate against, so the outcome
//     is always "skipped" regardless of whether a JSON-well-formedness
//     retry fired underneath — matching the payload doc's "skipped —
//     Mode=json or Mode=grammar (no schema to validate)".
//   - Mode "json_schema": werr != nil -> "failed"; a retry fired and the
//     final result is valid -> "retry_passed"; neither -> "passed".
//
// SchemaHash comes from structured.SchemaHash, never a hand-rolled
// digest (AC-016) — it returns "" for an empty schema, which is every
// Mode="json" call, matching the payload doc's stated empty-for-json
// convention for free.
func (s *structuredStream) emitAudit(werr error, retryAttempted bool, final llm.Response) {
	if s.audit == nil {
		return
	}
	attempts := 1
	if retryAttempted {
		attempts = 2
	}
	outcome := "passed"
	switch {
	case s.mode == "json":
		outcome = "skipped"
	case werr != nil:
		outcome = "failed"
	case retryAttempted:
		outcome = "retry_passed"
	}
	contextaudit.MustEmit(s.ctx, s.audit, contextaudit.KindLLMStructuredResponse,
		contextaudit.LLMStructuredResponsePayload{
			Provider:          s.provider,
			Model:             s.model,
			FormatMode:        s.mode,
			SchemaHash:        structured.SchemaHash(s.schema),
			ValidationOutcome: outcome,
			Attempts:          attempts,
			InputTokens:       final.Usage.InputTokens,
			OutputTokens:      final.Usage.OutputTokens,
		},
		time.Now(),
	)
}
