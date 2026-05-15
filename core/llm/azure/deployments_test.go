package azure

import (
	"strings"
	"testing"
)

func TestDeploymentRegistry_RoundTrip(t *testing.T) {
	yaml := `
version: 1
deployments:
  - host: myresource.openai.azure.com
    model_id: gpt-4o
    deployment: prod-gpt-4o-eastus
    api_version: "2024-10-21"
  - host: myresource.openai.azure.com
    model_id: o1
    deployment: prod-o1-eastus
    api_version: "2025-01-01-preview"
`
	reg, err := parseDeployments([]byte(yaml))
	if err != nil {
		t.Fatalf("parseDeployments: %v", err)
	}

	e, err := reg.Resolve("myresource.openai.azure.com", "gpt-4o")
	if err != nil {
		t.Fatalf("Resolve gpt-4o: %v", err)
	}
	if e.Deployment != "prod-gpt-4o-eastus" {
		t.Errorf("Deployment = %q; want prod-gpt-4o-eastus", e.Deployment)
	}
	if e.APIVersion != "2024-10-21" {
		t.Errorf("APIVersion = %q; want 2024-10-21", e.APIVersion)
	}

	e2, err := reg.Resolve("myresource.openai.azure.com", "o1")
	if err != nil {
		t.Fatalf("Resolve o1: %v", err)
	}
	if e2.Deployment != "prod-o1-eastus" {
		t.Errorf("Deployment = %q; want prod-o1-eastus", e2.Deployment)
	}
}

func TestDeploymentRegistry_RejectsBadVersion(t *testing.T) {
	yaml := `
version: 99
deployments: []
`
	_, err := parseDeployments([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for version=99, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported version") {
		t.Errorf("error %q does not mention 'unsupported version'", err)
	}
}

func TestDeploymentRegistry_MissReturnsTypedError(t *testing.T) {
	yaml := `
version: 1
deployments: []
`
	reg, err := parseDeployments([]byte(yaml))
	if err != nil {
		t.Fatalf("parseDeployments: %v", err)
	}

	_, rerr := reg.Resolve("missing.openai.azure.com", "gpt-4o")
	if rerr == nil {
		t.Fatal("expected ErrAzureDeploymentNotConfigured, got nil")
	}
	notCfg, ok := rerr.(*ErrAzureDeploymentNotConfigured)
	if !ok {
		t.Fatalf("error type = %T; want *ErrAzureDeploymentNotConfigured", rerr)
	}
	if notCfg.Host != "missing.openai.azure.com" {
		t.Errorf("Host = %q; want missing.openai.azure.com", notCfg.Host)
	}
	if notCfg.ModelID != "gpt-4o" {
		t.Errorf("ModelID = %q; want gpt-4o", notCfg.ModelID)
	}
	if !strings.Contains(rerr.Error(), "missing.openai.azure.com") {
		t.Errorf("error message does not contain host: %q", rerr)
	}
	if !strings.Contains(rerr.Error(), "gpt-4o") {
		t.Errorf("error message does not contain model: %q", rerr)
	}
}

func TestLoadDefaultDeployments(t *testing.T) {
	reg, err := LoadDefaultDeployments()
	if err != nil {
		t.Fatalf("LoadDefaultDeployments: %v", err)
	}
	// Default file is empty — just ensure it parses without error.
	entries := reg.Entries()
	if entries == nil {
		t.Error("Entries() should return non-nil slice")
	}
}
