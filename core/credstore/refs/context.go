package refs

import "context"

type ctxKey int

const (
	sanitizerKey ctxKey = iota
	resolverKey
)

// WithTurnSanitizer attaches a per-turn Sanitizer to the context.
// The chat runner calls this at turn start; tools and the streaming
// bridge retrieve it via SanitizerFromContext.
func WithTurnSanitizer(ctx context.Context, s *Sanitizer) context.Context {
	return context.WithValue(ctx, sanitizerKey, s)
}

// SanitizerFromContext retrieves the per-turn Sanitizer from the context.
// Returns nil when no sanitizer has been wired (callers treat nil as a
// no-op sanitizer — do nothing, return input unchanged).
func SanitizerFromContext(ctx context.Context) *Sanitizer {
	v, _ := ctx.Value(sanitizerKey).(*Sanitizer)
	return v
}

// WithResolver attaches a Resolver to the context. Tools retrieve it
// via ResolverFromContext to call Substitute at the execution boundary.
func WithResolver(ctx context.Context, r *Resolver) context.Context {
	return context.WithValue(ctx, resolverKey, r)
}

// ResolverFromContext retrieves the Resolver from the context. Returns
// nil when no resolver has been wired (test paths or non-tool contexts).
func ResolverFromContext(ctx context.Context) *Resolver {
	v, _ := ctx.Value(resolverKey).(*Resolver)
	return v
}
