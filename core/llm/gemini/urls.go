package gemini

import (
	"fmt"
	"net/url"
)

// EndpointKind selects which Google Gemini endpoint to target.
type EndpointKind string

const (
	// EndpointKindAIStudio targets the public Google AI Studio endpoint
	// (generativelanguage.googleapis.com). Authentication uses an API key.
	EndpointKindAIStudio EndpointKind = "ai_studio"

	// EndpointKindVertex targets the Google Cloud Vertex AI endpoint
	// (aiplatform.googleapis.com). Authentication uses OAuth 2.0
	// (service-account JSON or application-default credentials).
	EndpointKindVertex EndpointKind = "vertex"
)

const (
	aiStudioBaseURL = "https://generativelanguage.googleapis.com/v1beta/models/%s:streamGenerateContent?alt=sse"
	vertexBaseURL   = "https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent"
)

// AIStudioStreamURL returns the streamGenerateContent SSE URL for the
// Google AI Studio endpoint.
//
// Returns an error when model is empty.
func AIStudioStreamURL(model string) (string, error) {
	if model == "" {
		return "", fmt.Errorf("gemini: AI Studio URL requires a non-empty model name")
	}
	// percent-escape the model name so special characters (slashes,
	// dots, dashes in versioned model IDs) are preserved correctly.
	escaped := url.PathEscape(model)
	return fmt.Sprintf(aiStudioBaseURL, escaped), nil
}

// VertexStreamURL returns the streamGenerateContent SSE URL for the
// Vertex AI endpoint.
//
// Returns an error when any of model, project, or region is empty.
func VertexStreamURL(model, project, region string) (string, error) {
	if model == "" {
		return "", fmt.Errorf("gemini: Vertex URL requires a non-empty model name")
	}
	if project == "" {
		return "", fmt.Errorf("gemini: Vertex URL requires a non-empty project")
	}
	if region == "" {
		return "", fmt.Errorf("gemini: Vertex URL requires a non-empty region")
	}
	escapedModel := url.PathEscape(model)
	escapedProject := url.PathEscape(project)
	escapedRegion := url.PathEscape(region)
	return fmt.Sprintf(vertexBaseURL, escapedRegion, escapedProject, escapedRegion, escapedModel), nil
}

// AIStudioModelsURL returns the list-models URL for the AI Studio endpoint.
func AIStudioModelsURL() string {
	return "https://generativelanguage.googleapis.com/v1beta/models"
}

// VertexModelsURL returns the list-models URL for the Vertex AI endpoint.
func VertexModelsURL(project, region string) string {
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models",
		region, project, region)
}
