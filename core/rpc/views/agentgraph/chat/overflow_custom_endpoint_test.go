package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
)

// TestOverflowDetection_CustomEndpointErrorShapes: the custom-openai
// adapter must keep the provider's message for every error shape the
// OpenAI-compatible servers it fronts actually return — overflow
// recovery and the request-too-large verdict read that text. An earlier
// scrub kept only the nested {"error":{"message"}} shape and turned the
// rest into "Bad Request", silently disabling both on TGI / vLLM /
// FastAPI / plain-text servers. The credential must still be scrubbed.
func TestOverflowDetection_CustomEndpointErrorShapes(t *testing.T) {
	const cred = "SECRETcred4overflow"
	const overflow = "This model's maximum context length is 4096 tokens (context_length_exceeded)"
	bodies := map[string]string{
		"openai":    `{"error":{"message":"` + overflow + `","type":"invalid_request_error"}}`,
		"tgi":       `{"error":"Input validation error: ` + overflow + ` key=` + cred + `","error_type":"validation"}`,
		"vllm":      `{"object":"error","message":"` + overflow + `"}`,
		"fastapi":   `{"detail":"` + overflow + `"}`,
		"fastapi[]": `{"detail":[{"loc":["body"],"msg":"` + overflow + `"}]}`,
		"plain":     overflow + "\nAuthorization: Bearer " + cred,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			prof := corellm.ProviderProfile{ID: "p", Kind: custom.Kind, Model: "m", Endpoint: srv.URL + "/v1"}
			_, err := custom.New().Stream(context.Background(), corellm.GenerationRequest{}, prof, []byte(cred))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !isContextOverflowError(err) {
				t.Errorf("isContextOverflowError = false for %v", err)
			}
			if !isContextWindowRejection(err) {
				t.Errorf("isContextWindowRejection = false for %v", err)
			}
			if strings.Contains(err.Error(), cred) {
				t.Errorf("error leaks the credential: %v", err)
			}
		})
	}
}
