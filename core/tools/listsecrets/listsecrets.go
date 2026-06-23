// Package listsecrets implements the kenaz__list_secrets built-in tool.
//
// list_secrets is the model's only path to discover which @secret:
// references are available in the current session. It NEVER returns
// secret values — only reference tokens, descriptions, kind hints,
// scope hints, and optional budget information.
//
// The tool is default-on (model discovering what it may use is low-risk).
// Its invocations appear in the audit log as ordinary tool.invoked events.
// Cedar-gated by Action::"tool.list_secrets".
//
// Spec mapping: model-secret-references-01KW7M5A FR-005, §2.0.
package listsecrets

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

const (
	// ToolName is the namespaced tool identifier surfaced to the model.
	ToolName = "kenaz__list_secrets"

	// ToolDescription is the model-facing description.
	ToolDescription = "List the secret references this session is permitted to resolve. " +
		"Returns reference tokens, human-readable descriptions, and a kind hint. " +
		"NEVER returns secret values. Use the returned @secret:<locator> tokens in " +
		"tool arguments (e.g. web_fetch headers, bash env vars) to use a secret."
)

const inputSchema = `{
  "type": "object",
  "properties": {
    "filter": {
      "type": "string",
      "description": "Optional substring filter matched against the locator or description."
    }
  }
}`

// ListEntry is one item in the list_secrets response.
// IMPORTANT: this struct MUST NOT have a "value" field or any field
// that could carry plaintext credential bytes (FR-005a).
type ListEntry struct {
	// Ref is the @secret: reference token the model can embed in tool args.
	Ref string `json:"ref"`
	// Description is the human-readable label the user assigned.
	Description string `json:"description"`
	// Kind is the kind hint ("bearer", "basic", "header", "raw", "aws_credential").
	Kind string `json:"kind"`
	// ScopeHint is the scope ("session", "project", "agent", "workflow").
	ScopeHint string `json:"scope_hint"`
	// BudgetRemaining is included only when budget enforcement is active.
	BudgetRemaining *int64 `json:"budget_remaining,omitempty"`
}

// Index is the interface list_secrets depends on.
type Index interface {
	List(ctx context.Context, scope secrets.Scope) []secrets.ExposedEntry
}

// Options configures the Tool.
type Options struct {
	// Index is the ExposureIndex to query. Required.
	Index Index
	// Budget is the session budget. nil = unlimited (no budget_remaining field).
	Budget *refs.Budget
}

// Tool is the list_secrets builtin.
type Tool struct {
	index  Index
	budget *refs.Budget
}

// New constructs a list_secrets Tool.
func New(opts Options) *Tool {
	return &Tool{index: opts.Index, budget: opts.Budget}
}

// Name implements BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(inputSchema)
}

// Call implements BuiltinTool. It lists all exposed secrets for the session,
// optionally filtered by the filter arg. Never returns secret values.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Filter string `json:"filter"`
	}
	if len(argsJSON) > 0 && string(argsJSON) != "null" {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return errorResult("invalid args: " + err.Error()), nil
		}
	}

	entries := t.index.List(ctx, "") // empty scope = all scopes
	out := make([]ListEntry, 0, len(entries))
	for _, e := range entries {
		if args.Filter != "" {
			if !strings.Contains(e.Locator, args.Filter) &&
				!strings.Contains(e.Description, args.Filter) {
				continue
			}
		}
		le := ListEntry{
			Ref:         "@secret:" + e.Locator,
			Description: e.Description,
			Kind:        string(e.KindHint),
			ScopeHint:   string(e.Scope),
		}
		if t.budget != nil {
			rem := t.budget.Remaining(e.Locator)
			le.BudgetRemaining = &rem
		}
		out = append(out, le)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return errorResult("marshal error: " + err.Error()), nil
	}
	return raw, nil
}

func errorResult(msg string) json.RawMessage {
	v, _ := json.Marshal(map[string]string{"error": msg})
	return v
}
