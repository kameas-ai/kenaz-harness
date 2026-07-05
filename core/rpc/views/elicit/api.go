// Package elicit is the view-scoped RPC surface for the
// ask-user-question interactive elicitation feature (mission
// ask-user-question-interactive-01KZNP3G, WP04).
//
// It exposes two RPC operations to the Wails frontend:
//
//   - OpenAskDialog(request ElicitRequest) ElicitResponse
//     Opens the AskUserQuestion dialog and blocks until the user
//     submits or cancels. Called indirectly by the kenaz__ask_user_question
//     tool's Delegate implementation.
//
//   - SubmitAskAnswer(requestID string, answer json.RawMessage, cancelled bool)
//     The frontend calls this when the user confirms or dismisses.
//     Resolves the pending OpenAskDialog call.
//
// Wire flow (synchronous single-question, WP04):
//
//  1. Model calls kenaz__ask_user_question → tool.Call → Delegate.OpenDialog
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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/asks"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
)

// requestIDSeq is a per-process monotonic counter used in newRequestID
// to ensure uniqueness even when calls happen within the same nanosecond.
var requestIDSeq uint64

// TopicElicitPending is the Wails event-broker topic the backend emits
// when a new elicitation request is ready for the frontend to render.
// The frontend's useEventStream composable subscribes to this topic to
// show the AskUserQuestion dialog.
//
// Payload: ElicitRequest (serialised as JSON).
const TopicElicitPending = "elicit:pending"

// TopicElicitDeferred is the Wails event-broker topic emitted when a
// deferred ask is registered (WP06). The frontend's DeferredAskPill
// subscribes to this topic to show the chat-header pill.
//
// Payload: ElicitRequest (serialised as JSON, includes Mode:"deferred").
const TopicElicitDeferred = "elicit:deferred"

// TopicElicitDeferredAnswered is emitted when a deferred ask is answered
// (WP06). The frontend removes the pill and queues the system_reminder.
//
// Payload: DeferredAnsweredPayload.
const TopicElicitDeferredAnswered = "elicit:deferred:answered"

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

	// WP05: multi-question wizard fields.
	// Questions carries the full batch when len > 0. If populated, the
	// frontend renders a wizard and the single-question fields above are
	// ignored.
	Questions []WizardQuestion `json:"questions,omitempty"`
	// Mode is "blocking" (default) or "deferred".
	Mode string `json:"mode,omitempty"`
}

// WizardQuestion is one question in a multi-step wizard batch.
// All fields mirror ElicitRequest for the single-question path so the
// frontend can render each step with the same sub-components.
//
// DependsOn enables conditional follow-up questions: the question only
// renders when its dependency condition is satisfied.
type WizardQuestion struct {
	// ID uniquely identifies the question within the batch.  Returned
	// as the key in WizardAnswer.Answers.
	ID       string `json:"id"`
	Question string `json:"question"`
	Kind     string `json:"kind"`

	Options     []askuserquestion.QuestionOption `json:"options,omitempty"`
	Placeholder string                           `json:"placeholder,omitempty"`
	Min         *float64                         `json:"min,omitempty"`
	Max         *float64                         `json:"max,omitempty"`
	Step        *float64                         `json:"step,omitempty"`

	// DependsOn makes this question conditional on a prior answer.
	// The question is shown only when the named question has been answered
	// and the condition is satisfied.
	DependsOn *WizardDependsOn `json:"depends_on,omitempty"`
}

// WizardDependsOn declares a dependency on a prior question's answer.
type WizardDependsOn struct {
	// QuestionID is the id of the question this one depends on.
	QuestionID string `json:"question_id"`
	// Condition is "answered" (any non-null answer) or
	// {"equals": <value>} or {"includes": <value>}.
	Condition json.RawMessage `json:"condition"`
}

// DeferredAnsweredPayload is the event payload for TopicElicitDeferredAnswered.
// The frontend uses this to remove the pill and surface a system_reminder.
type DeferredAnsweredPayload struct {
	AskID string `json:"ask_id"`
	// SystemReminder is the text the frontend should inject into the next
	// LLM turn (spec FR-025).
	SystemReminder string `json:"system_reminder"`
}

