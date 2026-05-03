// Package autotitle provides one-shot session title generation from a
// conversation transcript.
//
// The package is intentionally narrow: it exposes a Generator that accepts
// an LLMCaller interface so callers can inject any provider-backed
// implementation (or a fake for tests) without importing
// core/llm/registry directly.
//
// Typical use:
//
//	g := autotitle.New(myLLMCaller)
//	title, err := g.GenerateTitle(ctx, transcript)
//
// See plan §2.1 and tasks.md WP02 for the full contract.
package autotitle
