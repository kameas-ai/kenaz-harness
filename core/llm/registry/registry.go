// Package registry implements the in-memory connector façade (WP02 / WP04 /
// WP05). It composes the gates, the credential resolver, the audit
// emitter, and the retry middleware into the canonical pipeline:
//
//	Profile lookup → CapabilityGate → PolicyGuard → CredentialResolver
//	  → AuditEmitter → RetryMiddleware → ProviderAdapter → CostReducer
//
// The registry is the single seam for OSS and enterprise extensibility
// (FR-018, C-005): every consumer reaches the connector through this
// package's Registry interface or the default New() implementation.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/azure"
	"github.com/kameas-ai/kenaz-harness/core/llm/bedrock"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
	"github.com/kameas-ai/kenaz-harness/core/llm/events"
	"github.com/kameas-ai/kenaz-harness/core/llm/gemini"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
	"github.com/kameas-ai/kenaz-harness/core/llm/retry"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// CostReducer is the optional usage→cost stage (WP11). The registry
// invokes it after the adapter returns a final Response.
type CostReducer interface {
	Derive(usage llm.Usage, kind, model string) llm.Cost
}

// Options bundles the optional collaborators wired into the Registry.
type Options struct {
	Catalog  *capabilities.Catalog
	Resolver *credref.Resolver
	Emitter  *events.Emitter
	Policy   llm.PolicyGuard
	Cost     CostReducer
}

// Registry is the mutable in-memory implementation.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]llm.ProviderAdapter
	profiles map[string]llm.ProviderProfile

	cat      *capabilities.Catalog
	gate     *capabilities.Gate
	resolver *credref.Resolver
	em       *events.Emitter
	policy   llm.PolicyGuard
	reducer  CostReducer

	now func() time.Time
}

// New returns a Registry wired with opts. Nil collaborators are replaced
// with safe defaults: AllowAllGuard for policy, an in-memory event sink
// for audit, and the embedded capability catalog when none is supplied.
func New(opts Options) (*Registry, error) {
	cat := opts.Catalog
	if cat == nil {
		c, err := capabilities.LoadDefault()
		if err != nil {
			return nil, fmt.Errorf("registry: load default capabilities: %w", err)
		}
		cat = c
	}
	emitter := opts.Emitter
	if emitter == nil {
		emitter = events.New(&events.MemorySink{})
	}
	var policy llm.PolicyGuard = opts.Policy
	if policy == nil {
		policy = llm.AllowAllGuard{}
	}
	r := &Registry{
		adapters: map[string]llm.ProviderAdapter{},
		profiles: map[string]llm.ProviderProfile{},
		cat:      cat,
		gate:     capabilities.NewGate(cat),
		resolver: opts.Resolver,
		em:       emitter,
		policy:   policy,
		reducer:  opts.Cost,
		now:      time.Now,
	}
	// Built-in adapters. Operators can re-register against the same
	// kind() string to swap (e.g. an enterprise build with custom
	// transports / proxies); last-write-wins per RegisterAdapter.
	//
	// DIRECTIVE_001: only the per-provider sub-package imports the
	// provider's wire types. The registry ships the default v1 set
	// out of the box so a fresh harness install can talk to a real
	// model without additional wiring (FR-018 / C-005).
	r.adapters[anthropic.Kind] = anthropic.New()
	r.adapters[bedrock.Kind] = bedrock.New()
	r.adapters[openai.Kind] = openai.New()
	r.adapters[openrouter.Kind] = openrouter.New()
	// Azure OpenAI adapter (feature-flagged: HARNESS_AZURE_OPENAI=0 to disable).
	if azure.AzureOpenAIEnabled() {
		r.adapters[azure.Kind] = azure.New()
	}
	// Google Gemini adapter — gated by HARNESS_GOOGLE_GEMINI env flag
	// (default: enabled). The flag allows operators to exclude Gemini
	// from the picker without recompiling.
	if gemini.IsEnabled() {
		r.adapters[gemini.Kind] = gemini.New()
	}
	// custom-openai adapter: opt-out via HARNESS_CUSTOM_OPENAI=0.
	// New() returns nil when the env flag is 0; skip registration in that case.
	// (custom-openai-compatible-endpoint-01KQ8VN0 WP02)
	if ca := custom.New(); ca != nil {
		r.adapters[custom.Kind] = ca
	}
	return r, nil
}

