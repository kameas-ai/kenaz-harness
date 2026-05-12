package cedar

import "embed"

// PoliciesFS is the embedded directory of shipped Cedar policy templates.
// Templates are available under the "policies/" prefix within the FS.
// Use fs.ReadFile(PoliciesFS, "policies/<name>") to access a specific template.
//
// The directory embed includes every .cedar file so that new templates added
// to core/policy/cedar/policies/ are automatically available to the
// InstallTemplate RPC without additional code changes.
//
//go:embed policies/*.cedar
var PoliciesFS embed.FS
