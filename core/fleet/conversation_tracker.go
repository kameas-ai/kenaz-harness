package fleet

// conversation_tracker.go — turns the runtime's turn-by-turn activity into the
// started/ended conversation lifecycle Fleet's v1 schema declares.
//
// # What a "conversation" is here
//
// A harness session has no natural end: a user can come back to it next week.
// Fleet's schema wants a conversation with a start, an end, a duration and
// token totals. The tracker defines a conversation as a SEGMENT — a contiguous
// stretch of activity in one session. A segment opens on the first turn after
// quiet, and closes when the session has been idle for IdleTimeout, when the
// process shuts down cleanly, or when export is deactivated.
//
// # What leaves the machine
//
// The map is keyed by the local session id, and that key never leaves this
// file. The exported conversation_id is a random UUID minted per segment, so
// it pairs a started record with its ended record and links nothing else —
// not two segments of the same session, not a session across restarts.
//
// # Pairing invariant
//
// conversation_ended is emitted only for a segment whose conversation_started
// was ACCEPTED by the emitter (consent on, pipeline active, class opted in).
// A segment opened while export was off is tracked locally so its turns do not
// open a second segment, but it is closed silently; the next turn after export
// comes on starts a fresh, reported segment. Fleet therefore never sees an
// orphaned "ended".

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// DefaultConversationIdleTimeout is how long a session must be quiet before
// its open segment is closed.
const DefaultConversationIdleTimeout = 10 * time.Minute

// conversationEmitter is the UsageEmitter surface the tracker uses.
type conversationEmitter interface {
	ConversationStarted(ctx context.Context, conversationID, providerKind string) bool
	ConversationEnded(ctx context.Context, conversationID string, duration time.Duration, tokenIn, tokenOut int64, costUSD float64) bool
	ToolInvoked(ctx context.Context, toolName string, latency time.Duration, success bool) bool
	Error(ctx context.Context, category ErrorCategory, recoverable bool) bool
}

type conversationSegment struct {
	id         string
	reported   bool
	startedAt  time.Time
	lastActive time.Time
	tokenIn    int64
	tokenOut   int64
	costUSD    float64
}

