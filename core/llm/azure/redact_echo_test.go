package azure

import (
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestClassifyStatus_ScrubsEchoedSecrets: a non-2xx body echoing the
// credential or the endpoint's query key never reaches the error text
// (which the registry persists via emitter.Error).
func TestClassifyStatus_ScrubsEchoedSecrets(t *testing.T) {
	secrets := llm.RequestSecrets("https://host/v1?api_key=SECRETquery1", []byte("SECRETcred22"))
	for _, body := range []string{
		"Authorization: Bearer SECRETcred22 ?api_key=SECRETquery1",
		`{"error":{"message":"you sent SECRETcred22 and SECRETquery1"}}`,
		"<html><body>api-key: SECRETcred22</body></html>",
	} {
		err := classifyStatus(400, []byte(body), secrets...)
		if strings.Contains(err.Error(), "SECRETcred22") || strings.Contains(err.Error(), "SECRETquery1") {
			t.Errorf("body %q leaks into error: %v", body, err)
		}
	}
}
