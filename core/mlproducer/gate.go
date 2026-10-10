package mlproducer

// gate.go — the consent gate (spec §4, plan "Gate"; ml-producer-01MLPRD01
// WP03). Recording into the outbox and shipping a batch both require:
//
//  1. signed in, fleet-enrolled, a node id;
//  2. the hosted_inference capability;
//  3. /me/ml effective with a current notice ack (MeML.IsEffective);
//  4. the org not paused;
//  5. the org's typed exclusions compiled (WP05; spec §12 A-11): the same
//     /me/ml read hands the newest lists to the recorder (OnExclusions)
//     before the decision is stored, so an open gate never records
//     under older exclusions than the read that opened it. A list this
//     build cannot honour closes the gate.
//
// WP05 removed WP03's dev-org-only guard: any org whose gate is open may
// record and ship, because its exclusions are now enforced on device.
//
// The Fleet calls live behind ConsentSource, implemented in core/rpc over
// core/fleet; this package stays fleet-free (check-no-fleet-imports.sh).
//
// Recording answers from a cached decision at most TTL (60 s) old,
// invalidated on every fleet session change, org pause change and
// capability change. Shipping re-reads everything before each batch.
// Any error closes the gate.
//
// Purge. A closure is either DEFINITIVE — Fleet said no (consent not
// effective, not entitled, an org this build may not ship for) — or
// TRANSIENT — we could not tell (a read error, enroll still pending at
// boot, capabilities not fetched yet, an access token awaiting refresh) or
// a hold that is explicitly not a withdrawal (a staff org pause: contract
// "Capability vs consent"). Sign-in, sign-out and node_removed purge
// through core/rpc's session-reset hook instead (records never cross a
// fleet session). Entering a definitive closure from any other state purges
// the unsent outbox once (spec §4: "Nothing recorded while gates are false
// is ever recovered"); a transient closure keeps it, so a flaky network or
// a slow boot enroll does not destroy consented data. The first decision
// after boot counts as "from another state": records left from a previous
// run whose consent has since been withdrawn are purged, never shipped.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Identity is who the producer would ship as.
type Identity struct {
	// SignedIn: a live fleet session (tokens present and not dead).
	SignedIn bool
	// Enrolled: this session completed an enroll.
	Enrolled bool
	// ResourceOrgID is the access token's resource-owner org claim —
	// kameas.org.id on the wire (Fleet 401s any other value).
	ResourceOrgID string
	// NodeID is the enrolled node id — kameas.node.id on the wire.
	NodeID string
}

// CapabilityState is the capability-poll half of the gate.
type CapabilityState struct {
	HostedInference bool
	// Paused: a staff org pause (a hold, not a withdrawal).
	Paused bool
}

// ErrCapabilitiesUnknown is what a ConsentSource returns when it has no
// fresh capability snapshot yet (boot before the first poll, or a stale
// cache): a transient closure, never a purge.
var ErrCapabilitiesUnknown = errors.New("mlproducer: capabilities not known yet")

// ConsentSource is the gate's view of Fleet. core/rpc implements it over
// core/fleet. Every method may do I/O; the gate never calls them on the
// tool-dispatch path.
type ConsentSource interface {
	Identity(ctx context.Context) (Identity, error)
	Capabilities(ctx context.Context) (CapabilityState, error)
	// MLConsent reads GET /api/v1/me/ml FRESH and returns
	// MeML.IsEffective() (effective AND a current notice ack) together
	// with the org's typed exclusions from the SAME read. A body whose
	// exclusions are malformed is an error (fail closed).
	MLConsent(ctx context.Context) (MLConsent, error)
}

// MLConsent is one /me/ml read, reduced to what the producer acts on.
type MLConsent struct {
	Effective  bool
	Exclusions Exclusions
}

// Gate closure reasons (Decision.Reason, and the shipping status's stop
// reason).
const (
	ReasonSignedOut        = "signed_out"
	ReasonNotEnrolled      = "not_enrolled"
	ReasonNoNodeID         = "no_node_id"
	ReasonNoOrgClaim       = "no_org_claim"
	ReasonCapsUnknown      = "capabilities_unknown"
	ReasonOrgPaused        = "org_paused"
	ReasonNotEntitled      = "not_entitled"
	ReasonNotEffective     = "ml_not_effective"
	ReasonConsentReadError = "consent_read_failed"
	// ReasonExclusionsInvalid: /me/ml carried an exclusion this build
	// cannot compile. Transient (no purge): nothing records until Fleet
	// serves lists the harness can honour.
	ReasonExclusionsInvalid = "exclusions_invalid"
)

