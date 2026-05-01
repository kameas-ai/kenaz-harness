// Package fsrequest implements kaneaz__request_filesystem_access, the
// built-in tool that lets the model ask the user to expand the
// filesystem MCP recipe's allowed_directories list at runtime.
//
// The tool fires the Cedar interactive prompt flow (fs:permission-pending,
// Op "recipe_dir_add") and – on approval – re-spawns the MCP server with
// the expanded allowed_directories list. On AllowAlways it also persists a
// Cedar snippet so future sessions inherit the grant without re-prompting.
//
// The model receives { granted, expanded_path, message } so it knows whether
// to retry its original filesystem operation.
package fsrequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kaneaz__request_filesystem_access"

	// ToolDescription tells the model when to use this tool.
	ToolDescription = "Request that a specific local directory be added to the " +
		"filesystem server's allowed list. Call this when you need to read or write " +
		"a path the filesystem server cannot currently reach. The user will be " +
		"prompted for approval. Returns whether the request was granted and the " +
		"canonicalised path to use in follow-up calls."
)

const inputSchema = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Absolute or tilde-expanded path to the directory you need access to."
    },
    "reason": {
      "type": "string",
      "description": "One or two sentences explaining why you need access to this directory."
    }
  },
  "required": ["path", "reason"]
}`

// RequestAdditionalAllowedDir is the backend method the tool delegates to.
// It is a narrow interface so the tool can be unit-tested without a real
// Tools view implementation.
type RequestAdditionalAllowedDir interface {
	RequestAdditionalAllowedDir(ctx context.Context, recipeID, path, reason string) (granted bool, expanded string, err error)
}

// Tool implements kaneaz__request_filesystem_access.
// It is safe for concurrent use; all mutable state lives in the delegate.
type Tool struct {
	delegate RequestAdditionalAllowedDir
	recipeID string
}

// Options configures the tool at construction time.
type Options struct {
	// Delegate is the backend that handles the actual prompt + restart logic.
	// Required.
	Delegate RequestAdditionalAllowedDir
	// RecipeID is the filesystem MCP recipe to expand. Defaults to
	// "filesystem" (v1 only supports the one shipped recipe).
	RecipeID string
}

// New constructs the tool. Panics if Delegate is nil (programming error).
func New(opts Options) *Tool {
	if opts.Delegate == nil {
		panic("fsrequest.New: nil Delegate")
	}
	id := opts.RecipeID
	if id == "" {
		id = "filesystem"
	}
	return &Tool{delegate: opts.Delegate, recipeID: id}
}

// Name returns the namespaced tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the user-facing description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON schema for the tool's input.
func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(inputSchema)
}

// input is the wire shape the model provides.
type input struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// output is the wire shape returned to the model.
type output struct {
	Granted      bool   `json:"granted"`
	ExpandedPath string `json:"expanded_path"`
	Message      string `json:"message"`
}

// errOutput is a structured tool-result for call-level errors that the
// model can inspect. These are returned as non-error json.RawMessage values
// so the kernel surfaces them verbatim (IsError = false).
type errOutput struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Call dispatches a single request_filesystem_access invocation.
//
// Go-error return is reserved for "couldn't even serialise a tool result";
// all expected failure modes (bad args, deny, restart failure) are encoded
// as JSON in the returned payload.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if t == nil {
		return marshalErr("tool_nil", "fsrequest tool is nil")
	}
	if len(argsJSON) == 0 {
		return marshalErr("invalid_args", "empty args")
	}
	var args input
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalErr("invalid_args", fmt.Sprintf("parse args: %v", err))
	}
	if args.Path == "" {
		return marshalErr("invalid_args", "path is required")
	}
	if args.Reason == "" {
		return marshalErr("invalid_args", "reason is required")
	}

	granted, expanded, err := t.delegate.RequestAdditionalAllowedDir(ctx, t.recipeID, args.Path, args.Reason)
	if err != nil {
		// Distinguish hard errors (deny-list rejection) from soft ones (restart
		// failed) — both become a non-granted result the model can log.
		return marshalOutput(output{
			Granted:      false,
			ExpandedPath: expanded,
			Message:      err.Error(),
		})
	}
	if !granted {
		return marshalOutput(output{
			Granted:      false,
			ExpandedPath: expanded,
			Message:      "access denied or timed out",
		})
	}
	return marshalOutput(output{
		Granted:      true,
		ExpandedPath: expanded,
		Message:      fmt.Sprintf("access granted to %s; retry your original operation using this path", expanded),
	})
}

func marshalOutput(out output) (json.RawMessage, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("fsrequest: marshal output: %w", err)
	}
	return b, nil
}

func marshalErr(kind, message string) (json.RawMessage, error) {
	b, err := json.Marshal(errOutput{Error: kind, Message: message})
	if err != nil {
		return nil, errors.New("fsrequest: marshal error result failed: " + message)
	}
	return b, nil
}
