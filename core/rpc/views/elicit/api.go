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
//
// # Storage (01PMGX01 WP06)
//
// This view owns no registry of its own. Blocking dialogs, wizard
// batches and deferred asks were three separate maps here (plus
// core/asks.DeferredRegistry, a fourth store in its own package); they
// are now one core/elicitation.Registry — the same store the `ask` graph
// node's durable pause rides. This package is the *transport adapter*:
// it converts Registry entries to the frozen ElicitRequest wire shape,
// emits them on the broker topics above, and converts the frontend's
// answers back. The RPC surface is unchanged.
package elicit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

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

	// SessionID identifies the session this ask belongs to
	// (served-mode-is-a-real-mode-01PMZ707 WP01). Mirrors
	// elicitation.Entry.SessionID. Populated so core/serve's WS fan-out
	// can scope "elicit:pending" to the connection's subscribed
	// session instead of broadcasting every session's blocking asks to
	// every served client. Empty for the rare case where the ask was
	// parked with no session in context — core/serve fails that case
	// closed (does not forward) rather than guessing.
	SessionID string `json:"session_id,omitempty"`

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

// WizardAnswer is the wire shape returned when a wizard (multi-question
// batch) completes: each question id maps to the user's answer JSON, or
// AnsweredSoFar carries the partial set when the user cancelled mid-flow.
//
// It is an alias of elicitation.BatchAnswer — the completion rule and
// the encoding live with the question shape (01PMGX01 WP06), not with
// the transport. The JSON field names are unchanged.
type WizardAnswer = elicitation.BatchAnswer

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

	// ListPendingForSession is ListPending narrowed to one session
	// (served-mode-is-a-real-mode-01PMZ707 WP01). core/serve's WS
	// reconnect snapshot calls this instead of ListPending so a served
	// client only rebuilds dialog state for the session its stream is
	// subscribed to.
	ListPendingForSession(ctx context.Context, sessionID string) ([]ElicitRequest, error)

	// RegisterDeferred registers a deferred ask and emits TopicElicitDeferred.
	// Returns a DeferredResult{Deferred:true, AskID:…} for the model.
	// Returns error when the session has too many concurrent pending asks.
	RegisterDeferred(ctx context.Context, sessionID string, req ElicitRequest) (DeferredResult, error)

	// AnswerDeferred records the user's answer for a pending deferred ask,
	// emits TopicElicitDeferredAnswered, and returns the system_reminder text
	// to be injected into the next LLM turn.
	AnswerDeferred(ctx context.Context, askID string, answer any) (string, error)

	// ListDeferred returns pending deferred asks for a session.
	ListDeferred(ctx context.Context, sessionID string) ([]elicitation.Entry, error)
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
//
// It holds no pending-ask state: every in-flight ask — blocking dialog,
// wizard batch, or deferred — lives in the shared elicitation.Registry
// (01PMGX01 WP06). The mutex guards only the Wails context.
type API struct {
	mu sync.Mutex

	// registry is the one pending-ask store.
	registry *elicitation.Registry

	// emitter pushes ElicitRequest payloads to the frontend.
	emitter Emitter

	// wailsCtx is the Wails OnStartup context required for EventsEmit.
	// Set via SetContext before the first OpenDialog call.
	wailsCtx context.Context
}

// Config holds construction parameters.
type Config struct {
	Emitter Emitter
	// DeferredExpiry sets the TTL for deferred asks. Zero = default (24 h).
	DeferredExpiry time.Duration
}

// New constructs an API. Emitter may be nil (tests where no frontend is
// attached); in that case events are dropped but SubmitAnswer still
// works if the test calls it directly.
func New(cfg Config) *API {
	a := &API{emitter: cfg.Emitter}
	a.registry = elicitation.NewRegistry(elicitation.Config{
		Expiry:  cfg.DeferredExpiry,
		Publish: a.publish,
	})
	return a
}

