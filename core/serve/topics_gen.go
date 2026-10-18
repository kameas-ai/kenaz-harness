package serve

// go:generate emits frontend/src/lib/servedStreamTopics.gen.ts from the
// passthroughTopics slice in wsstream.go. Run after any change to
// passthroughTopics, then commit the generated file alongside the Go
// change.
//
// The CI gate scripts/ci/check-codegen.sh re-runs this and fails on
// drift between the committed TS file and the freshly-generated one.
//
// Findings #63 / #62 (served-topic-single-source): before this
// generator, the forwarding allowlist was maintained by hand in three
// places (passthroughTopics here, SERVED_STREAM_TOPICS in
// harnessClient.ts, and a hand-copied mirror in
// harnessClient.wp06Overlay.test.ts) plus a fourth, related
// processWideTopics list that exempts session-less payloads from the
// D-705 fail-closed filter. processWideTopics is intentionally NOT
// generated — it is a genuinely different set (which forwarded topics
// skip session-scoping, not which topics are forwarded at all) and stays
// hand-authored, same as before.

//go:generate go run ./cmd/gen-served-topics -out frontend/src/lib/servedStreamTopics.gen.ts
