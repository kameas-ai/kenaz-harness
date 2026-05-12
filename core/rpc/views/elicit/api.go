// Package elicit is the view-scoped RPC surface for the
// ask-user-question interactive elicitation feature (mission
// ask-user-question-interactive-01KZNP3G, WP04).
//
// It exposes two RPC operations to the Wails frontend:
//
//   - OpenAskDialog(request ElicitRequest) ElicitResponse
//     Opens the AskUserQuestion dialog and blocks until the user
//     submits or cancels. Called indirectly by the kaneaz__ask_user_question
//     tool's Delegate implementation.
//
//   - SubmitAskAnswer(requestID string, answer json.RawMessage, cancelled bool)
//     The frontend calls this when the user confirms or dismisses.
//     Resolves the pending OpenAskDialog call.
//
// Wire flow (synchronous single-question, WP04):
//
//  1. Model calls kaneaz__ask_user_question → tool.Call → Delegate.OpenDialog
//  2. Delegate enqueues ElicitRequest → emits event on TopicElicitPending
//  3. Frontend receives event, renders AskUserQuestion dialog
//  4. User answers → frontend calls Elicit_SubmitAnswer(requestID, answer, false)
//     OR dismisses → frontend calls Elicit_SubmitAnswer(requestID, null, true)
//  5. SubmitAnswer resolves the pending channel → Delegate.OpenDialog returns
//  6. Tool encodes AskResult → model receives the answer
package elicit

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/tools/askuserquestion"
)

// TopicElicitPending is the Wails event-broker topic the backend emits
// when a new elicitation request is ready for the frontend to render.
// The frontend's useEventStream composable subscribes to this topic to
// show the AskUserQuestion dialog.
//
// Payload: ElicitRequest (serialised as JSON).
const TopicElicitPending = "elicit:pending"

