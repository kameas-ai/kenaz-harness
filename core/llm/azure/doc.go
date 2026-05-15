// Package azure is the Azure OpenAI Service streaming adapter.
//
// The adapter speaks the Azure OpenAI Service REST API, which is
// structurally identical to the OpenAI Chat Completions API but routes
// through a tenant-specific resource host, uses deployment IDs in place
// of model IDs in the URL, and authenticates with an "api-key" header
// rather than an Authorization Bearer token.
//
// Wire compatibility:
//
//   - Request body shape: identical to OpenAI (same JSON schema).
//   - SSE response shape: identical to OpenAI.
//   - URL: https://<resource-host>/openai/deployments/<deployment>/chat/completions?api-version=<version>
//   - Auth: api-key: <key> header.
//
// The deployment registry (DeploymentRegistry) maps logical
// (resourceHost, modelID) pairs to the concrete Azure deployment name
// and api-version configured by the operator. Adapters should never
// hard-code deployment names.
//
// Capability surface: Azure OpenAI mirrors OpenAI's capability catalog.
// The capabilities.Catalog is queried with provider="openai" so Azure
// deployments automatically inherit OpenAI YAML descriptors.
package azure
