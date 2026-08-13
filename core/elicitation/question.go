// Package elicitation is the single backing store for every user
// question the harness asks — whatever surface poses it.
//
// # Why this package exists
//
// Before agentgraph-total-convergence-01PMGX01 Phase 3 there were three
// unrelated elicitation surfaces (spec §1.3):
//
//   - the `ask` graph node, which paused a run through
//     agentgraph.AskBus and could only carry a free-form string;
//   - the kenaz__ask_user_question builtin tool, which owned seven
//     question kinds, validation and previews, and blocked in-process
//     on a bespoke pending map inside core/rpc/views/elicit;
//   - core/asks.DeferredRegistry, a third registry imported by exactly
//     one file, holding the deferral / expiry / system-reminder
//     semantics that neither of the other two could reach.
//
// They shared no code, so a capability added to one was invisible to
// the others. This package is the convergence: one Question type, one
// validator, one Registry. It follows the core/conversation reference
// pattern (spec §1.8) — the graph owns the primitive (`ask`), this
// package owns the storage, and one adapter per transport joins them:
//
//	core/agentgraph            → AskBus seam (memAskBus wraps a Registry)
//	core/rpc/views/agentgraph  → askRouter (graph-run pauses)
//	core/rpc/views/elicit      → API (Wails dialog transport)
//	core/tools/askuserquestion → model-facing schema shim
//
// # Dependency posture
//
// Standard library only. No agentgraph, no RPC, no Wails: every
// transport is injected (Registry.Config.Publish) so this package stays
// testable and importable from the kernel without a cycle.
package elicitation

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Kind is the closed enum of question types. The seven structured kinds
// are the model-facing contract of kenaz__ask_user_question and MUST NOT
// be renamed — the tool's InputSchema pins them.
//
// KindFreeform (the empty string) is the graph-authored plain-text ask:
// no dialog control, the answer is whatever string the user supplies.
// It is what every `ask` node meant before this package existed, and it
// stays the zero value so an un-migrated caller keeps today's behaviour.
type Kind string

// Question kinds.
const (
	KindFreeform Kind = ""
	KindRadio    Kind = "radio"
	KindCheckbox Kind = "checkbox"
	KindText     Kind = "text"
	KindNumber   Kind = "number"
	KindSlider   Kind = "slider"
	KindDate     Kind = "date"
	KindFile     Kind = "file"
)

// structuredKinds is the canonical set for O(1) membership checks.
var structuredKinds = map[Kind]struct{}{
	KindRadio:    {},
	KindCheckbox: {},
	KindText:     {},
	KindNumber:   {},
	KindSlider:   {},
	KindDate:     {},
	KindFile:     {},
}

// Structured reports whether k names one of the seven dialog kinds.
func (k Kind) Structured() bool {
	_, ok := structuredKinds[k]
	return ok
}

// Option is a single selectable entry for radio / checkbox kinds.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Preview describes the optional side-by-side preview pane.
type Preview struct {
	Kind     string `json:"kind"`
	Content  string `json:"content"`
	Language string `json:"language,omitempty"`
}

// Dependency makes a batch question conditional on a prior answer.
type Dependency struct {
	// QuestionID is the id of the question this one depends on.
	QuestionID string `json:"question_id"`
	// Condition is "answered" (any non-null answer), {"equals": <value>}
	// or {"includes": <value>}.
	Condition json.RawMessage `json:"condition"`
}

