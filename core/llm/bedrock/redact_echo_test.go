package bedrock

import (
	"strings"
	"testing"
)

// TestClassifyBedrockHTTPStatus_ScrubsEchoedKey: the error body used to
// become the error message verbatim (up to 8 MiB on the non-streaming
// path); it is now bounded and scrubbed of the bearer key.
func TestClassifyBedrockHTTPStatus_ScrubsEchoedKey(t *testing.T) {
	for _, body := range []string{
		"Authorization: Bearer SECRETbedrock1",
		`{"message":"bad key SECRETbedrock1"}`,
		strings.Repeat("x", 10000) + "SECRETbedrock1",
	} {
		err := classifyBedrockHTTPStatus(400, []byte(body), "SECRETbedrock1")
		if strings.Contains(err.Error(), "SECRETbedrock1") {
			t.Errorf("bedrock error leaks the key: %.200v", err)
		}
		if len(err.Error()) > 1024 {
			t.Errorf("bedrock error not bounded: %d bytes", len(err.Error()))
		}
	}
}
