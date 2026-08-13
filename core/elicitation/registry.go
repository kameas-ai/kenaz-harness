package elicitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MaxConcurrentDeferred is the default cap on in-flight deferred asks
// per session (ask-user-question-interactive-01KZNP3G FR-027).
const MaxConcurrentDeferred = 5

// DefaultExpiry is the default TTL for a deferred ask (FR-026).
const DefaultExpiry = 24 * time.Hour

// Status values for an Entry.
type Status string

// Entry statuses.
const (
	StatusPending  Status = "pending"
	StatusAnswered Status = "answered"
	StatusDeclined Status = "declined"
	StatusExpired  Status = "expired"
)

// Mode distinguishes the two lifetimes an ask can have.
//
// ModeBlocking: something is waiting for the answer — a goroutine
// parked in Park (the tool dialog) or a graph run parked on ErrPaused
// (the `ask` node). Blocking asks never expire: absence of an answer is
// not an outcome. The waiter's own context is the only clock.
//
// ModeDeferred: fire-and-forget. The model continues its turn, the user
// answers in their own time, and the answer arrives as a
// system_reminder on the next turn. Deferred asks are capped per session
// and DO expire.
type Mode string

// Ask modes. The string values are the frontend wire contract
// (ElicitRequest.Mode).
const (
	ModeBlocking Mode = "blocking"
	ModeDeferred Mode = "deferred"
)

// ErrTooManyPending is returned by Register when a session already has
// MaxConcurrentDeferred deferred asks in flight.
var ErrTooManyPending = errors.New("elicitation: too many concurrent pending deferred asks")

// ErrUnknown is returned when the referenced ask id is not registered or
// has already been resolved.
var ErrUnknown = errors.New("elicitation: unknown or already-resolved ask id")

// ErrDuplicate is returned by Register / Park when the supplied ID is
// already parked. Generated IDs never collide, so this indicates a
// caller bug (e.g. re-registering the same graph node twice without
// resolving it).
var ErrDuplicate = errors.New("elicitation: an ask is already pending under this id")

// Request is the input to Register / Park.
type Request struct {
	// ID is the caller-supplied key. Empty means "generate one". Graph
	// nodes pass NodeAskID(runID, nodeID) so the entry survives the
	// pause/resume round trip and is addressable on the way back.
	ID string

	// SessionID scopes the ask. The deferred concurrency cap is
	// per-session.
	SessionID string

	// RunID / NodeID identify the graph run and node when the ask came
	// from an `ask` node. Empty for tool-originated asks.
	RunID  string
	NodeID string

	// Question is the question spec.
	Question Question

	// Mode is ModeBlocking (default) or ModeDeferred.
	Mode Mode
}

// Entry is one registered ask. It is the registry record and the
// payload every transport adapts to its own wire shape.
type Entry struct {
	ID        string    `json:"ask_id"`
	SessionID string    `json:"session_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Question  Question  `json:"question"`
	Mode      Mode      `json:"mode"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// ExpiresAt is the deferred-ask deadline. Zero for blocking asks,
	// which never expire.
	ExpiresAt time.Time `json:"expires_at,omitzero"`

	// Answer is populated once Status is StatusAnswered.
	Answer Answer `json:"answer,omitzero"`

	// DeclineReason is populated once Status is StatusDeclined or
	// StatusExpired ("expired").
	DeclineReason string `json:"decline_reason,omitempty"`

	// Partial accumulates per-question answers while a Batch wizard is
	// in flight.
	Partial map[string]json.RawMessage `json:"partial,omitempty"`
}

// Publisher announces a newly-parked ask to a transport. Production
// binds it to the Wails event emitter; tests inject a recording fake.
// Nil is tolerated — the registry still parks and resolves, it just
// never announces itself.
type Publisher func(Entry)

// Config configures a Registry.
type Config struct {
	// Expiry is the deferred-ask TTL. Zero uses DefaultExpiry.
	Expiry time.Duration

	// MaxDeferred caps in-flight deferred asks per session. Zero uses
	// MaxConcurrentDeferred.
	MaxDeferred int

	// Publish announces newly-parked asks. Optional.
	Publish Publisher

	// Now is the clock. Zero uses time.Now; tests inject a fake.
	Now func() time.Time
}

