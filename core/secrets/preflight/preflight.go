// Package preflight implements the startup-time validator that
// resolves every registered CredentialReference exactly once and
// destroys the result. Required references that fail produce a
// fail-closed startup error; optional references degrade silently.
//
// Pre-flight is the moment FR-009 surfaces "your config is wrong"
// instead of "your model call mysteriously failed at midnight".
//
// Spec mapping: FR-009, NFR-006, C-006, D8.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/secrets/events"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
)

// Status is the discrete per-reference pre-flight outcome.
type Status string

const (
	StatusResolvable          Status = "resolvable"
	StatusUnresolvable        Status = "unresolvable"
	StatusOptionalUnresolvable Status = "optional_unresolvable"
	StatusBackendDegraded     Status = "backend_degraded"
)

// Result is the per-reference structured outcome.
type Result struct {
	// ReferenceID is the redaction-safe id (never the locator).
	ReferenceID string
	// Status is the discrete outcome.
	Status Status
	// ErrorCode is a short canonical token (e.g. "not_found",
	// "permission_denied"). Empty on success.
	ErrorCode string
	// Cause is the underlying error, if any. Safe to wrap because
	// the reference Locator is never embedded.
	Cause error
	// TestedAt is the wall-clock time of the probe.
	TestedAt time.Time
	// BackendKind is the resolving backend's name.
	BackendKind registry.BackendKind
	// Optional reflects whether the reference was marked optional.
	Optional bool
}

// ErrPreflightFailed is returned by Run when at least one required
// reference is unresolvable. Inspect the per-reference Results for
// detail.
var ErrPreflightFailed = errors.New("secrets/preflight: required references unresolvable")

// Validator runs pre-flight against a Registry.
type Validator struct {
	reg *registry.Registry
	log events.EventLogger
}

// New constructs a Validator. log may be nil; events are dropped in
// that case (the cycle-break path) — caller is responsible for
// wiring a real logger or buffering one before any production use.
func New(reg *registry.Registry, log events.EventLogger) *Validator {
	return &Validator{reg: reg, log: log}
}

// Run iterates refs, resolves each exactly once, and immediately
// destroys the Secret. Required-unresolvable refs cause Run to return
// (results, ErrPreflightFailed). Optional refs are recorded as
// optional_unresolvable but never fail Run.
func (v *Validator) Run(ctx context.Context, refs []ref.CredentialReference) ([]Result, error) {
	out := make([]Result, 0, len(refs))
	var hadRequiredFailure bool

	for _, r := range refs {
		out = append(out, v.runOne(ctx, r))
		last := out[len(out)-1]
		if last.Status == StatusUnresolvable && !last.Optional {
			hadRequiredFailure = true
		}
	}
	if hadRequiredFailure {
		return out, fmt.Errorf("%w", ErrPreflightFailed)
	}
	return out, nil
}

func (v *Validator) runOne(ctx context.Context, r ref.CredentialReference) Result {
	t0 := time.Now()
	res := Result{
		ReferenceID: r.ID(),
		TestedAt:    t0,
		Optional:    r.Optional,
	}

	b, err := v.reg.Lookup(r.Kind)
	if err != nil {
		res.Status = unresolvableOrOptional(r.Optional)
		res.ErrorCode = "no_backend"
		res.Cause = err
		v.emitFailed(ctx, r, "", t0)
		return res
	}

	res.BackendKind = b.Kind()
	s, err := b.Resolve(ctx, r)
	if err != nil {
		res.Status = unresolvableOrOptional(r.Optional)
		res.ErrorCode = errorCode(err)
		res.Cause = err
		v.emitFailed(ctx, r, b.Kind(), t0)
		return res
	}
	// Immediately destroy: pre-flight is a probe, not a resolve.
	if s != nil {
		s.Destroy()
	}

	// Health probe.
	h := b.Health(ctx)
	if h.Status == registry.HealthDegraded {
		res.Status = StatusBackendDegraded
	} else if h.Status == registry.HealthUnavailable {
		res.Status = unresolvableOrOptional(r.Optional)
		res.ErrorCode = "backend_unavailable"
	} else {
		res.Status = StatusResolvable
	}

	v.emitOK(ctx, r, b.Kind(), t0)
	return res
}

func unresolvableOrOptional(optional bool) Status {
	if optional {
		return StatusOptionalUnresolvable
	}
	return StatusUnresolvable
}

// errorCode maps a backend error to a canonical short token. WP07
// will replace this with errors.Is checks against the canonical
// taxonomy; this stub is a forward-compatible placeholder.
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case contains(msg, "not found"):
		return "not_found"
	case contains(msg, "empty"):
		return "reference_empty"
	case contains(msg, "permission"):
		return "permission_denied"
	case contains(msg, "unavailable"):
		return "backend_unavailable"
	default:
		return "failed"
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && stringIndex(s, sub) >= 0
}

// stringIndex is a minimal substring search (avoiding strings import
// to keep this file dependency-light).
func stringIndex(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func (v *Validator) emitOK(ctx context.Context, r ref.CredentialReference, b registry.BackendKind, t0 time.Time) {
	if v.log == nil {
		return
	}
	e := events.NewResolutionEvent(r, r.ConsumerID)
	e.BackendKind = b
	e.Outcome = "ok"
	e.LatencyMS = time.Since(t0).Milliseconds()
	_ = v.log.Append(ctx, events.KindPreFlightOK, e)
}

func (v *Validator) emitFailed(ctx context.Context, r ref.CredentialReference, b registry.BackendKind, t0 time.Time) {
	if v.log == nil {
		return
	}
	e := events.NewResolutionEvent(r, r.ConsumerID)
	e.BackendKind = b
	e.Outcome = "failed"
	e.LatencyMS = time.Since(t0).Milliseconds()
	_ = v.log.Append(ctx, events.KindPreFlightFailed, e)
}
