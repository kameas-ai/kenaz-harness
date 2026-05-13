package agents

import "embed"

// BundledFS is the embedded filesystem holding the five shipped profile
// YAML files. The embed directive captures all *.yaml files directly inside
// the bundled/ subdirectory.
//
//go:embed bundled/*.yaml
var BundledFS embed.FS
