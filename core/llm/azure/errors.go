package azure

import "fmt"

// ErrAzureDeploymentNotConfigured is returned when no deployment entry
// can be found for the given (host, modelID) pair in the deployment
// registry. Operators must add the deployment to deployments.yaml before
// the adapter will open any HTTP connection for that model.
type ErrAzureDeploymentNotConfigured struct {
	Host    string
	ModelID string
}

func (e *ErrAzureDeploymentNotConfigured) Error() string {
	return fmt.Sprintf(
		"azure: no deployment configured for host %q model %q — "+
			"add an entry to deployments.yaml",
		e.Host, e.ModelID,
	)
}

// ErrAzureInvalidHost is returned when a host string contains "://"
// (i.e. the caller supplied a full URL rather than just the host name).
type ErrAzureInvalidHost struct {
	Host string
}

func (e *ErrAzureInvalidHost) Error() string {
	return fmt.Sprintf(
		"azure: host must not contain a scheme (got %q) — use the bare hostname, e.g. myresource.openai.azure.com",
		e.Host,
	)
}