// Decision is one evaluation of the gate.
type Decision struct {
	Open bool
	// Reason is why the gate is closed ("" when open).
	Reason string
	// Definitive: Fleet said no (see the package comment); entering a
	// definitive closure purges.
	Definitive bool
	// ResourceOrgID / NodeID are what a batch shipped under this decision
	// carries (set only when Open).
	ResourceOrgID string
	NodeID        string
	// At is when the decision was made.
	At  time.Time
	gen uint64
}

// GateConfig wires a ConsentGate.
type GateConfig struct {
	// Source reads Fleet. Required; nil keeps the gate closed.
	Source ConsentSource
	// TTL bounds how old a decision Recording may answer from. 0 = 60 s.
	TTL time.Duration
	// ReadTimeout bounds one evaluation. 0 = 20 s.
	ReadTimeout time.Duration
	// Now is the clock (tests). nil = time.Now.
	Now func() time.Time
	// Purge wipes the outbox (Recorder.Purge). Called on entering a
	// definitive closure.
	Purge func(ctx context.Context) error
	// OnChange is told when the decision's Open or Reason changes. Called
	// outside the gate's locks, on the evaluating goroutine; it must not
	// block on the shipper (core/rpc hands it to a goroutine).
	OnChange func(Decision)
	// OnExclusions receives the newest compiled exclusions after every
	// successful /me/ml read, before the decision is stored
	// (Recorder.SetExclusions). Must not block.
	OnExclusions func(*ExclusionSet)
	// HomeDir expands `~` in path globs; "" = the user's home directory.
	HomeDir string
}

// DefaultGateTTL is the recording cache's maximum age (spec §4).
const DefaultGateTTL = 60 * time.Second

// ConsentGate is the real Gate. Recording() is cheap and never does I/O.
type ConsentGate struct {
	cfg GateConfig

	snap       atomic.Pointer[Decision]
	gen        atomic.Uint64
	refreshing atomic.Bool

	// mu serialises evaluations and the purge transition.
	mu sync.Mutex
	// purged: the current definitive closure has already purged.
	purged bool

	lifeMu sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

var _ Gate = (*ConsentGate)(nil)

// NewConsentGate returns a closed gate; nothing is read until Refresh,
// Shipping, Invalidate, Start or the first Recording call.
func NewConsentGate(cfg GateConfig) *ConsentGate {
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultGateTTL
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 20 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &ConsentGate{cfg: cfg}
}

// Recording implements Gate: the last decision, if it is open, at most TTL
// old and not invalidated since. Otherwise false — and a background
// re-read is started, so the gate converges without anyone waiting on it.
func (g *ConsentGate) Recording() bool {
	if g == nil {
		return false
	}
	d := g.snap.Load()
	if d == nil || d.gen != g.gen.Load() || g.cfg.Now().Sub(d.At) > g.cfg.TTL {
		g.kick()
		return false
	}
	return d.Open
}

// Last returns the most recent decision (zero = never evaluated: closed).
func (g *ConsentGate) Last() Decision {
	if g == nil {
		return Decision{}
	}
	if d := g.snap.Load(); d != nil {
		return *d
	}
	return Decision{}
}

// Invalidate drops the cached decision (a fleet session change, an org
// pause change, a capability change) and re-reads in the background.
// Recording answers false until the re-read lands.
func (g *ConsentGate) Invalidate() {
	if g == nil {
		return
	}
	g.gen.Add(1)
	g.kick()
}

func (g *ConsentGate) kick() {
	if !g.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer g.refreshing.Store(false)
		g.Refresh(context.Background())
	}()
}

// Refresh evaluates the gate now (fresh reads) and returns the decision.
func (g *ConsentGate) Refresh(ctx context.Context) Decision {
	if g == nil {
		return Decision{}
	}
	g.mu.Lock()
	gen := g.gen.Load()
	rctx, cancel := context.WithTimeout(ctx, g.cfg.ReadTimeout)
	d := g.evaluate(rctx)
	cancel()
	d.gen = gen
	prev, changed := g.applyLocked(ctx, d)
	g.mu.Unlock()
	g.notify(prev, d, changed)
	return d
}

// Shipping is the shipper's per-batch check: always a fresh read.
func (g *ConsentGate) Shipping(ctx context.Context) Decision { return g.Refresh(ctx) }

// NotEffective records Fleet's 403 ml_not_effective: a definitive closure
// (purge), without waiting for the next read. The next Refresh re-reads
// /me/ml as the contract asks.
func (g *ConsentGate) NotEffective(ctx context.Context) {
	if g == nil {
		return
	}
	g.mu.Lock()
	d := Decision{Reason: ReasonNotEffective, Definitive: true, gen: g.gen.Load()}
	prev, changed := g.applyLocked(ctx, d)
	g.mu.Unlock()
	g.notify(prev, d, changed)
}

