package fleet

// go:generate emits frontend/src/lib/capability-keys.ts from the
// AllCapabilities() slice in capability.go. Run after any change to the
// Capability constants, then commit the generated file alongside the
// Go change.
//
// The CI gate scripts/ci/check-codegen.sh re-runs this and fails on
// drift between the committed TS file and the freshly-generated one.
//
// Mission: fleet-capability-surface-01NDFSEX09, WP07.

//go:generate go run ./cmd/gen-capability-keys -out frontend/src/lib/capability-keys.ts