// DeferredResult is the immediate result returned to the model when the tool
// is called in deferred mode. The model uses AskID to retrieve the structured
// answer later via __ask_get_result(ask_id).
type DeferredResult struct {
	Deferred bool   `json:"deferred"`
	AskID    string `json:"ask_id"`
}

// WizardAnswer is the wire shape returned when a wizard (multi-question batch)
// completes. Each question id maps to the user's answer JSON.
type WizardAnswer struct {
	// Answers maps question_id → answer value (kind-dependent JSON type).
	Answers map[string]json.RawMessage `json:"answers"`
	// AnsweredSoFar is populated when Dismissed is true: the partial set
	// of answers before the user cancelled.
	AnsweredSoFar map[string]json.RawMessage `json:"answered_so_far,omitempty"`
	// Dismissed is true when the user cancelled the wizard mid-flow.
	Dismissed bool `json:"dismissed"`
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
	ch  chan ElicitResponse
	req ElicitRequest // stored so ListPending can return it on reconnect
}

// wizardEntry tracks state for a multi-question wizard in flight.
type wizardEntry struct {
	// questions is the full ordered list for this wizard batch.
	questions []WizardQuestion
	// answers accumulates answers as each step completes.
	answers map[string]json.RawMessage
	// ch is resolved with the final WizardAnswer JSON when complete or dismissed.
	ch chan ElicitResponse
}

// ElicitAPI is the view-scoped accessor the Bindings layer consumes.
type ElicitAPI interface {
	// SubmitAnswer resolves a pending elicitation. requestID was emitted
	// on TopicElicitPending; answerJSON is the user's answer (or null
	// when cancelled is true).
	SubmitAnswer(ctx context.Context, requestID string, answerJSON json.RawMessage, cancelled bool) error

	// SubmitWizardStep records one step answer for a multi-question wizard.
	// When all visible questions have been answered (or dismissed is true),
	// it resolves the pending OpenDialog channel with a WizardAnswer.
	// questionID identifies which question in the batch was answered.
	SubmitWizardStep(ctx context.Context, requestID string, questionID string, answerJSON json.RawMessage, dismissed bool) error

	// ListPending returns in-flight request IDs so the frontend can
	// reconcile its queue on reconnect / hot reload.
	ListPending(ctx context.Context) ([]ElicitRequest, error)

	// RegisterDeferred registers a deferred ask and emits TopicElicitDeferred.
	// Returns a DeferredResult{Deferred:true, AskID:…} for the model.
	// Returns error when the session has too many concurrent pending asks.
	RegisterDeferred(ctx context.Context, sessionID string, req ElicitRequest) (DeferredResult, error)

	// AnswerDeferred records the user's answer for a pending deferred ask,
	// emits TopicElicitDeferredAnswered, and returns the system_reminder text
	// to be injected into the next LLM turn.
	AnswerDeferred(ctx context.Context, askID string, answer any) (string, error)

	// ListDeferred returns pending deferred asks for a session.
	ListDeferred(ctx context.Context, sessionID string) ([]asks.DeferredAsk, error)
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
	// wizards tracks multi-question wizard sessions (WP05).
	wizards map[string]*wizardEntry
	// deferred is the deferred-ask registry (WP06).
	deferred *asks.DeferredRegistry

	// emitter pushes ElicitRequest payloads to the frontend.
	emitter Emitter

	// wailsCtx is the Wails OnStartup context required for EventsEmit.
	// Set via SetContext before the first OpenDialog call.
	wailsCtx context.Context
}

// Config holds construction parameters.
type Config struct {
	Emitter         Emitter
	// DeferredExpiry sets the TTL for deferred asks. Zero = default (24 h).
	DeferredExpiry  time.Duration
}

