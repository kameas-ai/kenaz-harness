package wirecheck

import (
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// Snapshotter is the optional interface adapters implement when their
// wire body is not directly accessible via buildRequestBody.
//
// Bedrock is the canonical case: it marshals the AWS SDK input struct
// to JSON rather than calling a local buildRequestBody function. The
// test injects a snapshotter that calls the adapter's internal
// marshaller and returns the bytes.
//
// Adapters that use a plain JSON map (anthropic, openai, openrouter)
// do not need to implement this interface — the wirecheck layer calls
// their exported BuildRequestBody directly.
type Snapshotter interface {
	// Snapshot returns the JSON-encoded wire body for the given request
	// and profile. The returned bytes must be valid JSON (typically the
	// marshalled SDK input struct) so that AssertSerialized can apply
	// JSON Pointer assertions.
	Snapshot(req llm.GenerationRequest, prof llm.ProviderProfile) ([]byte, error)
}