// applyLocked stamps and stores d, purging on entry into a definitive
// closure. Caller holds g.mu.
func (g *ConsentGate) applyLocked(ctx context.Context, d Decision) (Decision, bool) {
	if d.At.IsZero() {
		d.At = g.cfg.Now()
	}
	var prev Decision
	hadPrev := false
	if p := g.snap.Load(); p != nil {
		prev, hadPrev = *p, true
	}
	switch {
	case d.Open:
		g.purged = false
	case d.Definitive && !g.purged:
		g.purged = true
		if g.cfg.Purge != nil {
			if err := g.cfg.Purge(context.WithoutCancel(ctx)); err != nil {
				// Not fatal: the gate stays closed (nothing ships), and the
				// next definitive transition tries again.
				g.purged = false
				logging.L().Warn("mlproducer.gate.purge_failed", "reason", d.Reason, "err", err.Error())
			} else {
				logging.L().Info("mlproducer.gate.purged", "reason", d.Reason)
			}
		}
	}
	dd := d
	g.snap.Store(&dd)
	changed := !hadPrev || prev.Open != d.Open || prev.Reason != d.Reason
	return prev, changed
}

func (g *ConsentGate) notify(prev, d Decision, changed bool) {
	if !changed {
		return
	}
	logging.L().Info("mlproducer.gate.changed", "open", d.Open, "reason", d.Reason, "was_open", prev.Open)
	if g.cfg.OnChange != nil {
		g.cfg.OnChange(d)
	}
}

// evaluate runs the checks in order; the first failure decides.
func (g *ConsentGate) evaluate(ctx context.Context) Decision {
	src := g.cfg.Source
	if src == nil {
		return Decision{Reason: ReasonSignedOut}
	}
	id, err := src.Identity(ctx)
	if err != nil {
		return Decision{Reason: ReasonConsentReadError}
	}
	switch {
	case !id.SignedIn:
		// Not definitive: SignedIn is also false while an access token
		// awaits refresh. Sign-out itself purges through the session-reset
		// hook core/rpc registers, not through this read.
		return Decision{Reason: ReasonSignedOut}
	case !id.Enrolled:
		return Decision{Reason: ReasonNotEnrolled}
	case id.NodeID == "":
		return Decision{Reason: ReasonNoNodeID}
	case id.ResourceOrgID == "":
		return Decision{Reason: ReasonNoOrgClaim}
	}
	caps, err := src.Capabilities(ctx)
	if err != nil {
		return Decision{Reason: ReasonCapsUnknown}
	}
	// A pause is checked before entitlement and /me/ml: Fleet forces
	// effective=false while paused, and a pause is not a withdrawal.
	if caps.Paused {
		return Decision{Reason: ReasonOrgPaused}
	}
	if !caps.HostedInference {
		return Decision{Reason: ReasonNotEntitled, Definitive: true}
	}
	consent, err := src.MLConsent(ctx)
	if err != nil {
		return Decision{Reason: ReasonConsentReadError}
	}
	// The newest exclusions apply immediately, whatever the decision
	// (contract "Typed exclusions": re-read /me/ml and apply the current
	// lists). A broadening change also bumps notice_version server side,
	// so effective is false below until the member re-acks — no version
	// gating of our own.
	set, err := CompileExclusions(consent.Exclusions, g.cfg.HomeDir)
	if err != nil {
		logging.L().Warn("mlproducer.gate.exclusions_invalid", "version", consent.Exclusions.Version)
		return Decision{Reason: ReasonExclusionsInvalid}
	}
	if g.cfg.OnExclusions != nil {
		g.cfg.OnExclusions(set)
	}
	if !consent.Effective {
		return Decision{Reason: ReasonNotEffective, Definitive: true}
	}
	return Decision{Open: true, ResourceOrgID: id.ResourceOrgID, NodeID: id.NodeID}
}

// Start runs the background refresher: a re-read every 5/6 of the TTL, so
// an open gate never ages out under an active session. Idempotent. Stop
// ends it.
func (g *ConsentGate) Start(ctx context.Context) {
	if g == nil {
		return
	}
	g.lifeMu.Lock()
	defer g.lifeMu.Unlock()
	if g.cancel != nil {
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	g.cancel, g.done = cancel, done
	every := g.cfg.TTL * 5 / 6
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		g.Refresh(lctx)
		for {
			select {
			case <-lctx.Done():
				return
			case <-t.C:
				g.Refresh(lctx)
			}
		}
	}()
}

// Stop ends the refresher and waits for it.
func (g *ConsentGate) Stop() {
	if g == nil {
		return
	}
	g.lifeMu.Lock()
	cancel, done := g.cancel, g.done
	g.cancel, g.done = nil, nil
	g.lifeMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