// SetClock overrides the registry's clock; used by tests.
func (r *Registry) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now != nil {
		r.now = now
	}
}

// RegisterAdapter installs a for its declared Kind(). Last write wins.
func (r *Registry) RegisterAdapter(a llm.ProviderAdapter) {
	if a == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.Kind()] = a
}

// UnregisterAdapter removes the adapter for the given kind. No-op when
// the kind is not registered.
func (r *Registry) UnregisterAdapter(kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.adapters, kind)
}

// ConditionalAdapter is an optional interface that adapters can implement
// to signal conditional availability. The registry checks Enabled() at
// resolve time so that tier/capability changes propagate without restart.
type ConditionalAdapter interface {
	llm.ProviderAdapter
	// Enabled reports whether this adapter should currently be served.
	// When false, Adapter(kind) returns nil as if unregistered.
	Enabled() bool
}

// Adapter returns the adapter registered for kind. Returns nil if no
// adapter has been registered, or if the adapter implements ConditionalAdapter
// and Enabled() returns false (capability-gated adapters can gate themselves
// at resolve time so tier changes propagate without restart).
func (r *Registry) Adapter(kind string) llm.ProviderAdapter {
	r.mu.RLock()
	a := r.adapters[kind]
	r.mu.RUnlock()
	if a == nil {
		return nil
	}
	// Check conditional availability. If the adapter implements
	// ConditionalAdapter and reports disabled, return nil so the caller
	// behaves as if the kind were not registered.
	if ca, ok := a.(ConditionalAdapter); ok && !ca.Enabled() {
		return nil
	}
	return a
}