// SetContext provides the Wails app context. Must be called from
// OnStartup before any dialog is opened.
func (a *API) SetContext(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wailsCtx = ctx
}

// publish is the Registry publisher: it renders a newly-parked ask onto
// the frontend wire shape and emits it on the matching broker topic.
// Deferred asks announce on TopicElicitDeferred; everything else on
// TopicElicitPending. This is the single announce leg — blocking
// dialogs, wizards and deferred asks all reach the frontend through it.
func (a *API) publish(e elicitation.Entry) {
	a.mu.Lock()
	emitter, wailsCtx := a.emitter, a.wailsCtx
	a.mu.Unlock()
	if emitter == nil || wailsCtx == nil {
		return
	}
	topic := TopicElicitPending
	if e.Mode == elicitation.ModeDeferred {
		topic = TopicElicitDeferred
	}
	emitter.Emit(wailsCtx, topic, requestOf(e))
}

// OpenDialog implements askuserquestion.Delegate. It parks the question
// in the registry (which announces it on TopicElicitPending) and blocks
// until SubmitAnswer resolves it or ctx is cancelled.
//
// A 10-minute timeout is applied unconditionally so a hung dialog never
// blocks the tool loop forever. The deadline lives here rather than in
// the registry on purpose: a parked *graph run* must never be timed out
// (durable-pause contract), so only this in-process caller supplies a
// clock.
func (a *API) OpenDialog(ctx context.Context, q elicitation.Question) (elicitation.Answer, error) {
	const dialogTimeout = 10 * time.Minute

	// SessionID rides the context: the toolloop dispatcher wraps ctx with
	// toolloop.WithSessionID before calling into the built-in tool pool
	// (core/rpc/views/agentgraph/chat/kernel_tool_adapter.go), and
	// kenaz__ask_user_question's Call forwards that same ctx straight
	// through to Delegate.OpenDialog. Reading it back here is what lets
	// core/serve's WS fan-out scope "elicit:pending" to one session
	// (served-mode-is-a-real-mode-01PMZ707 WP01) — without this the
	// registry entry (and therefore the wire ElicitRequest) would carry
	// an empty SessionID for every model-invoked ask, and the served
	// fan-out would fail closed on the one path that matters most.
	sessionID := toolloop.SessionIDFromContext(ctx)

	timeoutCtx, cancel := context.WithTimeout(ctx, dialogTimeout)
	defer cancel()

	return a.registry.Park(timeoutCtx, elicitation.Request{
		SessionID: sessionID,
		Question:  q,
		Mode:      elicitation.ModeBlocking,
	})
}

// SubmitAnswer resolves a pending elicitation. Called by the Bindings
// layer when the frontend submits Elicit_SubmitAnswer.
func (a *API) SubmitAnswer(_ context.Context, requestID string, answerJSON json.RawMessage, cancelled bool) error {
	var err error
	if cancelled {
		// Dismissal is a decline, not an answer: the waiter still wakes
		// with Cancelled=true, but the entry does not masquerade as
		// StatusAnswered in the store.
		err = a.registry.Decline(requestID, "dismissed by user")
	} else {
		if len(answerJSON) == 0 {
			answerJSON = json.RawMessage("null")
		}
		err = a.registry.Resolve(requestID, elicitation.JSONAnswer(answerJSON))
	}
	if errors.Is(err, elicitation.ErrUnknown) {
		return ErrUnknownRequest
	}
	return err
}

// SubmitWizardStep records one question's answer for a multi-step
// wizard. When every visible question is answered the parked call
// resolves with a WizardAnswer; when dismissed is true it resolves
// immediately with the partial set so the model receives
// answered_so_far.
func (a *API) SubmitWizardStep(_ context.Context, requestID string, questionID string, answerJSON json.RawMessage, dismissed bool) error {
	_, err := a.registry.RecordStep(requestID, questionID, answerJSON, dismissed)
	if errors.Is(err, elicitation.ErrUnknown) {
		return ErrUnknownRequest
	}
	return err
}

