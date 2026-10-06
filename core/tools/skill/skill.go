// Package skill implements the kenaz__skill built-in tool.
//
// The model calls this tool to invoke any user-defined slash command that
// has model_invokable=true set in its frontmatter. The tool resolves the
// command via core/slashcmd.Dispatch.RunModelInvoked, which refuses
// commands the user did not mark model_invokable (trust-surfaces-that-
// fire-01PMZ202 WP20) and returns the rendered output as a JSON string.
//
// Tool name: kenaz__skill (follows the kenaz__ prefix convention for
// first-party builtins so Cedar policy can gate it uniformly).
//
// Input schema:
//
//	{
//	  "name": "command-name",    // required — bare slash command name
//	  "args": { ... }            // optional — map of input variable values
//	}
//
// The session is NEVER an argument (model-harness-toolset-01MHTS001 WP02
// security review, H1). It is taken exclusively from the dispatch context
// (toolloop.SessionIDFromContext, stamped by the kernel tool adapter). The
// schema used to accept a model-supplied "session_id" that was then
// stamped onto the slash dispatch ctx, overriding the real session: a
// forged or foreign id escaped the session's tool-permission resolution,
// including a scheduled run's allowlist containment. Unknown fields —
// session_id included — are now refused with invalid_args, never silently
// ignored, so a caller still sending one surfaces.
//
// Output schema (success):
//
//	{ "output": "<rendered text>", "kind": "info" }
//
// Output schema (error):
//
//	{ "isError": true, "error": "<message>" }
//
// model-invoked-skills-catalog-01KZNP3E WP02.
package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kenaz__skill"

	// ToolDescription guides the model on when to call this tool.
	ToolDescription = "Invoke a user-defined skill (slash command) by name. " +
		"Use this when the skills catalog lists a skill that matches the user's request. " +
		"Pass the skill name and any required arguments. " +
		"Returns the rendered output of the skill."

	// MaxNameLen caps the command name to prevent denial-of-service via
	// a pathologically long name that would never match a real command.
	MaxNameLen = 64
)

// inputSchema is the JSON Schema for kenaz__skill's argument shape.
const inputSchema = `{
  "type": "object",
  "properties": {
    "name": {
      "type": "string",
      "description": "The bare skill name (without the leading slash), e.g. \"summarize\"."
    },
    "args": {
      "type": "object",
      "description": "Named argument values required by the skill. Keys match the skill's declared input names.",
      "additionalProperties": { "type": "string" }
    },
    "project_id": {
      "type": "string",
      "description": "The active project ID. Optional; used for project-scoped command lookup."
    },
    "cwd": {
      "type": "string",
      "description": "Current working directory. Optional; used as the {{cwd}} template variable."
    }
  },
  "required": ["name"],
  "additionalProperties": false
}`

// Options bundles the Tool's dependencies.
type Options struct {
	// Dispatch is the command-dispatch layer. Required.
	Dispatch *coreslashcmd.Dispatch
	// Enabled returns true when the tool should accept calls. nil means always enabled.
	Enabled func() bool
}

// Tool implements the kenaz__skill builtin.
type Tool struct {
	opts Options
}

// New constructs a Tool.
func New(opts Options) *Tool {
	return &Tool{opts: opts}
}

// Name implements toolloop.BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements toolloop.BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements toolloop.BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// input is the parsed JSON shape the model passes to this tool.
// It deliberately has no session field: see the package doc (H1).
type input struct {
	Name      string            `json:"name"`
	Args      map[string]string `json:"args"`
	ProjectID string            `json:"project_id"`
	CWD       string            `json:"cwd"`
}

// output is the successful response shape.
type output struct {
	Output string `json:"output"`
	Kind   string `json:"kind"`
}

// errOutput is the error response shape.
type errOutput struct {
	IsError bool   `json:"isError"`
	Error   string `json:"error"`
	// Kind is the closed error vocabulary ("invalid_args", "no_session");
	// empty for the pre-existing free-text failures.
	Kind string `json:"kind,omitempty"`
}

// Call implements toolloop.BuiltinTool.
func (t *Tool) Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if t.opts.Enabled != nil && !t.opts.Enabled() {
		return marshalErr("skill tool is disabled")
	}
	if t.opts.Dispatch == nil {
		return marshalErr("skill tool: dispatch not configured")
	}

	var in input
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return marshalKindErr("invalid_args", fmt.Sprintf("skill tool: invalid arguments: %v (the session is taken from the conversation, never from an argument)", err))
	}
	sessionID := toolloop.SessionIDFromContext(ctx)
	if sessionID == "" {
		return marshalKindErr("no_session", "skill tool requires an active session context")
	}
	if in.Name == "" {
		return marshalErr("skill tool: 'name' argument is required")
	}
	if len(in.Name) > MaxNameLen {
		return marshalErr(fmt.Sprintf("skill tool: name exceeds %d characters", MaxNameLen))
	}
	if in.Args == nil {
		in.Args = map[string]string{}
	}

	sc := coreslashcmd.SessionContext{
		SessionID: sessionID,
		ProjectID: in.ProjectID,
		CWD:       in.CWD,
	}

	result, err := t.opts.Dispatch.RunModelInvoked(ctx, in.Name, in.Args, sc)
	if err != nil {
		return marshalErr(fmt.Sprintf("skill %q failed: %v", in.Name, err))
	}

	out := output{
		Output: result.Text,
		Kind:   result.Kind,
	}
	data, jsonErr := json.Marshal(out)
	if jsonErr != nil {
		return marshalErr(fmt.Sprintf("skill tool: marshal output: %v", jsonErr))
	}
	return data, nil
}

func marshalKindErr(kind, msg string) (json.RawMessage, error) {
	data, _ := json.Marshal(errOutput{IsError: true, Error: msg, Kind: kind})
	return data, nil
}

func marshalErr(msg string) (json.RawMessage, error) {
	data, _ := json.Marshal(errOutput{IsError: true, Error: msg})
	return data, nil
}
