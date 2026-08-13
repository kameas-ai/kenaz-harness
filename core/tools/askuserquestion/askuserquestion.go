// Package askuserquestion implements the kenaz__ask_user_question
// built-in tool (mission ask-user-question-interactive-01KZNP3G).
//
// The tool lets the model pose a structured question to the user via a
// rich Vue 3 dialog. It supports seven question kinds:
//
//   - radio      — single choice from a list of labelled options
//   - checkbox   — multi-select from a list of labelled options
//   - text       — freeform string (single-line or multiline)
//   - number     — numeric input with optional min / max / step
//   - slider     — range slider with min / max / step
//   - date       — date-only picker (YYYY-MM-DD)
//   - file       — OS file-picker (returns path)
//
// The tool follows the same "kenaz__" prefix + BuiltinTool interface
// contract as save_artifact and bash.
//
// # What this package is after 01PMGX01 WP06
//
// A shim, and deliberately nothing more. The model-facing name and JSON
// schema are a frozen contract, so they stay here; everything behind
// them — the question shape, the validation rules, the pending-ask
// store, the resolve leg — moved to core/elicitation, which the `ask`
// graph node rides too (spec §4.3, invariant I5). This file owns the
// translation from the model's args JSON to elicitation.Question and
// back from elicitation.Answer to the tool result. It owns no
// elicitation semantics of its own.
package askuserquestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
)

// ToolName is the namespaced tool identifier surfaced to the model.
// The "kenaz__" prefix is reserved for built-ins.
const ToolName = "kenaz__ask_user_question"

// ToolDescription is the user-facing description sent to the model.
// Phrased to bias model selection toward this tool when the model
// needs structured user input rather than injecting a free-text
// question into the chat turn.
const ToolDescription = "Pause the current turn and ask the user a structured question via an interactive dialog. " +
	"Supports radio, checkbox, text, number, slider, date, and file-picker question kinds. " +
	"Returns the user's answer so you can continue the task. " +
	"Use this instead of asking a question in chat when you need a specific, validated answer."

// QuestionKind is the closed enum of supported question types. It is an
// alias for elicitation.Kind: the seven kinds are one enum shared with
// the `ask` node, not a parallel copy (01PMGX01 WP06).
type QuestionKind = elicitation.Kind

// The seven model-facing question kinds. Aliases of the canonical
// constants so existing call sites keep compiling.
const (
	KindRadio    = elicitation.KindRadio
	KindCheckbox = elicitation.KindCheckbox
	KindText     = elicitation.KindText
	KindNumber   = elicitation.KindNumber
	KindSlider   = elicitation.KindSlider
	KindDate     = elicitation.KindDate
	KindFile     = elicitation.KindFile
)

// inputSchema is the JSON Schema the model receives in the tool catalog.
// Kept as a constant so InputSchema() is allocation-free.
const inputSchema = `{
  "type": "object",
  "properties": {
    "question": {
      "type": "string",
      "description": "The question to display to the user. Markdown supported."
    },
    "kind": {
      "type": "string",
      "enum": ["radio", "checkbox", "text", "number", "slider", "date", "file"],
      "description": "The type of input control to render."
    },
    "options": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "value": { "type": "string" },
          "label": { "type": "string" }
        },
        "required": ["value", "label"]
      },
      "description": "Required for 'radio' and 'checkbox' kinds. List of selectable options."
    },
    "placeholder": {
      "type": "string",
      "description": "Optional placeholder text for 'text' kind."
    },
    "min": {
      "type": "number",
      "description": "Minimum value for 'number' and 'slider' kinds."
    },
    "max": {
      "type": "number",
      "description": "Maximum value for 'number' and 'slider' kinds."
    },
    "step": {
      "type": "number",
      "description": "Step increment for 'number' and 'slider' kinds."
    },
    "default_value": {
      "description": "Pre-populated default. Type depends on 'kind': string for text/date/radio/file, number for number/slider, array of strings for checkbox."
    },
    "preview": {
      "type": "object",
      "description": "Optional side-by-side preview pane content.",
      "properties": {
        "kind": {
          "type": "string",
          "enum": ["markdown", "code", "image", "diff", "table", "html", "vue-component", "plain"],
          "description": "Preview renderer to use."
        },
        "content": {
          "type": "string",
          "description": "Content to render in the preview pane."
        },
        "language": {
          "type": "string",
          "description": "Language hint for 'code' and 'diff' renderers (e.g. 'typescript', 'python')."
        }
      },
      "required": ["kind", "content"]
    }
  },
  "required": ["question", "kind"]
}`

