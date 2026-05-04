package harness

// cedar_proposer.go — real CedarProposer implementation for WP07.
//
// Mission: harness-self-mcp-onboarding-01KQ8TDU WP07.
//
// The proposer receives a (name, body) pair from the agent via
// harness_write_propose_cedar_policy, validates the Cedar syntax, emits a
// pending-proposal on broker topic "cedar:propose-pending", and blocks until
// the user accepts or rejects from the CedarProposeModal.
//
// On accept: writes the snippet via the injected PolicySnippetWriter and
//            emits a KindHarnessSelfPolicyWritten audit event.
// On reject: returns an error carrying the rejection reason so the agent
//            sees a structured "policy rejected" tool result.
//
// Architecture note: the proposer deliberately does NOT import the cedar-go
// library — syntax validation is delegated to the ValidateFunc seam so the
// harness package stays free of the cedar-go dependency at the type level.
// The rpc layer wires in cedar.NewPolicySetFromBytes as the validator.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event/kind"
)

// TopicCedarProposePending is the broker topic the frontend
// CedarProposeModal subscribes to. Stable contract with the frontend.
const TopicCedarProposePending = "cedar:propose-pending"

// CedarProposeDecision is the user's resolution from the modal.
type CedarProposeDecision string

const (
	CedarProposeAccept CedarProposeDecision = "accept"
	CedarProposeReject CedarProposeDecision = "reject"
)

