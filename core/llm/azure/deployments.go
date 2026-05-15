package azure

import (
	"embed"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

//go:embed deployments.yaml
var defaultDeploymentsFS embed.FS

// DeploymentEntry is one row in the operator-supplied deployment registry.
type DeploymentEntry struct {
	// Host is the Azure OpenAI resource hostname without scheme.
	// Example: myresource.openai.azure.com
	Host string `yaml:"host"`
	// ModelID is the canonical OpenAI model identifier (e.g. "gpt-4o", "o1").
	ModelID string `yaml:"model_id"`
	// Deployment is the Azure deployment name created in Azure AI Studio.
	Deployment string `yaml:"deployment"`
	// APIVersion is the Azure OpenAI api-version string.
	// Example: "2024-10-21", "2025-01-01-preview"
	APIVersion string `yaml:"api_version"`
}

// deploymentsFile is the on-disk schema for the YAML registry file.
type deploymentsFile struct {
	Version     int               `yaml:"version"`
	Deployments []DeploymentEntry `yaml:"deployments"`
}

// DeploymentRegistry is an immutable in-memory index of (host, modelID) →
// DeploymentEntry built at load time. It is safe for concurrent reads.
type DeploymentRegistry struct {
	index map[deploymentKey]DeploymentEntry
}

type deploymentKey struct {
	Host    string
	ModelID string
}

// LoadDefaultDeployments loads the embedded deployments.yaml file shipped
// with the azure package. When no operator customisations are needed this
// returns an empty registry (no deployments). The operator must supply a
// custom path via LoadDeployments to configure real deployments.
func LoadDefaultDeployments() (*DeploymentRegistry, error) {
	data, err := fs.ReadFile(defaultDeploymentsFS, "deployments.yaml")
	if err != nil {
		return nil, fmt.Errorf("azure: read embedded deployments.yaml: %w", err)
	}
	return parseDeployments(data)
}

// LoadDeployments reads and parses a deployments YAML file from path.
// Returns ErrAzureDeploymentNotConfigured on a missing deployment; returns
// a parse error when the file is malformed or version != 1.
func LoadDeployments(path string) (*DeploymentRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("azure: read deployments file %q: %w", path, err)
	}
	return parseDeployments(data)
}

// parseDeployments is the shared parser used by both Load* functions.
func parseDeployments(data []byte) (*DeploymentRegistry, error) {
	var f deploymentsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("azure: parse deployments YAML: %w", err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("azure: deployments.yaml: unsupported version %d (expected 1)", f.Version)
	}
	r := &DeploymentRegistry{
		index: make(map[deploymentKey]DeploymentEntry, len(f.Deployments)),
	}
	for _, e := range f.Deployments {
		key := deploymentKey{Host: e.Host, ModelID: e.ModelID}
		r.index[key] = e
	}
	return r, nil
}

// Resolve returns the DeploymentEntry for (host, modelID). Returns
// ErrAzureDeploymentNotConfigured when no entry matches.
func (r *DeploymentRegistry) Resolve(host, modelID string) (DeploymentEntry, error) {
	key := deploymentKey{Host: host, ModelID: modelID}
	e, ok := r.index[key]
	if !ok {
		return DeploymentEntry{}, &ErrAzureDeploymentNotConfigured{Host: host, ModelID: modelID}
	}
	return e, nil
}

// Entries returns a snapshot of all registered deployment entries.
// Used by TestKey to enumerate models available on the resource.
func (r *DeploymentRegistry) Entries() []DeploymentEntry {
	out := make([]DeploymentEntry, 0, len(r.index))
	for _, e := range r.index {
		out = append(out, e)
	}
	return out
}