// QuestionOption is a single selectable entry for radio / checkbox kinds.
type QuestionOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PreviewSpec describes the optional side-by-side preview content.
type PreviewSpec struct {
	Kind     string `json:"kind"`
	Content  string `json:"content"`
	Language string `json:"language,omitempty"`
}

// AskArgs is the wire shape parsed from the model's args JSON.
type AskArgs struct {
	Question     string           `json:"question"`
	Kind         QuestionKind     `json:"kind"`
	Options      []QuestionOption `json:"options,omitempty"`
	Placeholder  string           `json:"placeholder,omitempty"`
	Min          *float64         `json:"min,omitempty"`
	Max          *float64         `json:"max,omitempty"`
	Step         *float64         `json:"step,omitempty"`
	DefaultValue json.RawMessage  `json:"default_value,omitempty"`
	Preview      *PreviewSpec     `json:"preview,omitempty"`
}

// ToQuestion projects the model's args onto the canonical question
// shape. This is the only place the tool's wire vocabulary meets the
// harness's.
func (a AskArgs) ToQuestion() elicitation.Question {
	q := elicitation.Question{
		Text:         a.Question,
		Kind:         a.Kind,
		Placeholder:  a.Placeholder,
		Min:          a.Min,
		Max:          a.Max,
		Step:         a.Step,
		DefaultValue: a.DefaultValue,
	}
	if len(a.Options) > 0 {
		q.Options = make([]elicitation.Option, 0, len(a.Options))
		for _, o := range a.Options {
			q.Options = append(q.Options, elicitation.Option{Value: o.Value, Label: o.Label})
		}
	}
	if a.Preview != nil {
		q.Preview = &elicitation.Preview{
			Kind:     a.Preview.Kind,
			Content:  a.Preview.Content,
			Language: a.Preview.Language,
		}
	}
	return q
}

// AskResult is returned to the model on a successful elicitation.
type AskResult struct {
	// Answer holds the user's response. Its concrete JSON type mirrors
	// the question kind: string for text/date/radio/file, number for
	// number/slider, array of strings for checkbox.
	Answer json.RawMessage `json:"answer"`
	// Cancelled is true when the user dismissed the dialog without
	// answering. Answer is null in that case.
	Cancelled bool `json:"cancelled"`
}

// errorResult is the JSON the model receives when the tool call fails.
type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Error kinds for errorResult.Error.
const (
	errKindNotWired    = "not_wired"
	errKindInvalidArgs = "invalid_args"
	errKindDelegate    = "delegate_error"
)

// Delegate is the narrow interface the RPC layer implements to open the
// dialog and wait for a user answer. The bridge pattern keeps the tool
// package free of Wails / RPC dependencies, which simplifies unit tests.
//
// OpenDialog blocks until the user submits or cancels. The returned
// Answer.Cancelled flag is true when the user dismissed without
// answering.
//
// It speaks elicitation.Question / elicitation.Answer rather than this
// package's wire types: the concrete implementation
// (core/rpc/views/elicit) parks the question in the shared
// elicitation.Registry, which is the same store the `ask` node's durable
// pause uses (01PMGX01 WP06). A nil Delegate makes every Call return
// errKindNotWired.
type Delegate interface {
	OpenDialog(ctx context.Context, q elicitation.Question) (elicitation.Answer, error)
}