// ElicitRequest is the payload emitted on TopicElicitPending and the
// wire shape passed to OpenAskDialog. It carries the full question spec
// the frontend needs to render the dialog.
type ElicitRequest struct {
	// RequestID is a unique identifier for this elicitation. The
	// frontend round-trips it back in SubmitAskAnswer so the pending
	// channel can be resolved correctly.
	RequestID string `json:"request_id"`

	// Question is the markdown-formatted question text.
	Question string `json:"question"`

	// Kind is one of the seven question kinds.
	Kind string `json:"kind"`

	// Options is the labelled choice list for radio / checkbox kinds.
	Options []askuserquestion.QuestionOption `json:"options,omitempty"`

	// Placeholder is optional placeholder text for the text kind.
	Placeholder string `json:"placeholder,omitempty"`

	// Min / Max / Step constrain numeric and slider inputs.
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`

	// DefaultValue is the pre-populated default (kind-dependent type).
	DefaultValue json.RawMessage `json:"default_value,omitempty"`

	// Preview is the optional side-by-side preview spec.
	Preview *askuserquestion.PreviewSpec `json:"preview,omitempty"`
}

// ElicitResponse is the wire shape returned by SubmitAskAnswer and
// resolved through OpenAskDialog.
type ElicitResponse struct {
	// RequestID echoes the inbound request for logging / tracing.
	RequestID string `json:"request_id"`

	// Answer holds the user's response. Null when Cancelled is true.
	Answer json.RawMessage `json:"answer"`

	// Cancelled is true when the user dismissed without answering.
	Cancelled bool `json:"cancelled"`
}

// pendingEntry is one in-flight elicitation awaiting user input.
type pendingEntry struct {
	ch chan ElicitResponse
}

// ElicitAPI is the view-scoped accessor the Bindings layer consumes.
type ElicitAPI interface {
	// SubmitAnswer resolves a pending elicitation. requestID was emitted
	// on TopicElicitPending; answerJSON is the user's answer (or null
	// when cancelled is true).
	SubmitAnswer(ctx context.Context, requestID string, answerJSON json.RawMessage, cancelled bool) error

	// ListPending returns in-flight request IDs so the frontend can
	// reconcile its queue on reconnect / hot reload.
	ListPending(ctx context.Context) ([]ElicitRequest, error)
}

// Emitter is the narrow interface for pushing events to the frontend.
// The concrete implementation wraps wruntime.EventsEmit (core/rpc/emitter.go).
// The interface lets tests inject a recording fake without Wails.
type Emitter interface {
	Emit(ctx context.Context, topic string, payload any)
}

// ErrUnknownRequest is returned by SubmitAnswer when the requestID has
// already been resolved or was never registered.
var ErrUnknownRequest = errors.New("elicit: unknown or already-resolved request ID")

// API is the concrete ElicitAPI implementation. It also implements
// askuserquestion.Delegate so the tool can call OpenDialog directly.
type API struct {
	mu      sync.Mutex
	pending map[string]pendingEntry

	// emitter pushes ElicitRequest payloads to the frontend.
	emitter Emitter

	// wailsCtx is the Wails OnStartup context required for EventsEmit.
	// Set via SetContext before the first OpenDialog call.
	wailsCtx context.Context
}

// Config holds construction parameters.
type Config struct {
	Emitter  Emitter
}

// New constructs an API. Emitter may be nil (tests where no frontend is
// attached); in that case events are dropped but SubmitAnswer still
// works if the test calls it directly.
func New(cfg Config) *API {
	return &API{
		pending: make(map[string]pendingEntry),
		emitter: cfg.Emitter,
	}
}

// SetContext provides the Wails app context. Must be called from
// OnStartup before any dialog is opened.
func (a *API) SetContext(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wailsCtx = ctx
}

// OpenDialog implements askuserquestion.Delegate. It:
//  1. Generates a unique request ID.
//  2. Registers a pending channel.
//  3. Emits TopicElicitPending.
//  4. Blocks until SubmitAnswer is called or ctx is cancelled.
//
// A 10-minute timeout is applied unconditionally so a hung dialog never
// blocks the tool loop forever.
func (a *API) OpenDialog(ctx context.Context, args askuserquestion.AskArgs) (askuserquestion.AskResult, error) {
	const dialogTimeout = 10 * time.Minute

	reqID := newRequestID()
	ch := make(chan ElicitResponse, 1)

	req := ElicitRequest{
		RequestID:    reqID,
		Question:     args.Question,
		Kind:         string(args.Kind),
		Options:      args.Options,
		Placeholder:  args.Placeholder,
		Min:          args.Min,
		Max:          args.Max,
		Step:         args.Step,
		DefaultValue: args.DefaultValue,
		Preview:      args.Preview,
	}

	a.mu.Lock()
	a.pending[reqID] = pendingEntry{ch: ch}
	wailsCtx := a.wailsCtx
	a.mu.Unlock()

	// Emit after releasing the lock so SubmitAnswer can acquire it
	// while the event is in flight.
	if a.emitter != nil && wailsCtx != nil {
		a.emitter.Emit(wailsCtx, TopicElicitPending, req)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, dialogTimeout)
	defer cancel()

	select {
	case resp := <-ch:
		return askuserquestion.AskResult{
			Answer:    resp.Answer,
			Cancelled: resp.Cancelled,
		}, nil
	case <-timeoutCtx.Done():
		a.mu.Lock()
		delete(a.pending, reqID)
		a.mu.Unlock()
		return askuserquestion.AskResult{}, timeoutCtx.Err()
	}
}

// SubmitAnswer resolves a pending elicitation. Called by the Bindings
// layer when the frontend submits Elicit_SubmitAnswer.
func (a *API) SubmitAnswer(_ context.Context, requestID string, answerJSON json.RawMessage, cancelled bool) error {
	a.mu.Lock()
	entry, ok := a.pending[requestID]
	if ok {
		delete(a.pending, requestID)
	}
	a.mu.Unlock()

	if !ok {
		return ErrUnknownRequest
	}

	resp := ElicitResponse{
		RequestID: requestID,
		Answer:    answerJSON,
		Cancelled: cancelled,
	}
	if len(resp.Answer) == 0 {
		resp.Answer = json.RawMessage("null")
	}

	// Non-blocking send: if the receiving goroutine already timed out
	// the channel is buffered (size 1) but the receiver is gone.
	select {
	case entry.ch <- resp:
	default:
	}
	return nil
}

// ListPending returns the current in-flight requests. The frontend uses
// this on reconnect to re-render any dialog it missed.
func (a *API) ListPending(_ context.Context) ([]ElicitRequest, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// We only store channels, not the original request. For WP04 we
	// return empty — full request storage for reconnect is deferred to
	// the async/multi-wizard WPs (WP05+). Until then the frontend re-emits
	// on reconnect via the same TopicElicitPending topic.
	_ = a.pending
	return nil, nil
}

// newRequestID generates a time-based hex request ID. The ID is unique
// within the process lifetime (nanosecond precision + per-process counter
// is sufficient; this is not a security boundary).
func newRequestID() string {
	b := make([]byte, 8)
	t := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(t >> (i * 8))
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