// Question is the one question shape in the harness. Every surface —
// the `ask` node's manifest attrs, the model-facing tool schema, and the
// frontend dialog wire — projects onto this type at its edge.
//
// Field-for-field it mirrors the frozen kenaz__ask_user_question
// argument contract, which is why the tool shim is a rename rather than
// a translation.
type Question struct {
	// ID identifies this question inside a Batch. Empty for a
	// single-question ask.
	ID string `json:"id,omitempty"`

	// Text is the markdown-formatted question shown to the user.
	Text string `json:"question"`

	// Kind selects the input control. KindFreeform means "plain text,
	// no dialog control" — the graph-authored default.
	Kind Kind `json:"kind,omitempty"`

	// Options is the labelled choice list for radio / checkbox.
	Options []Option `json:"options,omitempty"`

	// Placeholder is optional placeholder text for the text kind.
	Placeholder string `json:"placeholder,omitempty"`

	// Min / Max / Step constrain number and slider inputs. Pointers
	// because "unset" and "zero" are different answers.
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`

	// DefaultValue pre-populates the control. Its JSON type follows
	// Kind: string for text/date/radio/file, number for number/slider,
	// array of strings for checkbox.
	DefaultValue json.RawMessage `json:"default_value,omitempty"`

	// Preview is the optional side-by-side preview spec.
	Preview *Preview `json:"preview,omitempty"`

	// DependsOn makes this question conditional. Only meaningful for a
	// question inside a Batch.
	DependsOn *Dependency `json:"depends_on,omitempty"`

	// Batch, when non-empty, makes this a multi-question wizard: the
	// sub-questions are asked in order and the answer is a map keyed by
	// each sub-question's ID. Text/Kind on the parent are ignored.
	Batch []Question `json:"questions,omitempty"`
}

// Validate enforces the well-formedness rules shared by every surface.
//
// The error strings are load-bearing: they are what the model receives
// in kenaz__ask_user_question's invalid_args result, and they are pinned
// by core/tools/askuserquestion's tests. Do not reword them casually.
//
// A KindFreeform question is valid — that is the graph-authored ask.
// Surfaces whose contract requires an explicit control (the tool) call
// RequireStructured instead.
func (q Question) Validate() error {
	if len(q.Batch) > 0 {
		for i, sub := range q.Batch {
			if sub.ID == "" {
				return fmt.Errorf("questions[%d].id is required", i)
			}
			if err := sub.RequireStructured(); err != nil {
				return fmt.Errorf("questions[%d]: %w", i, err)
			}
		}
		return nil
	}
	if q.Text == "" {
		return errQuestionRequired
	}
	if q.Kind != KindFreeform && !q.Kind.Structured() {
		return unknownKindError(q.Kind)
	}
	return q.validateKindConstraints()
}

// RequireStructured is Validate plus the rule that the question must
// name one of the seven dialog kinds. The model-facing tool schema
// declares `kind` required, so a missing kind is invalid_args there even
// though it is the normal case for a graph-authored ask.
func (q Question) RequireStructured() error {
	if q.Text == "" {
		return errQuestionRequired
	}
	if !q.Kind.Structured() {
		return unknownKindError(q.Kind)
	}
	return q.validateKindConstraints()
}

// validateKindConstraints holds the per-kind rules.
func (q Question) validateKindConstraints() error {
	if (q.Kind == KindRadio || q.Kind == KindCheckbox) && len(q.Options) == 0 {
		return fmt.Errorf("kind=%q requires at least one option", q.Kind)
	}
	return nil
}

// errQuestionRequired is the missing-text error. Kept as a value so the
// two validators cannot drift on its wording — it is model-visible.
var errQuestionRequired = errors.New("question is required")

// unknownKindError renders the model-facing unknown-kind message. The
// wording is part of the tool's observable contract.
func unknownKindError(k Kind) error {
	return fmt.Errorf("unknown kind %q; must be one of radio, checkbox, text, number, slider, date, file", k)
}

// Answer is a resolved user response.
//
// Text and Value are two views of one answer, not two answers: a
// free-form ask fills Text and leaves Value nil; a structured ask fills
// Value with the kind-typed JSON and leaves Text empty unless the answer
// is a bare string. Callers that need "whatever the user said, as a
// string" use String().
type Answer struct {
	// Text is the canonical string form. It is the whole answer for a
	// free-form ask.
	Text string `json:"text,omitempty"`

	// Value is the kind-typed JSON answer for a structured ask: a
	// string for text/date/radio/file, a number for number/slider, an
	// array of strings for checkbox, or an object keyed by question id
	// for a Batch.
	Value json.RawMessage `json:"value,omitempty"`

	// Cancelled is true when the user dismissed the dialog without
	// answering.
	Cancelled bool `json:"cancelled,omitempty"`
}

// TextAnswer builds a free-form answer.
func TextAnswer(s string) Answer { return Answer{Text: s} }

// JSONAnswer builds a structured answer from raw kind-typed JSON. When
// the payload is a bare JSON string, Text is populated too so free-form
// consumers see the same value.
func JSONAnswer(v json.RawMessage) Answer {
	a := Answer{Value: v}
	var s string
	if len(v) > 0 && json.Unmarshal(v, &s) == nil {
		a.Text = s
	}
	return a
}

// String renders the answer as a plain string: Text when present,
// otherwise the raw JSON of Value. Never truncates — callers that need a
// preview must use a rune-safe truncator of their own.
func (a Answer) String() string {
	if a.Text != "" {
		return a.Text
	}
	if len(a.Value) > 0 {
		return string(a.Value)
	}
	return ""
}

// Decoded returns the answer as a plain Go value suitable for a node
// output port: the string for a free-form answer, otherwise the decoded
// JSON (string / float64 / []any / map[string]any). Undecodable JSON
// falls back to the raw string so a node never loses the answer.
func (a Answer) Decoded() any {
	if len(a.Value) == 0 {
		return a.Text
	}
	var v any
	if err := json.Unmarshal(a.Value, &v); err != nil {
		return string(a.Value)
	}
	return v
}

// BatchComplete reports whether every *visible* question in a batch has
// been answered. A question with an unsatisfied DependsOn is not
// visible, so it is not required.
//
// This is the completion rule the wizard transport applies after each
// step. It lives here rather than in the RPC view because it is a
// property of the question shape, not of the dialog.
func BatchComplete(batch []Question, answers map[string]json.RawMessage) bool {
	for _, q := range batch {
		if q.DependsOn != nil {
			prior, ok := answers[q.DependsOn.QuestionID]
			if !ok {
				// Dependency not answered yet — this question is not
				// visible, skip it.
				continue
			}
			if !DependencyMet(q.DependsOn.Condition, prior) {
				continue
			}
		}
		if _, ok := answers[q.ID]; !ok {
			return false
		}
	}
	return true
}

// DependencyMet evaluates a Dependency.Condition against a prior answer.
// Supports three forms:
//
//	"answered"            — any non-null answer satisfies the condition.
//	{"equals": <value>}   — answer must JSON-equal the value.
//	{"includes": <value>} — answer (array) must contain the value.
func DependencyMet(condition json.RawMessage, priorAnswer json.RawMessage) bool {
	answered := len(priorAnswer) > 0 && string(priorAnswer) != "null"
	if len(condition) == 0 {
		return answered
	}
	var s string
	if json.Unmarshal(condition, &s) == nil && s == "answered" {
		return answered
	}
	var eq struct {
		Equals json.RawMessage `json:"equals"`
	}
	if json.Unmarshal(condition, &eq) == nil && len(eq.Equals) > 0 {
		return string(eq.Equals) == string(priorAnswer)
	}
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
