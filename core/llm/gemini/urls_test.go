package gemini

import (
	"strings"
	"testing"
)

func TestAIStudioStreamURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		model     string
		wantErr   bool
		wantInURL string
	}{
		{
			name:      "empty model",
			model:     "",
			wantErr:   true,
		},
		{
			name:      "simple model",
			model:     "gemini-2.0-flash",
			wantInURL: "gemini-2.0-flash",
		},
		{
			name:      "versioned model with dots",
			model:     "gemini-2.5-pro-preview-04-09",
			wantInURL: "gemini-2.5-pro-preview-04-09",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := AIStudioStreamURL(tc.model)
			if tc.wantErr {
				if err == nil {
					t.Errorf("want error, got URL %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantInURL != "" && !strings.Contains(got, tc.wantInURL) {
				t.Errorf("URL %q does not contain %q", got, tc.wantInURL)
			}
			if !strings.Contains(got, "streamGenerateContent") {
				t.Errorf("URL %q missing streamGenerateContent", got)
			}
			if !strings.Contains(got, "alt=sse") {
				t.Errorf("URL %q missing alt=sse", got)
			}
		})
	}
}

func TestVertexStreamURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		model   string
		project string
		region  string
		wantErr bool
	}{
		{name: "empty model", model: "", project: "proj", region: "us-central1", wantErr: true},
		{name: "empty project", model: "gemini-2.0-flash", project: "", region: "us-central1", wantErr: true},
		{name: "empty region", model: "gemini-2.0-flash", project: "proj", region: "", wantErr: true},
		{name: "valid", model: "gemini-2.5-pro-preview-04-09", project: "my-project", region: "us-central1", wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := VertexStreamURL(tc.model, tc.project, tc.region)
			if tc.wantErr {
				if err == nil {
					t.Errorf("want error, got URL %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tc.project) {
				t.Errorf("URL %q missing project %q", got, tc.project)
			}
			if !strings.Contains(got, tc.region) {
				t.Errorf("URL %q missing region %q", got, tc.region)
			}
			if !strings.Contains(got, tc.model) {
				t.Errorf("URL %q missing model %q", got, tc.model)
			}
		})
	}
}

func TestAIStudioStreamURL_PercentEscaping(t *testing.T) {
	t.Parallel()
	// Model names with dashes and dots must be URL-safe in the path segment.
	model := "gemini-2.5-pro-preview-04-09"
	u, err := AIStudioStreamURL(model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The model should appear in the URL. Dot in path segments is safe and
	// url.PathEscape preserves it; we just confirm the round-trip works.
	if !strings.Contains(u, "gemini-2.5-pro-preview-04-09") {
		t.Errorf("escaped URL %q should contain the model name", u)
	}
}