// CedarProposalPayload is the broker-emitted payload the frontend renders.
type CedarProposalPayload struct {
	RequestID  string `json:"request_id"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	Rationale  string `json:"rationale,omitempty"`
	IssuedAt   string `json:"issued_at"`
	DeadlineAt string `json:"deadline_at"`
}

// PolicySnippetWriter writes a named Cedar policy snippet to persistent
// storage and reloads the engine. Satisfied by cedarpolicy.API.WritePolicySnippet
// in production; tests inject a stub.
type PolicySnippetWriter interface {
	WritePolicySnippet(ctx context.Context, name, body string) error
}

// CedarProposeDispatcher emits proposals on the broker so the frontend
// CedarProposeModal can render them.
type CedarProposeDispatcher interface {
	Dispatch(ctx context.Context, topic string, payload CedarProposalPayload)
}

// CedarProposeDispatcherFunc adapts a function to CedarProposeDispatcher.
type CedarProposeDispatcherFunc func(ctx context.Context, topic string, payload CedarProposalPayload)

func (f CedarProposeDispatcherFunc) Dispatch(ctx context.Context, topic string, payload CedarProposalPayload) {
	if f != nil {
		f(ctx, topic, payload)
	}
}

// CedarValidateFunc validates Cedar policy syntax. In production:
//
//	func(name, body string) error {
//	    _, err := cedar.NewPolicySetFromBytes(name, []byte(body))
//	    return err
//	}
type CedarValidateFunc func(name, body string) error

// ProposePending is a pending proposal awaiting user resolution.
type proposePending struct {
	resolveCh chan CedarProposeDecision
	timer     *time.Timer
	once      sync.Once
}

// CedarProposerImpl is the production CedarProposer. It implements the
// CedarProposer interface declared in handlers.go (WP07).
type CedarProposerImpl struct {
	dispatcher CedarProposeDispatcher
	writer     PolicySnippetWriter
	validate   CedarValidateFunc
	audit      AuditSink

	// propose timeout (default 5 minutes, overridable in tests).
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]*proposePending
}

// CedarProposerOptions configures a CedarProposerImpl.
type CedarProposerOptions struct {
	// Dispatcher emits the proposal on the broker topic. Required for
	// frontend interaction; may be nil in tests that drive round-trips
	// by calling ResolveProposeRequest directly.
	Dispatcher CedarProposeDispatcher

	// Writer persists accepted snippets. Required; Propose returns
	// errNotConfigured when nil.
	Writer PolicySnippetWriter

	// Validate checks Cedar syntax. When nil, syntax validation is
	// skipped (test/permissive path).
	Validate CedarValidateFunc

	// Audit emits policy-lifecycle events. When nil, events are dropped.
	Audit AuditSink

	// Timeout is the per-proposal user deadline. Defaults to 5 minutes.
	Timeout time.Duration
}

// NewCedarProposer constructs a CedarProposerImpl.
func NewCedarProposer(opts CedarProposerOptions) *CedarProposerImpl {
	to := opts.Timeout
	if to <= 0 {
		to = 5 * time.Minute
	}
	audit := opts.Audit
	if audit == nil {
		audit = noopAudit{}
	}
	return &CedarProposerImpl{
		dispatcher: opts.Dispatcher,
		writer:     opts.Writer,
		validate:   opts.Validate,
		audit:      audit,
		timeout:    to,
		pending:    map[string]*proposePending{},
	}
}

// Propose implements CedarProposer (handlers.go).
//
// It validates Cedar syntax, emits the proposal on the broker topic, and
// blocks until the user accepts or rejects (or the timeout fires). On
// accept it writes the snippet and emits an audit event. On reject it
// returns a descriptive error.
func (p *CedarProposerImpl) Propose(ctx context.Context, name, body string) error {
	if p.writer == nil {
		return errNotConfigured
	}

	// Step 1: validate Cedar syntax before bothering the user.
	if p.validate != nil {
		if err := p.validate(name, body); err != nil {
			return fmt.Errorf("harness_write_propose_cedar_policy: invalid Cedar syntax: %w", err)
		}
	}

	// Step 2: generate a request id and enqueue.
	id, err := proposeID()
	if err != nil {
		return fmt.Errorf("harness_write_propose_cedar_policy: id gen: %w", err)
	}
	now := time.Now().UTC()
	deadline := now.Add(p.timeout)

	entry := &proposePending{
		resolveCh: make(chan CedarProposeDecision, 1),
	}
	entry.timer = time.AfterFunc(p.timeout, func() {
		p.resolveEntry(id, CedarProposeReject)
	})

	p.mu.Lock()
	p.pending[id] = entry
	p.mu.Unlock()

	// Step 3: emit the proposal payload on the broker topic.
	payload := CedarProposalPayload{
		RequestID:  id,
		Name:       name,
		Body:       body,
		IssuedAt:   now.Format(time.RFC3339),
		DeadlineAt: deadline.Format(time.RFC3339),
	}
	if p.dispatcher != nil {
		p.dispatcher.Dispatch(ctx, TopicCedarProposePending, payload)
	}

	// Emit audit: policy proposed.
	p.audit.Emit(ctx, string(kind.KindHarnessSelfPolicyProposed), map[string]any{
		"name": name,
	})

	// Step 4: block until resolution, timeout, or ctx cancel.
	// resolveEntry removes the entry from p.pending before sending on
	// resolveCh, so no explicit cleanup is needed after we receive.
	var decision CedarProposeDecision
	select {
	case d := <-entry.resolveCh:
		decision = d
	case <-ctx.Done():
		// Deliver reject so the timer goroutine's resolveEntry returns false.
		p.resolveEntry(id, CedarProposeReject)
		// Drain in case resolveEntry already sent before we called it.
		select {
		case <-entry.resolveCh:
		default:
		}
		return fmt.Errorf("harness_write_propose_cedar_policy: context canceled: %w", ctx.Err())
	}

	// Step 5: act on the decision.
	switch decision {
	case CedarProposeAccept:
		if err := p.writer.WritePolicySnippet(ctx, name, body); err != nil {
			return fmt.Errorf("harness_write_propose_cedar_policy: write snippet %q: %w", name, err)
		}
		p.audit.Emit(ctx, string(kind.KindHarnessSelfPolicyWritten), map[string]any{
			"name":      name,
			"body_size": len(body),
		})
		return nil

	default: // CedarProposeReject or timeout
		p.audit.Emit(ctx, string(kind.KindHarnessSelfPolicyRejected), map[string]any{
			"name":   name,
			"reason": string(decision),
		})
		return errors.New("harness_write_propose_cedar_policy: policy proposal rejected by user")
	}
}

// ResolveProposeRequest resolves an in-flight proposal. Called by the RPC
// handler that the frontend CedarProposeModal posts to.
//
// Returns ErrUnknownProposeRequest when id is not in the pending map
// (already resolved, timed out, or never existed).
func (p *CedarProposerImpl) ResolveProposeRequest(id string, decision CedarProposeDecision) error {
	if !p.resolveEntry(id, decision) {
		return ErrUnknownProposeRequest
	}
	return nil
}

// ResolveProposeRequestRaw satisfies the rpc.CedarProposeResolver interface.
// It converts the raw string decision ("accept" | "reject") to a typed
// CedarProposeDecision and delegates to ResolveProposeRequest.
func (p *CedarProposerImpl) ResolveProposeRequestRaw(id, decision string) error {
	return p.ResolveProposeRequest(id, CedarProposeDecision(decision))
}

// resolveEntry delivers decision to the pending entry and removes it from
// the map. Returns false when the entry is not found. Safe for concurrent
// calls — sync.Once inside the lock guarantees exactly-once delivery.
func (p *CedarProposerImpl) resolveEntry(id string, decision CedarProposeDecision) bool {
	p.mu.Lock()
	entry, ok := p.pending[id]
	if !ok {
		p.mu.Unlock()
		return false
	}
	delete(p.pending, id)
	p.mu.Unlock()

	// once.Do is idempotent; timer.Stop races are benign (the timer
	// func calls resolveEntry too, and the entry is already gone from
	// the map by the time it acquires mu again).
	var delivered bool
	entry.once.Do(func() {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		entry.resolveCh <- decision
		delivered = true
	})
	return delivered
}

// ErrUnknownProposeRequest is returned by ResolveProposeRequest when the
// id is not in the pending map.
var ErrUnknownProposeRequest = errors.New("cedar: propose request id not found (already resolved or timed out)")

// proposeID generates a random 96-bit hex id for a proposal request.
func proposeID() (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "cprop-" + hex.EncodeToString(buf[:]), nil
}
