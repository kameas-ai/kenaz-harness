package manifest

import _ "embed"

// SchemaJSON is the embedded JSON Schema document that defines the
// kaneaz.yaml manifest contract. Operators can introspect it via
// `harness bundle schema` (CLI wiring lives outside this package). It is
// the source of truth for the manifest contract; the Go validator in
// validate.go is a hand-rolled implementation of the same constraints.
//
//go:embed schema/kaneaz.yaml.schema.json
var SchemaJSON []byte
