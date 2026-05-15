package azure

import (
	"net/http"
	"strings"
)

// setAPIKeyHeader writes the Azure OpenAI api-key header to req. The
// credential bytes are converted to string exactly once, inline. The cred
// slice is not retained beyond the header set.
//
// Azure uses the "api-key" header rather than an Authorization Bearer
// token — this is the only auth difference from the OpenAI adapter.
func setAPIKeyHeader(req *http.Request, cred []byte) {
	req.Header.Set("api-key", string(cred))
}

// TestKeyResult is the structured outcome of TestKey. ModelCount is the
// number of models visible to the API key. DeprecationWarning is non-empty
// when the provider signals pending API deprecation via response headers.
type TestKeyResult struct {
	OK                 bool
	ModelCount         int
	DeprecationWarning string
}

// extractDeprecationWarning reads Azure-specific deprecation headers from
// the HTTP response and combines their values. Azure may send either
// "api-deprecation" or "azureml-model-deprecation"; when both are present
// they are joined with "; ".
func extractDeprecationWarning(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	var parts []string
	if v := resp.Header.Get("api-deprecation"); v != "" {
		parts = append(parts, v)
	}
	if v := resp.Header.Get("azureml-model-deprecation"); v != "" {
		parts = append(parts, v)
	}
	return strings.Join(parts, "; ")
}
