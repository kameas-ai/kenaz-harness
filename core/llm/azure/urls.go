package azure

import (
	"fmt"
	"strings"
)

// buildChatURL constructs the Azure OpenAI chat completions URL for the
// given resource host, deployment name, and api-version.
//
// The URL shape is:
//
//	https://<host>/openai/deployments/<deployment>/chat/completions?api-version=<apiVersion>
//
// Constraints (validated before the URL is built):
//   - host must not contain "://" (scheme prefix not permitted).
//   - deployment must be non-empty.
//   - apiVersion must be non-empty.
func buildChatURL(host, deployment, apiVersion string) (string, error) {
	if strings.Contains(host, "://") {
		return "", &ErrAzureInvalidHost{Host: host}
	}
	if deployment == "" {
		return "", fmt.Errorf("azure: deployment name must not be empty")
	}
	if apiVersion == "" {
		return "", fmt.Errorf("azure: api-version must not be empty")
	}
	return fmt.Sprintf(
		"https://%s/openai/deployments/%s/chat/completions?api-version=%s",
		host, deployment, apiVersion,
	), nil
}

// buildModelsURL constructs the Azure OpenAI /openai/models list URL.
// Used by TestKey to enumerate deployments and verify the API key.
func buildModelsURL(host, apiVersion string) (string, error) {
	if strings.Contains(host, "://") {
		return "", &ErrAzureInvalidHost{Host: host}
	}
	if apiVersion == "" {
		return "", fmt.Errorf("azure: api-version must not be empty")
	}
	return fmt.Sprintf(
		"https://%s/openai/models?api-version=%s",
		host, apiVersion,
	), nil
}
