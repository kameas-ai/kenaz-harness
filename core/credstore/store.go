package credstore

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// defaultTTL is the handle lifetime when no WithTTL option is given.
const defaultTTL = 60 * time.Second

// reapInterval is how often the expiration goroutine wakes to sweep
// expired entries. Per spec: entries are deleted within 30s of
// expiresAt, so 30s interval is the upper bound.
const reapInterval = 30 * time.Second

// AccessPurpose is a caller-supplied label describing why the
// credential is being accessed. It appears in audit events.
type AccessPurpose string

// entry is the internal record created by Issue. It is private to
// this package.
type entry struct {
	ref       secrets.CredentialReference
	purpose   AccessPurpose
	expiresAt time.Time
	used      atomic.Bool
}

// store implements Store. All exported methods are safe for concurrent
// use.
type store struct {
	mu        sync.RWMutex
	entries   map[Handle]*entry
	resolver  secrets.ResolverAPI
	clock     func() time.Time
	done      chan struct{}
	closeOnce sync.Once

	// auditEmitter is the optional audit.Emitter injected via
	// WithAuditEmitter. nil = no-op (tests / pre-boot path).
	auditEmitter audit.Emitter
	// auditCh is the 256-slot buffered channel that decouples Use from
	// the emitter I/O. Nil when no emitter is wired.
	auditCh chan auditEmitJob

	// auditSweeper is the optional deletion backend for SweepAuditLog
	// (WP07). nil = no-op.
	auditSweeper AuditSweeper
	// retentionDaysFn, when non-nil, returns the current retention
	// window in days. Zero means forever (spec Q26.1 default). Read
	// by the background sweep goroutine on every tick.
	retentionDaysFn func() int
	// sweepLastRan tracks the last sweep wall time (atomic pointer swap,
	// no lock). nil = never swept.
	sweepLastRan *time.Time

	// cedarGate is the optional Cedar policy gate consulted inside Use
	// before secret resolution. nil = no gate (default-allow), keeps
	// the test harness path simple. When set, evaluation routes through
	// cedar.CheckCredentialAccess (mission cedar-credential-policy
	// WP05).
	cedarGate cedar.Gate
	// strictMode is read on every Use call (not at construction time)
	// so flipping the Settings.CedarStrictCredentialMode dial takes
	// effect on the next Use without re-creating the store. nil
	// degrades to "false" (lenient).
	strictMode func() bool
	// promptRegistry is the process-singleton Cedar prompt registry
	// used by IssueForMCPSpawn when the Cedar gate returns
	// NotApplicable. nil = skip interactive prompt (default-allow).
	promptRegistry *cedar.Registry

	// policyDataDir and policyEngine are used by IssueForMCPSpawn to
	// write a persistent AllowAlways .cedar snippet when the user picks
	// "Allow always" in the credential modal. Both must be non-nil for
	// the persistent grant to be written; nil values degrade to
	// AllowOnce behaviour (in-session only). Injected via
	// WithMCPSpawnPolicyWriter (cedar-credential-policy follow-up).
	policyDataDir string
	policyEngine  *cedar.Engine
}

// Store is the public interface. Callers obtain a *store via New.
type Store interface {
	// Issue registers a new credential reference and returns a handle.
	Issue(ctx context.Context, ref secrets.CredentialReference, purpose AccessPurpose, opts ...IssueOption) (Handle, error)
	// Use resolves the credential behind h and passes the raw bytes to
	// op. The bytes are zeroed on return even if op panics.
	Use(ctx context.Context, h Handle, op func([]byte) error) error
	// Peek resolves the credential behind ref and returns a redacted
	// display value. No audit event is emitted.
	Peek(ctx context.Context, ref secrets.CredentialReference) (Redacted, error)
	// RoundTrip issues a single-use handle and immediately calls Use(op)
	// in one step. Convenience wrapper for callers that do not need to
	// manage the Handle lifecycle explicitly.
	RoundTrip(ctx context.Context, ref secrets.CredentialReference, purpose AccessPurpose, op func([]byte) error) error
	// IssueForMCPSpawn resolves the credentials required to spawn an
	// MCP stdio child process. The Cedar gate fires with
	// purpose="mcp_spawn"; NotApplicable triggers an interactive prompt
	// via the Registry (if wired). On Allow / prompt-Allow, the
	// resolved env map is returned. On Deny / prompt-Deny, an error is
	// returned and no bytes flow. recipeID is used as the Cedar resource
	// UID and as the CredPromptSurface.ProviderID. envKeys are the env
	// var names the recipe declares; only those keys are resolved.
	IssueForMCPSpawn(ctx context.Context, recipeID string, envKeys []string, backend secrets.Backend) (map[string]string, error)
	// SweepAuditLog deletes credential.accessed audit rows older than
	// retainDuration. When retainDuration is zero the call is a no-op
	// and returns (0, nil). Requires WithAuditSweeper to be wired;
	// without it the call is always a no-op (WP07).
	SweepAuditLog(ctx context.Context, retainDuration time.Duration) (int, error)
	// Close stops the expiration goroutine and zeroes all entries.
	Close()
}

