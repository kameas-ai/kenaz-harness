package refs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/event/secretref"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Sentinel errors returned by Resolve and Substitute.
var (
	// ErrResolveDenied is returned when the Cedar policy gate denies
	// the resolution. The caller should surface "secret_resolve_denied"
	// to the model with the decision id.
	ErrResolveDenied = errors.New("refs: secret resolution denied by policy")

	// ErrUnknownLocator is returned when the locator does not appear in
	// the session's exposed_secrets index.
	ErrUnknownLocator = errors.New("refs: unknown secret locator")

	// ErrResolutionUnavailable is returned when the underlying keychain
	// or secret backend returns an error.
	ErrResolutionUnavailable = errors.New("refs: secret unavailable")

	// ErrBudgetExhausted is returned when the per-session budget for the
	// locator is exceeded.
	ErrBudgetExhausted = errors.New("refs: resolution budget exhausted")
)

// SecretLookup is the interface the Resolver uses to fetch plaintext
// secret bytes for a locator. The ExposureIndex (WP06) implements this.
// Implementations MUST zero the bytes when the callback returns
// (same contract as credstore.Use).
type SecretLookup interface {
	// Use calls op with the plaintext bytes for the given locator.
	// Returns ErrUnknownLocator when the locator is not in the
	// exposed_secrets set. The bytes are zeroed after op returns.
	Use(ctx context.Context, locator string, op func([]byte) error) error
}

// AuditEmitter is a minimal interface for emitting audit events.
type AuditEmitter interface {
	EmitSecretResolved(ctx context.Context, evt secretref.ResolvedEvent)
}

// noopAuditEmitter is a no-op implementation used when no emitter is wired.
type noopAuditEmitter struct{}

func (noopAuditEmitter) EmitSecretResolved(_ context.Context, _ secretref.ResolvedEvent) {}

// ResolverOptions configures a Resolver at construction time.
type ResolverOptions struct {
	// Lookup provides access to the exposed_secrets index. Required.
	Lookup SecretLookup
	// Gate is the Cedar policy gate. nil = default-allow (no gating).
	Gate cedar.Gate
	// Budget is the per-session resolution budget tracker. nil = unlimited.
	Budget *Budget
	// Emitter receives audit events. nil = no-op.
	Emitter AuditEmitter
	// SessionID identifies the owning session for audit events.
	SessionID string
	// Agent is the agent name for audit events (e.g. "chat").
	Agent string
}

// Resolver performs model-side @secret: reference substitution in tool
// arguments. One instance is created per session and lives for the
// session's lifetime.
//
// Thread-safety: Resolve / Substitute are safe for concurrent use after
// construction. The per-turn Sanitizer is managed externally via the
// context (see WithTurnSanitizer).
type Resolver struct {
	lookup    SecretLookup
	gate      cedar.Gate
	budget    *Budget
	emitter   AuditEmitter
	sessionID string
	agent     string
}

// NewResolver constructs a Resolver. All fields in opts are optional
// except Lookup.
func NewResolver(opts ResolverOptions) *Resolver {
	emitter := AuditEmitter(noopAuditEmitter{})
	if opts.Emitter != nil {
		emitter = opts.Emitter
	}
	return &Resolver{
		lookup:    opts.Lookup,
		gate:      opts.Gate,
		budget:    opts.Budget,
		emitter:   emitter,
		sessionID: opts.SessionID,
		agent:     opts.Agent,
	}
}