// ConversationTracker is safe for concurrent use. A nil tracker is a no-op.
type ConversationTracker struct {
	emitter     conversationEmitter
	idleTimeout time.Duration
	now         func() time.Time

	mu       sync.Mutex
	segments map[string]*conversationSegment // key: local session id — NEVER exported

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// TrackerOption configures a ConversationTracker.
type TrackerOption func(*ConversationTracker)

// WithIdleTimeout overrides DefaultConversationIdleTimeout.
func WithIdleTimeout(d time.Duration) TrackerOption {
	return func(t *ConversationTracker) {
		if d > 0 {
			t.idleTimeout = d
		}
	}
}

// WithTrackerClock injects a time source (tests).
func WithTrackerClock(now func() time.Time) TrackerOption {
	return func(t *ConversationTracker) {
		if now != nil {
			t.now = now
		}
	}
}

// NewConversationTracker builds a tracker over emitter. Call Start to run the
// idle janitor; without it segments still close on Close / DropAll / Sweep.
func NewConversationTracker(emitter *UsageEmitter, opts ...TrackerOption) *ConversationTracker {
	if emitter == nil {
		return nil
	}
	return newConversationTracker(emitter, opts...)
}

func newConversationTracker(emitter conversationEmitter, opts ...TrackerOption) *ConversationTracker {
	t := &ConversationTracker{
		emitter:     emitter,
		idleTimeout: DefaultConversationIdleTimeout,
		now:         time.Now,
		segments:    make(map[string]*conversationSegment),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Start runs the idle janitor until ctx is cancelled or Close is called.
func (t *ConversationTracker) Start(ctx context.Context) {
	if t == nil {
		return
	}
	interval := t.idleTimeout / 4
	if interval < time.Second {
		interval = time.Second
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	go func() {
		defer close(t.doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.stopCh:
				return
			case <-ticker.C:
				t.Sweep(ctx)
			}
		}
	}()
}

// TurnStarted records that a user turn began in sessionID. It opens a segment
// when none is open, or when the open one was never reported and export has
// since come on.
func (t *ConversationTracker) TurnStarted(ctx context.Context, sessionID, providerKind string) {
	if t == nil || sessionID == "" {
		return
	}
	now := t.now()

	t.mu.Lock()
	seg := t.segments[sessionID]
	if seg != nil && seg.reported {
		seg.lastActive = now
		t.mu.Unlock()
		return
	}
	// No segment, or an unreported one: (re)try to open a reported segment.
	id, err := newConversationID()
	if err != nil {
		t.mu.Unlock()
		return
	}
	fresh := &conversationSegment{id: id, startedAt: now, lastActive: now}
	if seg != nil {
		// Keep the quiet segment's start if this attempt is not accepted
		// either, so an all-unreported session stays one local segment.
		fresh.startedAt = seg.startedAt
	}
	t.segments[sessionID] = fresh
	t.mu.Unlock()

	// Emit outside the lock: the emitter may touch the OTel SDK.
	accepted := t.emitter.ConversationStarted(ctx, id, providerKind)

	t.mu.Lock()
	if cur := t.segments[sessionID]; cur == fresh {
		cur.reported = accepted
		if accepted {
			cur.startedAt = now
		}
	}
	t.mu.Unlock()
}

// LLMResponse adds one model response's usage to the session's open segment.
func (t *ConversationTracker) LLMResponse(_ context.Context, sessionID string, tokenIn, tokenOut int64, costUSD float64) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	seg := t.segments[sessionID]
	if seg == nil {
		return
	}
	seg.lastActive = t.now()
	if tokenIn > 0 {
		seg.tokenIn += tokenIn
	}
	if tokenOut > 0 {
		seg.tokenOut += tokenOut
	}
	if costUSD > 0 {
		seg.costUSD += costUSD
	}
}

// ToolInvoked reports one completed tool call and keeps the segment warm.
func (t *ConversationTracker) ToolInvoked(ctx context.Context, sessionID, toolName string, latency time.Duration, success bool) {
	if t == nil {
		return
	}
	t.touch(sessionID)
	t.emitter.ToolInvoked(ctx, toolName, latency, success)
}

// TurnFailed reports a failed turn by closed category.
func (t *ConversationTracker) TurnFailed(ctx context.Context, sessionID string, category ErrorCategory, recoverable bool) {
	if t == nil {
		return
	}
	t.touch(sessionID)
	t.emitter.Error(ctx, category, recoverable)
}

func (t *ConversationTracker) touch(sessionID string) {
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	if seg := t.segments[sessionID]; seg != nil {
		seg.lastActive = t.now()
	}
	t.mu.Unlock()
}

// Sweep closes every segment idle for at least the idle timeout. The janitor
// calls it; tests call it directly.
func (t *ConversationTracker) Sweep(ctx context.Context) {
	if t == nil {
		return
	}
	now := t.now()
	var due []*conversationSegment
	t.mu.Lock()
	for sid, seg := range t.segments {
		if now.Sub(seg.lastActive) >= t.idleTimeout {
			due = append(due, seg)
			delete(t.segments, sid)
		}
	}
	t.mu.Unlock()
	for _, seg := range due {
		t.end(ctx, seg)
	}
}

// EndAll closes every open segment and reports the reported ones. Clean
// shutdown path: the session is still valid, so the totals are worth sending.
func (t *ConversationTracker) EndAll(ctx context.Context) {
	if t == nil {
		return
	}
	for _, seg := range t.drain() {
		t.end(ctx, seg)
	}
}

// DropAll forgets every open segment WITHOUT reporting. Sign-out, account
// change and consent withdrawal path: the totals were gathered under a session
// that no longer stands, and must not be attributed to whatever comes next.
func (t *ConversationTracker) DropAll() {
	if t == nil {
		return
	}
	_ = t.drain()
}

// OpenSegments returns how many segments are open (diagnostics, tests).
func (t *ConversationTracker) OpenSegments() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.segments)
}

// Close stops the janitor. It does not report; callers choose EndAll or
// DropAll first.
func (t *ConversationTracker) Close() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() { close(t.stopCh) })
}

func (t *ConversationTracker) drain() []*conversationSegment {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*conversationSegment, 0, len(t.segments))
	for sid, seg := range t.segments {
		out = append(out, seg)
		delete(t.segments, sid)
	}
	return out
}

func (t *ConversationTracker) end(ctx context.Context, seg *conversationSegment) {
	if !seg.reported {
		return // pairing invariant: no started, no ended
	}
	t.emitter.ConversationEnded(ctx, seg.id, seg.lastActive.Sub(seg.startedAt),
		seg.tokenIn, seg.tokenOut, seg.costUSD)
}

// newConversationID mints a random RFC 4122 v4 UUID. Hand-rolled over
// crypto/rand so this package takes no new module dependency.
func newConversationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