// Options configures the Tool at construction.
//
// Delegate is optional at WP01; the tool is registered into the
// BuiltinRegistry immediately. When Delegate is nil every Call returns
// errKindNotWired so the model knows the feature is not yet wired.
//
// Enabled, if non-nil, is consulted on every Call. nil = always enabled.
//
// Logger is optional; nil falls back to slog.Default.
type Options struct {
	Delegate Delegate
	Enabled  func() bool
	Logger   *slog.Logger
}

// Tool implements the kenaz__ask_user_question builtin.
// Safe for concurrent use.
type Tool struct {
	delegate Delegate
	enabled  func() bool
	logger   *slog.Logger
}

// New constructs a Tool. All Options fields are optional at WP01.
func New(opts Options) *Tool {
	enabled := opts.Enabled
	if enabled == nil {
		enabled = func() bool { return true }
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Tool{
		delegate: opts.Delegate,
		enabled:  enabled,
		logger:   logger,
	}
}

// Name returns the namespaced tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the user-facing tool description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON Schema for the tool's args.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// Call implements BuiltinTool. It parses + validates the model's args,
// then delegates to the RPC bridge to open the dialog and await a reply.
//
// The Go-error return is reserved for "couldn't even produce a tool
// result". All expected failure modes (not wired, invalid args,
// delegate error) return a JSON-encoded errorResult so the model can
// recover.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if !t.enabled() {
		return marshalErr(errKindNotWired, "ask_user_question tool is disabled")
	}

	if len(argsJSON) == 0 {
		return marshalErr(errKindInvalidArgs, "empty args")
	}
	var args AskArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("parse args: %v", err))
	}

	// Validate through the shared elicitation rules. RequireStructured
	// (rather than plain Validate) is what makes `kind` mandatory here:
	// the tool's JSON schema declares it required, while a graph-authored
	// `ask` node may legitimately be free-form.
	question := args.ToQuestion()
	if err := question.RequireStructured(); err != nil {
		return marshalErr(errKindInvalidArgs, err.Error())
	}

	// Delegate gate.
	if t.delegate == nil {
		t.logger.Warn("askuserquestion.not_wired",
			"tool", ToolName,
			"kind", args.Kind,
		)
		return marshalErr(errKindNotWired,
			"ask_user_question dialog bridge is not wired; call will return once WP04 lands")
	}

	answer, err := t.delegate.OpenDialog(ctx, question)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Context cancellation: surface as cancelled result rather
			// than a hard error so the model sees Cancelled=true and
			// can continue.
			r := AskResult{Cancelled: true, Answer: json.RawMessage("null")}
			return json.Marshal(r)
		}
		t.logger.Warn("askuserquestion.delegate_error",
			"tool", ToolName,
			"kind", args.Kind,
			"err", err.Error(),
		)
		return marshalErr(errKindDelegate, fmt.Sprintf("dialog error: %v", err))
	}

	encoded, err := json.Marshal(resultOf(answer))
	if err != nil {
		return nil, fmt.Errorf("askuserquestion: marshal result: %w", err)
	}
	return encoded, nil
}

// resultOf projects a resolved elicitation.Answer onto the frozen
// model-facing result shape. A cancelled or empty answer renders as JSON
// null so the model always receives a well-typed `answer` field.
func resultOf(a elicitation.Answer) AskResult {
	out := AskResult{Cancelled: a.Cancelled, Answer: a.Value}
	if len(out.Answer) == 0 {
		if a.Text != "" && !a.Cancelled {
			if encoded, err := json.Marshal(a.Text); err == nil {
				out.Answer = encoded
				return out
			}
		}
		out.Answer = json.RawMessage("null")
	}
	return out
}

// marshalErr returns a JSON-encoded errorResult tool result (no Go error).
func marshalErr(kind, message string) (json.RawMessage, error) {
	out := errorResult{Error: kind, Message: message}
	return json.Marshal(out)
}
