package registry

import (
	"context"
	"encoding/json"

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
	// wants to validate.
	var repaired *llm.Response

	data, werr := structured.WithRetry(
		s.ctx,
		s.schema,
		func(_ context.Context) ([]byte, error) {
			return []byte(llm.Message{Content: resp.Content}.Text()), nil
		},
		func(ctx context.Context, hint string) ([]byte, error) {
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
	if werr != nil {
		return llm.Response{}, werr
	}
	_ = data // the validated bytes; the caller consumes the typed Response, not raw bytes.

	if repaired != nil {
		return *repaired, nil
	}
	return resp, nil
}