// Resolve resolves one secret reference and returns the plaintext bytes.
// The caller MUST zero the returned buffer with ZeroBuffer immediately
// after using it (the defer pattern is recommended).
//
// The Sanitizer in ctx (if any) is populated with the fingerprint of the
// resolved plaintext so the streaming layer can redact it.
//
// One KindSecretReferenceResolved audit event is emitted regardless of
// outcome. Plaintext never enters the event.
func (r *Resolver) Resolve(ctx context.Context, ref Reference, rctx cedar.ResolveContext) ([]byte, cedar.Decision, error) {
	// ── 1. Cedar gate ────────────────────────────────────────────────────
	remaining := int64(0)
	if r.budget != nil {
		remaining = r.budget.Remaining(ref.Locator)
	} else {
		remaining = DefaultBudget
	}
	rctx2 := rctx
	rctx2.AgentKind = rctx.AgentKind
	if rctx2.AgentKind == "" {
		rctx2.AgentKind = "trusted"
	}
	rctx2.Budget = remaining

	decision := cedar.EvaluateSecretReferenceResolve(ctx, r.gate, ref.Locator, rctx2)
	if decision.Outcome == cedar.Deny {
		reason := secretref.OutcomeDenied
		if remaining == 0 {
			reason = secretref.OutcomeBudgetExhausted
		}
		evt := secretref.NewResolvedEvent(
			r.sessionID, r.agent, rctx.ToolName, ref.Locator,
			rctx.DestinationHost, decision.MatchedPolicy,
			reason, decision.Reason, "",
		)
		r.emitter.EmitSecretResolved(ctx, evt)
		if remaining == 0 {
			return nil, decision, fmt.Errorf("%w: locator=%s", ErrBudgetExhausted, ref.Locator)
		}
		return nil, decision, fmt.Errorf("%w: locator=%s decision=%s", ErrResolveDenied, ref.Locator, decision.MatchedPolicy)
	}

	// ── 2. Consume budget ────────────────────────────────────────────────
	if r.budget != nil && !r.budget.Consume(ref.Locator) {
		evt := secretref.NewResolvedEvent(
			r.sessionID, r.agent, rctx.ToolName, ref.Locator,
			rctx.DestinationHost, "",
			secretref.OutcomeBudgetExhausted, "budget exhausted after gate", "",
		)
		r.emitter.EmitSecretResolved(ctx, evt)
		return nil, decision, fmt.Errorf("%w: locator=%s", ErrBudgetExhausted, ref.Locator)
	}

	// ── 3. Fetch plaintext via SecretLookup ──────────────────────────────
	var resolvedBuf []byte
	lookupErr := r.lookup.Use(ctx, ref.Locator, func(plaintext []byte) error {
		// Populate the per-turn sanitizer fingerprint BEFORE copying.
		if s := SanitizerFromContext(ctx); s != nil {
			s.Add(plaintext, ref.Locator)
		}
		// Copy into our own buffer; the lookup will zero its copy.
		resolvedBuf = make([]byte, len(plaintext))
		copy(resolvedBuf, plaintext)
		return nil
	})
	if lookupErr != nil {
		outcome := secretref.OutcomeUnknownLocator
		if !errors.Is(lookupErr, ErrUnknownLocator) {
			outcome = secretref.OutcomeUnavailable
		}
		evt := secretref.NewResolvedEvent(
			r.sessionID, r.agent, rctx.ToolName, ref.Locator,
			rctx.DestinationHost, "",
			outcome, lookupErr.Error(), "",
		)
		r.emitter.EmitSecretResolved(ctx, evt)
		return nil, decision, lookupErr
	}

	// ── 4. Emit success audit event ──────────────────────────────────────
	evt := secretref.NewResolvedEvent(
		r.sessionID, r.agent, rctx.ToolName, ref.Locator,
		rctx.DestinationHost, decision.MatchedPolicy,
		secretref.OutcomePermit, "resolved", "",
	)
	r.emitter.EmitSecretResolved(ctx, evt)

	return resolvedBuf, decision, nil
}

// ZeroBuffer zeroes a byte buffer and prevents the garbage collector
// from eliding the zeroing as a dead store. Callers MUST call this on
// every resolved-bytes buffer immediately after the side-effect that
// consumed it returns.
func ZeroBuffer(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}

// Substitute scans s for @secret: reference tokens, resolves each via
// Resolve, and returns the rewritten string. If s contains no reference
// tokens the input is returned unchanged (zero allocation fast path).
//
// The caller is responsible for calling ZeroBuffer on the returned
// bytes if the result differs from the input; however, Substitute
// itself handles the intermediate plaintext buffers via ZeroBuffer so
// the window is tight.
//
// rctx is forwarded to each Resolve call. All resolutions in a single
// Substitute call share the same rctx (same tool, same destination).
func (r *Resolver) Substitute(ctx context.Context, s string, rctx cedar.ResolveContext) (string, []cedar.Decision, error) {
	matches := FindAll(s)
	if len(matches) == 0 {
		return s, nil, nil // fast path: no references
	}

	decisions := make([]cedar.Decision, 0, len(matches))
	var buf bytes.Buffer
	buf.Grow(len(s))

	pos := 0
	for _, m := range matches {
		// Write text before this match.
		buf.WriteString(s[pos:m.Start])

		resolved, dec, err := r.Resolve(ctx, m.Reference, rctx)
		decisions = append(decisions, dec)
		if err != nil {
			// On resolution failure, leave the token unexpanded so the
			// caller can detect the error. Return the error immediately.
			ZeroBuffer(resolved)
			return "", decisions, fmt.Errorf("refs: substitute %s: %w", m.Reference.Token(), err)
		}

		buf.Write(resolved)
		ZeroBuffer(resolved)
		pos = m.End
	}
	// Write any remaining text after the last match.
	buf.WriteString(s[pos:])

	return buf.String(), decisions, nil
}

// SubstituteBytes is like Substitute but operates on a []byte argument
// string. The returned []byte is a new allocation; the caller is
// responsible for zeroing it via ZeroBuffer after use.
func (r *Resolver) SubstituteBytes(ctx context.Context, src []byte, rctx cedar.ResolveContext) ([]byte, []cedar.Decision, error) {
	result, decisions, err := r.Substitute(ctx, string(src), rctx)
	if err != nil {
		return nil, decisions, err
	}
	return []byte(result), decisions, nil
}

// HasReference reports whether s contains at least one @secret: token.
// Useful for fast-path skipping in tool implementations.
func HasReference(s string) bool {
	return strings.Contains(s, "@secret:")
}
