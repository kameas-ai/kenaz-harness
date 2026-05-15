package azure

import (
	"strings"
	"testing"
)

func TestBuildChatURL_GoldenShape(t *testing.T) {
	u, err := buildChatURL("foo.openai.azure.com", "prod-gpt-4o", "2024-10-21")
	if err != nil {
		t.Fatalf("buildChatURL: %v", err)
	}
	want := "https://foo.openai.azure.com/openai/deployments/prod-gpt-4o/chat/completions?api-version=2024-10-21"
	if u != want {
		t.Errorf("URL = %q; want %q", u, want)
	}
}

func TestBuildChatURL_RejectsSchemeInHost(t *testing.T) {
	cases := []string{
		"https://foo.openai.azure.com",
		"http://foo.openai.azure.com",
	}
	for _, host := range cases {
		_, err := buildChatURL(host, "dep", "2024-10-21")
		if err == nil {
			t.Errorf("host %q: expected error, got nil", host)
			continue
		}
		if _, ok := err.(*ErrAzureInvalidHost); !ok {
			t.Errorf("host %q: error type = %T; want *ErrAzureInvalidHost", host, err)
		}
	}
}

func TestBuildChatURL_RejectsEmptyDeployment(t *testing.T) {
	_, err := buildChatURL("foo.openai.azure.com", "", "2024-10-21")
	if err == nil {
		t.Fatal("expected error for empty deployment, got nil")
	}
	if !strings.Contains(err.Error(), "deployment") {
		t.Errorf("error %q does not mention 'deployment'", err)
	}
}

func TestBuildChatURL_RejectsEmptyAPIVersion(t *testing.T) {
	_, err := buildChatURL("foo.openai.azure.com", "prod-gpt-4o", "")
	if err == nil {
		t.Fatal("expected error for empty api-version, got nil")
	}
	if !strings.Contains(err.Error(), "api-version") {
		t.Errorf("error %q does not mention 'api-version'", err)
	}
}
