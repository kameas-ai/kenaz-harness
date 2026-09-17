package serve

// fleet_enroll_supervisor.go — keeps a served harness enrolled with Fleet for
// as long as the host has a session.
//
// It replaces a one-shot `if signedInAtBoot { go enroll() }` that both served
// entry points carried. That never recovered from: an anonymous boot, a failed
// first enroll (boot races the VM's egress rules), a host sign-out/sign-in, or
// a host account change.
//
// core/serve may not import core/fleet, so everything fleet-shaped arrives as
// a callback.

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

// AuthWatcher is the slice of *authbroker.Session the supervisor needs.
type AuthWatcher interface {
	State() authbroker.State
	Subscribe() <-chan struct{}
}

// FleetEnrollConfig wires the supervisor.
type FleetEnrollConfig struct {
	Auth AuthWatcher
	// Identity returns a stable key for the account the current token asserts
	// (subject+org+issuer), or "" when there is no usable token.
	Identity func() string
	// Enroll enrolls and reconciles telemetry. Called on first sign-in, after
	// a failure (with backoff), and whenever Identity changes.
	Enroll func(ctx context.Context) error
	// Reconcile re-evaluates export for the enrolled identity. Cheap.
	Reconcile func(ctx context.Context)
	// SessionEnded runs when the session goes away after an enroll.
	SessionEnded func(ctx context.Context)
	Log          *slog.Logger

	// Test seams; zero values select production behaviour.
	After        func(time.Duration) <-chan time.Time
	RetryBase    time.Duration // default 2s
	RetryMax     time.Duration // default 5m
	ReconcileGap time.Duration // default 1m
}

// FleetEnrollStatus is a payload-free view of the supervisor for diagnosis.
type FleetEnrollStatus struct {
	AuthState      string `json:"auth_state"`
	Enrolled       bool   `json:"enrolled"`
	EnrollAttempts int    `json:"enroll_attempts"`
	EnrollFailures int    `json:"enroll_failures"`
	LastError      string `json:"last_error,omitempty"` // error class only, see classify
	LastEnrollAt   string `json:"last_enroll_at,omitempty"`
}

// FleetEnrollSupervisor is started once per served process.
type FleetEnrollSupervisor struct {
	cfg FleetEnrollConfig

	mu       sync.Mutex
	enrolled string // identity key of the last successful enroll
	status   FleetEnrollStatus
}

// NewFleetEnrollSupervisor validates cfg and applies defaults.
func NewFleetEnrollSupervisor(cfg FleetEnrollConfig) *FleetEnrollSupervisor {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = 2 * time.Second
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = 5 * time.Minute
	}
	if cfg.ReconcileGap <= 0 {
		cfg.ReconcileGap = time.Minute
	}
	return &FleetEnrollSupervisor{cfg: cfg}
}

// Status returns the current snapshot.
func (s *FleetEnrollSupervisor) Status() FleetEnrollStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Enrolled = s.enrolled != ""
	if s.cfg.Auth != nil {
		st.AuthState = s.cfg.Auth.State().String()
	}
	return st
}

// Run blocks until ctx is cancelled.
func (s *FleetEnrollSupervisor) Run(ctx context.Context) {
	if s.cfg.Auth == nil || s.cfg.Enroll == nil || s.cfg.Identity == nil {
		return
	}
	notify := s.cfg.Auth.Subscribe()
	backoff := s.cfg.RetryBase
	for {
		wait := s.cfg.ReconcileGap
		if retry := s.step(ctx); retry {
			wait = backoff
			backoff *= 2
			if backoff > s.cfg.RetryMax {
				backoff = s.cfg.RetryMax
			}
		} else {
			backoff = s.cfg.RetryBase
		}
		select {
		case <-ctx.Done():
			return
		case <-notify:
		case <-s.cfg.After(wait):
		}
	}
}

// step brings enrollment in line with the auth state once. It reports whether
// the caller should retry on the failure backoff.
func (s *FleetEnrollSupervisor) step(ctx context.Context) (retry bool) {
	s.mu.Lock()
	enrolled := s.enrolled
	s.mu.Unlock()

	identity := ""
	if s.cfg.Auth.State() == authbroker.StateSignedIn {
		identity = s.cfg.Identity()
	}
	if identity == "" {
		if enrolled != "" {
			s.cfg.Log.Info("harness-served: fleet session ended")
			if s.cfg.SessionEnded != nil {
				s.cfg.SessionEnded(ctx)
			}
			s.mu.Lock()
			s.enrolled = ""
			s.mu.Unlock()
		}
		return false
	}
	if identity == enrolled {
		if s.cfg.Reconcile != nil {
			s.cfg.Reconcile(ctx)
		}
		return false
	}

	// First sign-in, a retry, or the account changed under us.
	err := s.cfg.Enroll(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.EnrollAttempts++
	if err != nil {
		s.status.EnrollFailures++
		s.status.LastError = classifyEnrollError(err)
		s.cfg.Log.Info("harness-served: fleet enroll failed; will retry", "class", s.status.LastError)
		return true
	}
	s.enrolled = identity
	s.status.LastError = ""
	s.status.LastEnrollAt = time.Now().UTC().Format(time.RFC3339)
	s.cfg.Log.Info("harness-served: fleet enrolled")
	return false
}

// classifyEnrollError reduces an error to a class. The raw text can carry a
// URL or a response preview; status surfaces must not.
func classifyEnrollError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, c := range []struct{ needle, class string }{
		{"401", "unauthorized"}, {"403", "forbidden"}, {"not signed in", "unauthorized"},
		{"disabled", "fleet_disabled"}, {"not configured", "profile_not_configured"},
		{"timeout", "timeout"}, {"deadline", "timeout"},
		{"connection", "network"}, {"no such host", "network"}, {"dial", "network"},
		{"50", "server_error"},
	} {
		if containsFold(msg, c.needle) {
			return c.class
		}
	}
	return "other"
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