// OpenWizard parks a multi-question wizard and blocks until all visible
// questions are answered or the wizard is dismissed. It is called by the
// tool layer when the model submits more than one question in a batch.
func (a *API) OpenWizard(ctx context.Context, req ElicitRequest) (WizardAnswer, error) {
	const dialogTimeout = 10 * time.Minute

	// See OpenDialog's comment: SessionID rides the context the same way.
	sessionID := toolloop.SessionIDFromContext(ctx)

	timeoutCtx, cancel := context.WithTimeout(ctx, dialogTimeout)
	defer cancel()

	answer, err := a.registry.Park(timeoutCtx, elicitation.Request{
		SessionID: sessionID,
		Question:  questionOf(req),
		Mode:      elicitation.ModeBlocking,
	})
	if err != nil {
		return WizardAnswer{}, err
	}
	var wa WizardAnswer
	if err := json.Unmarshal(answer.Value, &wa); err != nil {
		return WizardAnswer{}, fmt.Errorf("elicit: decode wizard answer: %w", err)
	}
	return wa, nil
}

// RegisterDeferred implements ElicitAPI. It registers a deferred ask in
// the shared registry (which announces it on TopicElicitDeferred) and
// returns immediately with DeferredResult.
func (a *API) RegisterDeferred(_ context.Context, sessionID string, req ElicitRequest) (DeferredResult, error) {
	entry, err := a.registry.Register(elicitation.Request{
		SessionID: sessionID,
		Question:  questionOf(req),
		Mode:      elicitation.ModeDeferred,
	})
	if err != nil {
		return DeferredResult{}, err
	}
	return DeferredResult{Deferred: true, AskID: entry.ID}, nil
}

// AnswerDeferred records the user's answer for a pending deferred ask.
// Emits TopicElicitDeferredAnswered and returns the system_reminder text.
func (a *API) AnswerDeferred(_ context.Context, askID string, answer any) (string, error) {
	encoded, err := json.Marshal(answer)
	if err != nil {
		return "", fmt.Errorf("elicit: encode deferred answer: %w", err)
	}
	if err := a.registry.Resolve(askID, elicitation.JSONAnswer(encoded)); err != nil {
		return "", err
	}
	reminder := elicitation.SystemReminderText(askID, answer)

	a.mu.Lock()
	emitter, wailsCtx := a.emitter, a.wailsCtx
	a.mu.Unlock()

	if emitter != nil && wailsCtx != nil {
		emitter.Emit(wailsCtx, TopicElicitDeferredAnswered, DeferredAnsweredPayload{
			AskID:          askID,
			SystemReminder: reminder,
		})
	}
	return reminder, nil
}

// ListDeferred returns pending deferred asks for a session.
func (a *API) ListDeferred(_ context.Context, sessionID string) ([]elicitation.Entry, error) {
	return a.registry.ListPending(elicitation.Filter{
		SessionID: sessionID,
		Mode:      elicitation.ModeDeferred,
	}), nil
}

// ListPending returns the current in-flight blocking elicitation
// requests. The frontend calls this on reconnect (FR-007) to re-render
// any dialog that was open before the connection was lost.
func (a *API) ListPending(_ context.Context) ([]ElicitRequest, error) {
	entries := a.registry.ListPending(elicitation.Filter{Mode: elicitation.ModeBlocking})
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]ElicitRequest, 0, len(entries))
	for _, e := range entries {
		out = append(out, requestOf(e))
	}
	return out, nil
}

// ListPendingForSession is ListPending narrowed to one session
// (served-mode-is-a-real-mode-01PMZ707 WP01). core/serve's WS handler
// calls this for the reconnect snapshot instead of ListPending so a
// served client only rebuilds dialog state for the session its stream
// is subscribed to — the sibling of the elicitation.Filter{SessionID:
// …} pattern ListDeferred already uses above.
func (a *API) ListPendingForSession(_ context.Context, sessionID string) ([]ElicitRequest, error) {
	entries := a.registry.ListPending(elicitation.Filter{
		SessionID: sessionID,
		Mode:      elicitation.ModeBlocking,
	})
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]ElicitRequest, 0, len(entries))
	for _, e := range entries {
		out = append(out, requestOf(e))
	}
	return out, nil
}