// entry is the internal record: the public Entry plus the channel a
// blocking waiter listens on. The channel is buffered (1) so a resolver
// never blocks even when the waiter has already left via context
// cancellation.
type entry struct {
	rec Entry
	ch  chan Answer
}

// Registry is the one pending-ask store. Every elicitation surface in
// the harness — the `ask` node's durable pause, the
// kenaz__ask_user_question dialog, and deferred asks — registers here.
//
// Safe for concurrent use: asks are parked from kernel goroutines and
// resolved from RPC goroutines.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry

	expiry      time.Duration
	maxDeferred int
	publish     Publisher
	now         func() time.Time
}

// NewRegistry constructs a Registry.
func NewRegistry(cfg Config) *Registry {
	expiry := cfg.Expiry
	if expiry <= 0 {
		expiry = DefaultExpiry
	}
	maxDeferred := cfg.MaxDeferred
	if maxDeferred <= 0 {
		maxDeferred = MaxConcurrentDeferred
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Registry{
		entries:     make(map[string]*entry),
		expiry:      expiry,
		maxDeferred: maxDeferred,
		publish:     cfg.Publish,
		now:         now,
	}
}

// NodeAskID is the registry key for an `ask` node's pause. It is derived
// rather than generated so the entry is addressable again after the run
// resumes in a different goroutine.
//
// NUL separator: neither run ids nor node ids contain NUL.
func NodeAskID(runID, nodeID string) string { return "node\x00" + runID + "\x00" + nodeID }

// Register parks an ask and returns immediately. It backs the two
// non-waiting callers: the `ask` node (whose run parks on ErrPaused, not
// on a channel) and deferred asks.
//
// Returns ErrTooManyPending when a deferred ask would exceed the
// per-session cap, and ErrDuplicate when the id is already parked.
func (r *Registry) Register(req Request) (Entry, error) {
	e, err := r.register(req)
	if err != nil {
		return Entry{}, err
	}
	r.announce(e.rec)
	return e.rec, nil
}

// Park registers an ask and blocks until it is resolved or ctx is done.
// It backs the in-process dialog: the tool call cannot continue without
// an answer.
//
// There is no internal deadline — absence of an answer is not an
// outcome (the ConfirmBus doctrine, core/toolloop/confirm.go). Callers
// that want one supply it on ctx; core/rpc/views/elicit applies the
// 10-minute dialog timeout that way.
//
// On ctx cancellation the entry is unparked so it cannot leak.
func (r *Registry) Park(ctx context.Context, req Request) (Answer, error) {
	if req.Mode == ModeDeferred {
		return Answer{}, fmt.Errorf("elicitation: Park is for blocking asks; use Register for deferred")
	}
	e, err := r.register(req)
	if err != nil {
		return Answer{}, err
	}
	r.announce(e.rec)

	select {
	case a := <-e.ch:
		return a, nil
	case <-ctx.Done():
		r.mu.Lock()
		// Only drop our own entry: a concurrent Resolve may already
		// have removed it and a later ask may have reused the key.
		if cur, ok := r.entries[e.rec.ID]; ok && cur == e {
			delete(r.entries, e.rec.ID)
		}
		r.mu.Unlock()
		return Answer{}, ctx.Err()
	}
}

// register is the shared registration half of Register / Park.
func (r *Registry) register(req Request) (*entry, error) {
	mode := req.Mode
	if mode == "" {
		mode = ModeBlocking
	}
	now := r.now()

	e := &entry{
		rec: Entry{
			ID:        req.ID,
			SessionID: req.SessionID,
			RunID:     req.RunID,
			NodeID:    req.NodeID,
			Question:  req.Question,
			Mode:      mode,
			Status:    StatusPending,
			CreatedAt: now,
		},
		ch: make(chan Answer, 1),
	}
	if mode == ModeDeferred {
		e.rec.ExpiresAt = now.Add(r.expiry)
	}
	if len(req.Question.Batch) > 0 {
		e.rec.Partial = make(map[string]json.RawMessage, len(req.Question.Batch))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if mode == ModeDeferred {
		pending := 0
		for _, other := range r.entries {
			if other.rec.SessionID == req.SessionID &&
				other.rec.Mode == ModeDeferred &&
				other.rec.Status == StatusPending {
				pending++
			}
		}
		if pending >= r.maxDeferred {
			return nil, ErrTooManyPending
		}
	}

	if e.rec.ID == "" {
		e.rec.ID = newAskID()
	}
	if _, dup := r.entries[e.rec.ID]; dup {
		return nil, ErrDuplicate
	}
	r.entries[e.rec.ID] = e
	return e, nil
}

// announce fires the publisher outside the lock so a synchronous
// publisher may resolve re-entrantly.
func (r *Registry) announce(rec Entry) {
	r.mu.Lock()
	publish := r.publish
	r.mu.Unlock()
	if publish != nil {
		publish(rec)
	}
}

// Resolve delivers a user's answer. The entry leaves the pending set and
// any blocking waiter wakes.
//
// The answered record is retained (Status StatusAnswered) so the `ask`
// node can read it back on its second fire after the run resumes —
// LookupAnswer must be idempotent across a kernel resume.
func (r *Registry) Resolve(id string, a Answer) error {
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status != StatusPending {
		r.mu.Unlock()
		return ErrUnknown
	}
	e.rec.Status = StatusAnswered
	e.rec.Answer = a
	ch := e.ch
	r.mu.Unlock()

	// Buffered send: the waiter may already have left via ctx.
	select {
	case ch <- a:
	default:
	}
	return nil
}

// Decline resolves a pending ask as declined. Explicit dismissal arrives
// here; elapsed time only reaches it through SweepExpired, and only for
// deferred asks.
func (r *Registry) Decline(id, reason string) error {
	if reason == "" {
		reason = "declined by user"
	}
	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status != StatusPending {
		r.mu.Unlock()
		return ErrUnknown
	}
	e.rec.Status = StatusDeclined
	e.rec.DeclineReason = reason
	e.rec.Answer = Answer{Cancelled: true}
	ch := e.ch
	r.mu.Unlock()

	select {
	case ch <- Answer{Cancelled: true}:
	default:
	}
	return nil
}

// RecordStep records one sub-answer for a Batch (wizard) ask.
//
// When dismissed is true the ask resolves immediately with whatever was
// collected so far. Otherwise the step is recorded and, once every
// visible question is answered (BatchComplete), the ask resolves with an
// object keyed by question id.
//
// Returns whether the ask is now complete.
func (r *Registry) RecordStep(id, questionID string, answer json.RawMessage, dismissed bool) (complete bool, err error) {
	if len(answer) == 0 {
		answer = json.RawMessage("null")
	}

	r.mu.Lock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status != StatusPending {
		r.mu.Unlock()
		return false, ErrUnknown
	}
	if e.rec.Partial == nil {
		e.rec.Partial = map[string]json.RawMessage{}
	}
	if !dismissed {
		e.rec.Partial[questionID] = answer
	}
	partial := make(map[string]json.RawMessage, len(e.rec.Partial))
	for k, v := range e.rec.Partial {
		partial[k] = v
	}
	batch := e.rec.Question.Batch
	r.mu.Unlock()

	if !dismissed && !BatchComplete(batch, partial) {
		return false, nil
	}

	encoded, mErr := json.Marshal(BatchAnswer{
		Answers:       answersIf(!dismissed, partial),
		AnsweredSoFar: answersIf(dismissed, partial),
		Dismissed:     dismissed,
	})
	if mErr != nil {
		return false, fmt.Errorf("elicitation: encode batch answer: %w", mErr)
	}
	return true, r.Resolve(id, Answer{Value: encoded, Cancelled: dismissed})
}

// BatchAnswer is the resolved shape of a Batch ask. The JSON field names
// are the frontend + model wire contract for the multi-question wizard.
type BatchAnswer struct {
	// Answers maps question id → answer. Populated on completion.
	Answers map[string]json.RawMessage `json:"answers"`
	// AnsweredSoFar carries the partial set when Dismissed is true.
	AnsweredSoFar map[string]json.RawMessage `json:"answered_so_far,omitempty"`
	// Dismissed is true when the user cancelled mid-flow.
	Dismissed bool `json:"dismissed"`
}

// answersIf returns m when cond holds, nil otherwise. Keeps the
// completion / dismissal encodings from duplicating the marshal block.
func answersIf(cond bool, m map[string]json.RawMessage) map[string]json.RawMessage {
	if !cond {
		return nil
	}
	return m
}

// Get returns a snapshot of an entry.
func (r *Registry) Get(id string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok {
		return Entry{}, false
	}
	return e.rec, true
}

// Answered returns the recorded answer for an ask, with ok=true only
// once it has been answered. It is the read half of the `ask` node's
// pause/resume cycle and is deliberately non-destructive: the executor
// may fire more than once.
func (r *Registry) Answered(id string) (Answer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.rec.Status != StatusAnswered {
		return Answer{}, false
	}
	return e.rec.Answer, true
}

// Filter narrows ListPending. Zero-valued fields do not constrain.
type Filter struct {
	SessionID string
	RunID     string
	Mode      Mode
}

func (f Filter) matches(rec Entry) bool {
	if f.SessionID != "" && rec.SessionID != f.SessionID {
		return false
	}
	if f.RunID != "" && rec.RunID != f.RunID {
		return false
	}
	if f.Mode != "" && rec.Mode != f.Mode {
		return false
	}
	return true
}

// ListPending returns every pending ask matching f. Order is
// unspecified; callers needing determinism must sort.
//
// Expiry is enforced here rather than by a background timer: every read
// of the pending set sweeps first, so an expired deferred ask can never
// be reported as pending. That keeps the deferral TTL a real rule with
// no goroutine to leak — the previous owner of these semantics
// (core/asks.DeferredRegistry) exported SweepExpired and nothing ever
// called it, which made the 24-hour expiry documentation-only.
func (r *Registry) ListPending(f Filter) []Entry {
	r.SweepExpired()

	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Entry
	for _, e := range r.entries {
		if e.rec.Status != StatusPending || !f.matches(e.rec) {
			continue
		}
		out = append(out, e.rec)
	}
	return out
}

// PendingCount reports how many asks are currently pending. Test and
// telemetry helper.
func (r *Registry) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.entries {
		if e.rec.Status == StatusPending {
			n++
		}
	}
	return n
}

// SweepExpired marks every pending DEFERRED ask past its deadline as
// expired and returns how many it swept.
//
// Blocking asks are untouched by design: a parked graph run and an open
// dialog have a waiter, and converting their silence into an outcome
// would break the durable-pause contract.
func (r *Registry) SweepExpired() int {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for _, e := range r.entries {
		if e.rec.Mode != ModeDeferred || e.rec.Status != StatusPending {
			continue
		}
		if e.rec.ExpiresAt.IsZero() || !now.After(e.rec.ExpiresAt) {
			continue
		}
		e.rec.Status = StatusExpired
		e.rec.DeclineReason = "expired"
		n++
	}
	return n
}

// SystemReminderText renders the system_reminder the harness injects
// into the next LLM turn for an answered deferred ask (FR-025).
func SystemReminderText(askID string, answer any) string {
	return fmt.Sprintf("Pending elicitation %q answered: %v", askID, answer)
}

// ---- id generation ----

var askIDSeq uint64

// newAskID returns a process-unique ask identifier. Not a security
// boundary — uniqueness within the process lifetime is all that is
// required.
//
// The "elicit-" prefix and hex encoding are preserved from the WP04
// implementation because the id is round-tripped through the frontend.
func newAskID() string {
	seq := atomic.AddUint64(&askIDSeq, 1)
	t := uint64(time.Now().UnixNano())
	combined := t ^ (seq << 32)
	b := make([]byte, 8)
	for i := range b {
		b[i] = byte(combined >> (i * 8))
	}
	return "elicit-" + hexEncode(b)
}

// hexEncode returns a lowercase hex string for b.
func hexEncode(b []byte) string {
	const hx = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hx[v>>4]
		out[i*2+1] = hx[v&0xf]
	}
	return string(out)
}