// IssueOption is a functional option for Issue.
type IssueOption func(*issueConfig)

type issueConfig struct {
	ttl time.Duration
}

// WithTTL overrides the default handle TTL.
func WithTTL(d time.Duration) IssueOption {
	return func(c *issueConfig) { c.ttl = d }
}

// StoreOption configures a Store at construction time.
type StoreOption func(*store)

// WithCedarGate threads a Cedar policy gate + strictMode callback into
// the store. The gate fires inside Use() before secret resolution
// (mission cedar-credential-policy-01KQ8TDE WP05). nil gate is a no-op
// (no gating). nil strictMode degrades to "lenient" (NotApplicable
// outcomes allow access).
func WithCedarGate(g cedar.Gate, strictMode func() bool) StoreOption {
	return func(s *store) {
		s.cedarGate = g
		s.strictMode = strictMode
	}
}

// WithPromptRegistry threads the process-singleton Cedar prompt registry
// into the store. IssueForMCPSpawn uses it to fire an interactive
// credential prompt when the Cedar gate returns NotApplicable (no rule
// matched). nil registry = default-allow on NotApplicable (pre-boot
// posture, same behaviour as before WP05 was completed).
func WithPromptRegistry(r *cedar.Registry) StoreOption {
	return func(s *store) {
		s.promptRegistry = r
	}
}

// WithMCPSpawnPolicyWriter threads the data directory path and Cedar
// engine into the store so IssueForMCPSpawn can write a persistent
// .cedar AllowAlways grant when the user picks "Allow always" in the
// credential modal (cedar-credential-policy follow-up).
//
// When either argument is empty / nil the persistent grant is silently
// skipped; the interactive (in-session) transient grant still covers
// the current process lifetime.
func WithMCPSpawnPolicyWriter(dataDir string, engine *cedar.Engine) StoreOption {
	return func(s *store) {
		s.policyDataDir = dataDir
		s.policyEngine = engine
	}
}

// New constructs a Store. resolver is used by Use to fetch raw bytes.
// clock defaults to time.Now when nil (tests inject a fake clock).
func New(resolver secrets.ResolverAPI, clock func() time.Time, opts ...StoreOption) Store {
	if clock == nil {
		clock = time.Now
	}
	s := &store{
		entries:  make(map[Handle]*entry),
		resolver: resolver,
		clock:    clock,
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	go s.reapLoop()
	// Start the daily audit-retention sweep goroutine (WP07). It only
	// does meaningful work when retentionDaysFn + auditSweeper are both
	// wired; otherwise maybeSweep returns immediately on every tick.
	go s.sweepLoop()
	return s
}

// reapLoop runs in the background and deletes expired entries roughly
// every reapInterval. It NEVER holds the entries lock while doing
// anything external — it snapshots expired handles under the lock,
// releases, then deletes in a second pass.
func (s *store) reapLoop() {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.reapOnce()
		}
	}
}

func (s *store) reapOnce() {
	now := s.clock()

	// Phase 1: collect expired handles under the read lock.
	s.mu.RLock()
	var expired []Handle
	for h, e := range s.entries {
		if now.After(e.expiresAt) {
			expired = append(expired, h)
		}
	}
	s.mu.RUnlock()

	if len(expired) == 0 {
		return
	}

	// Phase 2: delete under the write lock (no external calls here).
	s.mu.Lock()
	for _, h := range expired {
		delete(s.entries, h)
	}
	s.mu.Unlock()
}

// Close signals the expiration goroutine to stop and clears all
// entries. Idempotent.
func (s *store) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.entries = make(map[Handle]*entry)
		s.mu.Unlock()
	})
}