// New constructs an API. Emitter may be nil (tests where no frontend is
// attached); in that case events are dropped but SubmitAnswer still
// works if the test calls it directly.
func New(cfg Config) *API {
	return &API{
		pending:  make(map[string]pendingEntry),
		wizards:  make(map[string]*wizardEntry),
		deferred: asks.NewDeferredRegistry(cfg.DeferredExpiry),
		emitter:  cfg.Emitter,
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
	a.pending[reqID] = pendingEntry{ch: ch, req: req}
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

// SubmitWizardStep records one question's answer for a multi-step wizard.
// When all visible questions are answered, it resolves the pending OpenDialog
// channel. When dismissed is true, it resolves immediately with the partial
// set of answers so the model receives answered_so_far.
//
// The count of "visible" questions is the number of questions in the batch
// that either have no DependsOn or whose dependency condition is satisfied by
// the answers collected so far. This is evaluated conservatively: a question
// with an unsatisfied condition is excluded from the completion check.
func (a *API) SubmitWizardStep(_ context.Context, requestID string, questionID string, answerJSON json.RawMessage, dismissed bool) error {
	a.mu.Lock()
	w, ok := a.wizards[requestID]
	if !ok {
		a.mu.Unlock()
		return ErrUnknownRequest
	}

	if len(answerJSON) == 0 {
		answerJSON = json.RawMessage("null")
	}

	if dismissed {
		// Return partial answers so model receives answered_so_far.
		partial := make(map[string]json.RawMessage, len(w.answers))
		for k, v := range w.answers {
			partial[k] = v
		}
		delete(a.wizards, requestID)
		ch := w.ch
		a.mu.Unlock()

		wa := WizardAnswer{Dismissed: true, AnsweredSoFar: partial}
		encoded, _ := json.Marshal(wa)
		select {
		case ch <- ElicitResponse{RequestID: requestID, Answer: encoded}:
		default:
		}
		return nil
	}

	// Record this answer.
	w.answers[questionID] = answerJSON

	// Check if all visible questions have been answered.
	allAnswered := allVisibleAnswered(w.questions, w.answers)

	if allAnswered {
		answers := make(map[string]json.RawMessage, len(w.answers))
		for k, v := range w.answers {
			answers[k] = v
		}
		delete(a.wizards, requestID)
		ch := w.ch
		a.mu.Unlock()

		wa := WizardAnswer{Answers: answers}
		encoded, _ := json.Marshal(wa)
		select {
		case ch <- ElicitResponse{RequestID: requestID, Answer: encoded}:
		default:
		}
		return nil
	}

	a.mu.Unlock()
	return nil
}

// allVisibleAnswered returns true when every question that should be visible
// (no dependency, or dependency satisfied) has been answered.
func allVisibleAnswered(questions []WizardQuestion, answers map[string]json.RawMessage) bool {
	for _, q := range questions {
		if q.DependsOn != nil {
			// Check if the dependency is satisfied.
			priorAnswer, prior := answers[q.DependsOn.QuestionID]
			if !prior {
				// Dependency question not yet answered — this question is not
				// yet visible, skip it.
				continue
			}
			if !dependencyConditionMet(q.DependsOn.Condition, priorAnswer) {
				// Condition not met — question is hidden, skip it.
				continue
			}
		}
		// Question is visible — must be answered.
		if _, ok := answers[q.ID]; !ok {
			return false
		}
	}
	return true
}

// dependencyConditionMet evaluates a WizardDependsOn.Condition against the
// given prior answer JSON. Supports three forms:
//
//	"answered"              — any non-null answer satisfies the condition.
//	{"equals": <value>}     — answer must JSON-equal the value.
//	{"includes": <value>}   — answer (array) must contain the value.
func dependencyConditionMet(condition json.RawMessage, priorAnswer json.RawMessage) bool {
	if len(condition) == 0 {
		// Treat no condition as "answered".
		return string(priorAnswer) != "null" && len(priorAnswer) > 0
	}
	// Check for string literal "answered".
	var s string
	if json.Unmarshal(condition, &s) == nil && s == "answered" {
		return string(priorAnswer) != "null" && len(priorAnswer) > 0
	}
	// Check for {"equals": <value>}.
	var eq struct {
		Equals json.RawMessage `json:"equals"`
	}
	if json.Unmarshal(condition, &eq) == nil && len(eq.Equals) > 0 {
		return string(eq.Equals) == string(priorAnswer)
	}
	// Check for {"includes": <value>}.
	var inc struct {
		Includes json.RawMessage `json:"includes"`
	}
	if json.Unmarshal(condition, &inc) == nil && len(inc.Includes) > 0 {
		var arr []json.RawMessage
		if json.Unmarshal(priorAnswer, &arr) == nil {
			target := string(inc.Includes)
			for _, item := range arr {
				if string(item) == target {
					return true
				}
			}
		}
		return false
	}
	return false
}

// OpenWizard registers a multi-question wizard session and emits the full
// batch to the frontend via TopicElicitPending. It blocks until all
// visible questions are answered or the wizard is dismissed.
//
// This is called by the tool layer when the model submits more than one
// question in a batch.
func (a *API) OpenWizard(ctx context.Context, req ElicitRequest) (WizardAnswer, error) {
	const dialogTimeout = 10 * time.Minute

	reqID := newRequestID()
	req.RequestID = reqID
	ch := make(chan ElicitResponse, 1)

	entry := &wizardEntry{
		questions: req.Questions,
		answers:   make(map[string]json.RawMessage),
		ch:        ch,
	}

	a.mu.Lock()
	a.wizards[reqID] = entry
	wailsCtx := a.wailsCtx
	a.mu.Unlock()

	if a.emitter != nil && wailsCtx != nil {
		a.emitter.Emit(wailsCtx, TopicElicitPending, req)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, dialogTimeout)
	defer cancel()

	select {
	case resp := <-ch:
		var wa WizardAnswer
		if err := json.Unmarshal(resp.Answer, &wa); err != nil {
			return WizardAnswer{}, fmt.Errorf("elicit: decode wizard answer: %w", err)
		}
		return wa, nil
	case <-timeoutCtx.Done():
		a.mu.Lock()
		delete(a.wizards, reqID)
		a.mu.Unlock()
		return WizardAnswer{}, timeoutCtx.Err()
	}
}

// RegisterDeferred implements ElicitAPI. It registers a new deferred ask
// in the DeferredRegistry and emits TopicElicitDeferred to notify the
// frontend chat header. Returns immediately with DeferredResult.
func (a *API) RegisterDeferred(ctx context.Context, sessionID string, req ElicitRequest) (DeferredResult, error) {
	askID := newRequestID()
	req.RequestID = askID
	req.Mode = "deferred"

	if err := a.deferred.Register(sessionID, askID); err != nil {
		return DeferredResult{}, err
	}

	a.mu.Lock()
	wailsCtx := a.wailsCtx
	a.mu.Unlock()

	if a.emitter != nil && wailsCtx != nil {
		a.emitter.Emit(wailsCtx, TopicElicitDeferred, req)
	}

	return DeferredResult{Deferred: true, AskID: askID}, nil
}

// AnswerDeferred records the user's answer for a pending deferred ask.
// Emits TopicElicitDeferredAnswered and returns the system_reminder text.
func (a *API) AnswerDeferred(ctx context.Context, askID string, answer any) (string, error) {
	if err := a.deferred.Answer(askID, answer); err != nil {
		return "", err
	}
	reminder := asks.SystemReminderText(askID, answer)

	a.mu.Lock()
	wailsCtx := a.wailsCtx
	a.mu.Unlock()

	if a.emitter != nil && wailsCtx != nil {
		a.emitter.Emit(wailsCtx, TopicElicitDeferredAnswered, DeferredAnsweredPayload{
			AskID:          askID,
			SystemReminder: reminder,
		})
	}
	return reminder, nil
}

// ListDeferred returns pending deferred asks for a session.
func (a *API) ListDeferred(_ context.Context, sessionID string) ([]asks.DeferredAsk, error) {
	return a.deferred.ListPending(sessionID), nil
}

// ListPending returns the current in-flight blocking elicitation requests.
// The frontend calls this on reconnect (FR-007) to re-render any dialog
// that was open before the connection was lost.
//
// Returns a snapshot of all pending asks; callers receive enough information
// to show "an input is being requested — reconnect to continue" even if the
// full dialog cannot be reconstructed.
func (a *API) ListPending(_ context.Context) ([]ElicitRequest, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.pending) == 0 {
		return nil, nil
	}
	out := make([]ElicitRequest, 0, len(a.pending))
	for _, entry := range a.pending {
		out = append(out, entry.req)
	}
	return out, nil
}

// newRequestID generates a time-based hex request ID. The ID is unique
// within the process lifetime (nanosecond precision + per-process counter
// is sufficient; this is not a security boundary).
func newRequestID() string {
	seq := atomic.AddUint64(&requestIDSeq, 1)
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
