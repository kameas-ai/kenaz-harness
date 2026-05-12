package structured

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// WithRetry calls call() and validates the response against schema.
// On validation failure with strict=false (and retries not globally
// suppressed via HARNESS_RESPONSE_FORMAT_NO_RETRY=1), it fires one retry
// by calling retryCall() with an error hint appended.
//
// Returns:
//   - (data, nil) on success (validation passed on first or second attempt).
//   - (nil, *ErrResponseValidationFailed) when all attempts fail schema validation.
//   - (nil, err) for any non-validation error returned by call or retryCall.
//
// When schema is nil or empty the response is only checked for valid JSON.
// When maxRetries is 0 or strict is true, no retry is attempted.
//
// (structured-output-and-grammar-01KX5R8A FR-006)
func WithRetry(
	ctx context.Context,
	schema json.RawMessage,
	call func(ctx context.Context) ([]byte, error),
	retryCall func(ctx context.Context, hint string) ([]byte, error),
	mode string,
	strict bool,
	maxRetries int,
) ([]byte, error) {
	// First attempt.
	data, err := call(ctx)
	if err != nil {
		return nil, err
	}

	verr := Validate(schema, data)
	if verr == nil {
		return data, nil
	}

	// First attempt failed validation.
	if strict || maxRetries <= 0 || retryDisabledByEnv() {
		return nil, newValidationFailed(mode, verr.Error(), data) //nolint:wrapcheck
	}

	// One retry: append corrective hint to prompt.
	hint := "Your previous response was invalid: " + verr.Error() +
		". Please correct it and respond with valid JSON that matches the required schema."
	data2, err2 := retryCall(ctx, hint)
	if err2 != nil {
		return nil, err2
	}
	verr2 := Validate(schema, data2)
	if verr2 == nil {
		return data2, nil
	}
	return nil, newValidationFailed(mode, verr2.Error(), data2)
}

// retryDisabledByEnv reports whether HARNESS_RESPONSE_FORMAT_NO_RETRY=1
// suppresses the automatic retry on validation failure.
func retryDisabledByEnv() bool {
	return strings.TrimSpace(os.Getenv("HARNESS_RESPONSE_FORMAT_NO_RETRY")) == "1"
}

// newValidationFailed constructs a *llm.ErrResponseValidationFailed.
// The raw response is truncated to 500 chars to keep error messages readable.
func newValidationFailed(mode, schemaError string, raw []byte) *llm.ErrResponseValidationFailed {
	rawStr := string(raw)
	if len(rawStr) > 500 {
		rawStr = rawStr[:500]
	}
	return &llm.ErrResponseValidationFailed{
		Mode:        mode,
		SchemaError: schemaError,
		Raw:         rawStr,
	}
}
