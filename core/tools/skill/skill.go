// Package skill implements the kaneaz__skill built-in tool.
//
// The model calls this tool to invoke any user-defined slash command that
// has model_invokable=true set in its frontmatter. The tool resolves the
// command via core/slashcmd.Dispatch.Run and returns the rendered output
// as a JSON string.
//
// Tool name: kaneaz__skill (follows the kaneaz__ prefix convention for
// first-party builtins so Cedar policy can gate it uniformly).
//
// Input schema:
//
//	{
//	  "name": "command-name",    // required — bare slash command name
//	  "args": { ... }            // optional — map of input variable values
//	}
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
	"context"
	"encoding/json"
	"fmt"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kaneaz__skill"

	// ToolDescription guides the model on when to call this tool.
	ToolDescription = "Invoke a user-defined skill (slash command) by name. " +
		"Use this when the skills catalog lists a skill that matches the user's request. " +
		"Pass the skill name and any required arguments. " +
		"Returns the rendered output of the skill."

	// MaxNameLen caps the command name to prevent denial-of-service via
	// a pathologically long name that would never match a real command.
	MaxNameLen = 64
)

// inputSchema is the JSON Schema for kaneaz__skill's argument shape.
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
    "session_id": {
      "type": "string",
      "description": "The active session ID. Passed through to the skill for session-scoped resolution."
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
  "required": ["name"]
}`

// Options bundles the Tool's dependencies.
type Options struct {
	// Dispatch is the command-dispatch layer. Required.
	Dispatch *coreslashcmd.Dispatch
	// Enabled returns true when the tool should accept calls. nil means always enabled.
	Enabled func() bool
}

// Tool implements the kaneaz__skill builtin.
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
type input struct {
	Name      string            `json:"name"`
	Args      map[string]string `json:"args"`
	SessionID string            `json:"session_id"`
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
	if err := json.Unmarshal(args, &in); err != nil {
		return marshalErr(fmt.Sprintf("skill tool: invalid arguments: %v", err))
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
		SessionID: in.SessionID,
		ProjectID: in.ProjectID,
		CWD:       in.CWD,
	}

	result, err := t.opts.Dispatch.Run(ctx, in.Name, in.Args, sc)
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

func marshalErr(msg string) (json.RawMessage, error) {
	data, _ := json.Marshal(errOutput{IsError: true, Error: msg})
	return data, nil
}