// ---- wire <-> canonical conversion ----
//
// ElicitRequest is the frozen frontend contract (it is a Wails-bound
// type; see frontend/wailsjs/go/models.ts). elicitation.Question is the
// canonical shape. These two functions are the whole adapter.

// requestOf renders a registry entry as the frontend wire shape.
func requestOf(e elicitation.Entry) ElicitRequest {
	q := e.Question
	req := ElicitRequest{
		RequestID:    e.ID,
		SessionID:    e.SessionID,
		Question:     q.Text,
		Kind:         string(q.Kind),
		Placeholder:  q.Placeholder,
		Min:          q.Min,
		Max:          q.Max,
		Step:         q.Step,
		DefaultValue: q.DefaultValue,
		Options:      wireOptions(q.Options),
	}
	if q.Preview != nil {
		req.Preview = &askuserquestion.PreviewSpec{
			Kind:     q.Preview.Kind,
			Content:  q.Preview.Content,
			Language: q.Preview.Language,
		}
	}
	if e.Mode == elicitation.ModeDeferred {
		req.Mode = string(elicitation.ModeDeferred)
	}
	for _, sub := range q.Batch {
		wq := WizardQuestion{
			ID:          sub.ID,
			Question:    sub.Text,
			Kind:        string(sub.Kind),
			Options:     wireOptions(sub.Options),
			Placeholder: sub.Placeholder,
			Min:         sub.Min,
			Max:         sub.Max,
			Step:        sub.Step,
		}
		if sub.DependsOn != nil {
			wq.DependsOn = &WizardDependsOn{
				QuestionID: sub.DependsOn.QuestionID,
				Condition:  sub.DependsOn.Condition,
			}
		}
		req.Questions = append(req.Questions, wq)
	}
	return req
}

// questionOf parses the frontend wire shape into the canonical question.
func questionOf(req ElicitRequest) elicitation.Question {
	q := elicitation.Question{
		Text:         req.Question,
		Kind:         elicitation.Kind(req.Kind),
		Placeholder:  req.Placeholder,
		Min:          req.Min,
		Max:          req.Max,
		Step:         req.Step,
		DefaultValue: req.DefaultValue,
		Options:      canonicalOptions(req.Options),
	}
	if req.Preview != nil {
		q.Preview = &elicitation.Preview{
			Kind:     req.Preview.Kind,
			Content:  req.Preview.Content,
			Language: req.Preview.Language,
		}
	}
	for _, sub := range req.Questions {
		sq := elicitation.Question{
			ID:          sub.ID,
			Text:        sub.Question,
			Kind:        elicitation.Kind(sub.Kind),
			Options:     canonicalOptions(sub.Options),
			Placeholder: sub.Placeholder,
			Min:         sub.Min,
			Max:         sub.Max,
			Step:        sub.Step,
		}
		if sub.DependsOn != nil {
			sq.DependsOn = &elicitation.Dependency{
				QuestionID: sub.DependsOn.QuestionID,
				Condition:  sub.DependsOn.Condition,
			}
		}
		q.Batch = append(q.Batch, sq)
	}
	return q
}

func wireOptions(in []elicitation.Option) []askuserquestion.QuestionOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]askuserquestion.QuestionOption, 0, len(in))
	for _, o := range in {
		out = append(out, askuserquestion.QuestionOption{Value: o.Value, Label: o.Label})
	}
	return out
}

func canonicalOptions(in []askuserquestion.QuestionOption) []elicitation.Option {
	if len(in) == 0 {
		return nil
	}
	out := make([]elicitation.Option, 0, len(in))
	for _, o := range in {
		out = append(out, elicitation.Option{Value: o.Value, Label: o.Label})
	}
	return out
}