// MergePersonalProfiles installs personal (user-scoped) profiles after
// the bundle-derived profiles. Profile IDs that collide with an existing
// (bundle) entry are SKIPPED — bundle profiles always win, per the
// providers-management mission contract. Validation failures abort the
// merge with the registry left unchanged for the offending batch.
//
// The personal store's persistence is owned by core/llm/personal; this
// method is the registry-side seam that merges its output.
func (r *Registry) MergePersonalProfiles(profs []llm.ProviderProfile) error {
	for _, p := range profs {
		if err := llm.ValidateProfile(p); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range profs {
		if _, exists := r.profiles[p.ID]; exists {
			// Bundle-derived (or earlier) entry wins; skip silently.
			continue
		}
		r.profiles[p.ID] = p
	}
	return nil
}

// LoadProfiles validates and installs profs. On any validation failure
// the registry is left unchanged. Profile-id collisions are surfaced
// as a typed error.
func (r *Registry) LoadProfiles(profs []llm.ProviderProfile) error {
	seen := map[string]struct{}{}
	for _, p := range profs {
		if err := llm.ValidateProfile(p); err != nil {
			return err
		}
		if _, ok := seen[p.ID]; ok {
			return fmt.Errorf("llm: duplicate profile id %q in input", p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Detect collision against already-loaded profiles.
	for _, p := range profs {
		if _, ok := r.profiles[p.ID]; ok {
			return fmt.Errorf("llm: profile id %q already loaded", p.ID)
		}
	}
	for _, p := range profs {
		r.profiles[p.ID] = p
	}
	return nil
}

// Profile returns the loaded profile by id.
func (r *Registry) Profile(id string) (llm.ProviderProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[id]
	if !ok {
		return llm.ProviderProfile{}, fmt.Errorf("llm: profile %q not registered", id)
	}
	return p, nil
}

// Profiles returns a snapshot list of loaded profiles in insertion-
// order is not guaranteed; the order matches map iteration.
func (r *Registry) Profiles() []llm.ProviderProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ProviderProfile, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, p)
	}
	return out
}

// PreflightAll resolves every loaded profile's credential reference and
// emits llm/preflight_resolved or llm/preflight_failed for each.
func (r *Registry) PreflightAll(ctx context.Context) []llm.PreflightResult {
	profs := r.Profiles()
	if r.resolver == nil {
		results := make([]llm.PreflightResult, len(profs))
		for i, p := range profs {
			results[i] = llm.PreflightResult{
				ProfileID: p.ID, Kind: p.Kind, Resolved: false,
				Message: "no credential resolver configured",
			}
			_ = r.em.PreflightFailed(ctx, p, errors.New(results[i].Message))
		}
		return results
	}
	results := r.resolver.Preflight(ctx, profs)
	for i, p := range profs {
		// Defense-in-depth: bedrock + missing region must always be a
		// preflight failure even if the credential resolved (R7 / edge
		// case in spec).
		if p.Kind == "bedrock" && p.Region == "" {
			results[i].Resolved = false
			if results[i].Err == nil {
				results[i].Err = fmt.Errorf("bedrock profile %q: region not configured", p.ID)
				results[i].Message = results[i].Err.Error()
			}
		}
		if results[i].Resolved {
			_ = r.em.PreflightResolved(ctx, p)
		} else {
			_ = r.em.PreflightFailed(ctx, p, results[i].Err)
		}
	}
	return results
}

// Stream dispatches the canonical pipeline. The returned Stream is the
// adapter's stream wrapped with retry-aware error reporting.
//
// Pipeline order (matches plan §4):
//
//  1. Profile lookup
//  2. CapabilityGate.Check  → llm/capability_rejected on failure
//  3. PolicyGuard.Allow     → llm/policy_denied on failure
//  4. CredentialResolver    → llm/error on resolution failure
//  5. AuditEmitter.RequestSubmitted
//  6. RetryMiddleware.Run(adapter.Stream)  → llm/retry_attempted per retry
//  7. (Stream returned to caller; CostReducer attaches Cost on Final)
func (r *Registry) Stream(ctx context.Context, req llm.GenerationRequest) (llm.Stream, error) {
	log := logging.L()
	r.mu.RLock()
	prof, ok := r.profiles[req.ProfileID]
	adapter := r.adapters[prof.Kind]
	emitter := r.em
	policy := r.policy
	gate := r.gate
	reducer := r.reducer
	resolver := r.resolver
	r.mu.RUnlock()

	log.Info("registry.stream.dispatch",
		"profile_id", req.ProfileID,
		"profile_found", ok,
		"profile_kind", prof.Kind,
		"profile_default_model", prof.Model,
		"requested_model", req.Model,
		"adapter_registered", adapter != nil,
	)

	if !ok {
		return nil, fmt.Errorf("llm: profile %q not registered", req.ProfileID)
	}
	if adapter == nil {
		return nil, fmt.Errorf("llm: no adapter registered for kind %q", prof.Kind)
	}

	// Per-call model override: when the chat surface picks a model
	// other than the profile default (and that model is in the
	// authorised set), substitute prof.Model so the adapter,
	// capability gate, and audit trail all see the actual model
	// being called.
	if req.Model != "" && req.Model != prof.Model {
		allowed := prof.AvailableModels()
		ok := false
		for _, m := range allowed {
			if m == req.Model {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("llm: model %q not authorised for profile %q (allowed: %v)", req.Model, req.ProfileID, allowed)
		}
		prof.Model = req.Model
	}

	// 2. CapabilityGate.
	if _, err := gate.Check(req, prof); err != nil {
		var ce *llm.ErrCapabilityUnsupported
		if errors.As(err, &ce) {
			_ = emitter.CapabilityRejected(ctx, req, prof, ce.Capabilities)
		}
		return nil, err
	}
	// 2b. Attachment pre-flight gate (multimodal-io-01KQ8TDF FR-010).
	// Validates byte caps, MIME types, image counts, and audio rejection
	// for every image/document ContentBlock in the request.
	if err := gate.CheckAttachments(req, prof); err != nil {
		return nil, err
	}

	// 3. PolicyGuard.
	if perr := policy.Allow(ctx, req, prof); perr != nil {
		var pd *llm.ErrPolicyDenied
		reason := perr.Error()
		if errors.As(perr, &pd) {
			reason = pd.Reason
		}
		_ = emitter.PolicyDenied(ctx, req, prof, reason)
		return nil, perr
	}

	// 4. CredentialResolver.
	var credBytes []byte
	if resolver != nil {
		s, rerr := resolver.Resolve(ctx, prof.ID, prof.Cred)
		if rerr != nil {
			_ = emitter.Error(ctx, req, prof, rerr)
			return nil, rerr
		}
		// Copy the credential bytes via Secret.Use (the canonical
		// borrowed-view pattern from secrets-keychain) and Destroy
		// the resolver-returned Secret immediately; the adapter gets
		// its own private copy in credBytes.
		if useErr := s.Use(func(value []byte) error {
			credBytes = append([]byte(nil), value...)
			return nil
		}); useErr != nil {
			s.Destroy()
			_ = emitter.Error(ctx, req, prof, useErr)
			return nil, useErr
		}
		s.Destroy()
	}

	// 5. AuditEmitter.RequestSubmitted.
	_ = emitter.RequestSubmitted(ctx, req, prof, snapshotIDFromCtx(ctx))

	// 6. RetryMiddleware → adapter.
	policyP := retry.FromLLM(prof.Retry)
	if req.RetryOverride != nil {
		policyP = retry.FromLLM(req.RetryOverride)
	}
	mw := retry.New(func(attempt int, plannedMS, actualMS int, err error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		_ = emitter.RetryAttempted(ctx, req, prof, attempt, plannedMS, actualMS, msg)
	})
	start := r.now()
	stream, attempts, err := retry.Run(mw, ctx, policyP, func(c context.Context) (llm.Stream, error) {
		return adapter.Stream(c, req, prof, credBytes)
	})
	if err != nil {
		// Zeroize the adapter's credential copy on failure path.
		zero(credBytes)
		// provider-keychain-rotation-01KQ8TD9 WP01: when the adapter returns
		// *ErrAuth, decorate it with the registry-level (provider, profileID,
		// modelID) context so the chat runner can populate the rotation toast
		// without knowing which profile is bound to the session.
		//
		// ErrCredentialResolution is NOT decorated here — that signals a
		// missing/wiped keychain entry, which requires re-onboarding rather
		// than key rotation.
		var authErr *llm.ErrAuth
		if errors.As(err, &authErr) {
			decorated := &llm.ErrProviderAuthFailed{
				Provider:  prof.Kind,
				ProfileID: prof.ID,
				ModelID:   prof.Model,
				Reason:    authErr.Message,
				Cause:     authErr,
			}
			_ = emitter.Error(ctx, req, prof, decorated)
			return nil, decorated
		}
		_ = emitter.Error(ctx, req, prof, err)
		return nil, err
	}

	// 7. Wrap the returned stream so we can attach the cost reducer and
	// audit terminal events on Final()/Cancel().
	wrapped := &auditedStream{
		inner:    stream,
		req:      req,
		prof:     prof,
		emitter:  emitter,
		reducer:  reducer,
		started:  start,
		attempts: attempts,
		credBuf:  credBytes,
	}
	return wrapped, nil
}

func snapshotIDFromCtx(ctx context.Context) string {
	if v := ctx.Value(snapshotKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

type snapshotKey struct{}

// WithSnapshotID returns a context carrying the bundle ResolvedGraph
// snapshot id (FR-020). Used by the bundle-format-resolver to thread
// the snapshot through to llm/request_submitted payloads.
func WithSnapshotID(parent context.Context, id string) context.Context {
	return context.WithValue(parent, snapshotKey{}, id)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
