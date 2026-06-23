// Package saveartifact implements the kenaz__save_artifact built-in
// tool. It accepts (title, content[, mime_type]) from the model and
// pipes the content directly into the existing artifact CAS pipeline:
// MediaStore.Put for content-hash-keyed storage, then an artifacts row
// pointing at the same content_hash. No filesystem touchpoint.
//
// This sits alongside core/tools/websearch and core/tools/bash; the
// chassis registers all three through core/rpc/builtins_wiring.go and
// the toolloop's BuiltinPool dispatches by namespaced "kenaz__" name.
//
// Default-on by design (mirrors the user-supplied design intent):
// saving deliverables is a low-risk primitive — the cost of an unwanted
// save is "user deletes one artifact row"; the cost of default-off is
// "model can't save without setup." The Settings dial uses an inverted
// `SaveArtifactDisabled bool` field so a fresh install (zero-value)
// matches "tool enabled".
package saveartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"path"
	"strings"
	"unicode/utf8"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced tool identifier surfaced to the
	// model. The "kenaz__" prefix is reserved for built-ins so the
	// dispatcher can route by prefix without a registry lookup.
	ToolName = "kenaz__save_artifact"

	// ToolDescription is the user-facing description sent to the
	// model via the tool catalog. Phrased to bias model selection
	// toward this tool when the user asks to save / export / produce
	// a document, vs. dropping fenced code in chat or going to MCP
	// filesystem servers.
	ToolDescription = "Save a named deliverable to the session's artifacts. " +
		"Use this when the user asks you to save, export, or produce a document or file. " +
		"Returns an artifact ID the user can find in the Artifacts tab."

	// MaxTitleRunes is the upper bound on title length. The Artifacts
	// tab renders titles inline; an unbounded title would break the
	// layout. 200 runes covers any sensible deliverable filename.
	MaxTitleRunes = 200

	// MaxContentBytes caps the content payload at 10 MiB. Above this,
	// the call returns IsError with error="content_too_large". The
	// cap protects the chassis from an LLM-induced memory blow-up;
	// 10 MiB is well above any expected single deliverable size.
	MaxContentBytes = 10 * 1024 * 1024

	// DefaultMimeType is the fallback mime when neither an explicit
	// mime_type arg nor the title's extension yields one.
	DefaultMimeType = "text/plain; charset=utf-8"
)

// inputSchema is the JSON Schema describing kenaz__save_artifact's
// argument shape. Inlined as a constant so InputSchema() returns the
// same json.RawMessage on every call.
const inputSchema = `{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "Display name for the artifact (used as the basename for mime detection if it looks like a path)."
    },
    "content": {
      "type": "string",
      "description": "The bytes to save. UTF-8."
    },
    "mime_type": {
      "type": "string",
      "description": "Optional explicit mime type. Falls back to extension-detection on title, then text/plain."
    }
  },
  "required": ["title", "content"]
}`

// Manager is the slice of *coreart.Manager the tool consumes. The
// interface (not the concrete pointer) lets tests substitute a
// recording fake without spinning up a MediaStore.
type Manager interface {
	Capture(ctx context.Context, candidates []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error)
}

// Options configures a Tool at construction. Manager is required.
//
// Enabled is consulted on every Call as a defence-in-depth hatch:
// the chassis already gates dispatch via toolloop.EnabledFilter, but
// stale tool catalogs in the model's context can still produce calls
// after the user toggles the dial off mid-session. nil Enabled is
// treated as "always enabled".
//
// SessionResolver is consulted on every Call to derive the sessionID
// passed to Manager.Capture. Default = toolloop.SessionIDFromContext,
// which works in production where kernelToolAdapter stuffs the
// session ID into the context just before pool.Call. Tests can pass
// a fixed-value resolver.
//
// Logger is optional; nil falls back to slog.Default.
type Options struct {
	Manager         Manager
	Enabled         func() bool
	SessionResolver func(ctx context.Context) string
	Logger          *slog.Logger
}

// Tool implements the kenaz__save_artifact built-in. Safe for
// concurrent use; all state is read-only after construction.
type Tool struct {
	mgr      Manager
	enabled  func() bool
	resolver func(ctx context.Context) string
	logger   *slog.Logger
}

// New constructs a Tool. Manager is required; Enabled defaults to
// "always on"; SessionResolver defaults to toolloop.SessionIDFromContext.
func New(opts Options) *Tool {
	if opts.Manager == nil {
		panic("saveartifact.New: nil Manager")
	}
	enabled := opts.Enabled
	if enabled == nil {
		enabled = func() bool { return true }
	}
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Tool{
		mgr:      opts.Manager,
		enabled:  enabled,
		resolver: resolver,
		logger:   logger,
	}
}

// Name returns the namespaced tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the user-facing tool description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON Schema for the tool's args.
func (t *Tool) InputSchema() json.RawMessage {
	return json.RawMessage(inputSchema)
}

// saveArtifactArgs is the wire shape parsed from the model's args.
type saveArtifactArgs struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	MimeType string `json:"mime_type,omitempty"`
}

// successResult is the JSON the model sees when a save succeeds. The
// model uses these fields in its next turn ("I saved Q4-summary.md, see
// the Artifacts tab — id <ulid>").
type successResult struct {
	ArtifactID string `json:"artifact_id"`
	Title      string `json:"title"`
	Size       int64  `json:"size"`
	MimeType   string `json:"mime_type"`
}

// errorResult is the JSON the model sees when a save fails. Structured
// so a model conditioned on tool-result shapes can recover (e.g. shrink
// content on content_too_large, ask the user on disabled).
type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Error kinds — the values returned in errorResult.Error.
const (
	errKindDisabled        = "disabled"
	errKindInvalidArgs     = "invalid_args"
	errKindContentTooLarge = "content_too_large"
	errKindCaptureFailed   = "capture_failed"
	errKindNoSession       = "no_session"
)

// Call dispatches a single save_artifact invocation: parse args →
// validate → resolve mime → resolve session → Capture → format result.
//
// The Go-error return is reserved for "couldn't even produce a tool
// result" (json.Marshal failure on the success/error envelope). All
// expected failure modes (validation, oversize, capture failure,
// disabled) return a JSON-encoded errorResult with no Go error so the
// kernel can surface the failure to the model verbatim.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if t == nil {
		return nil, errors.New("saveartifact: nil tool")
	}

	// Defence-in-depth: the chassis EnabledFilter already gates
	// dispatch, but a stale model-side tool catalog can still fire a
	// call after the user toggles the dial off. Honour the live state.
	if !t.enabled() {
		return marshalErr(errKindDisabled, "save_artifact tool is disabled in Settings")
	}

	if len(argsJSON) == 0 {
		return marshalErr(errKindInvalidArgs, "empty args")
	}
	var args saveArtifactArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("parse args: %v", err))
	}

	args.Title = strings.TrimSpace(args.Title)
	if args.Title == "" {
		return marshalErr(errKindInvalidArgs, "title is required")
	}
	if utf8.RuneCountInString(args.Title) > MaxTitleRunes {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("title exceeds %d runes", MaxTitleRunes))
	}
	if args.Content == "" {
		return marshalErr(errKindInvalidArgs, "content is required")
	}
	if len(args.Content) > MaxContentBytes {
		return marshalErr(errKindContentTooLarge, fmt.Sprintf("content exceeds %d bytes", MaxContentBytes))
	}

	mimeType := resolveMime(args.Title, args.MimeType)

	sessionID := t.resolver(ctx)
	if sessionID == "" {
		// No session in context — the chassis didn't plumb one. This
		// is a wiring bug, not a model error; surface as a typed
		// failure rather than panicking.
		t.logger.Warn("saveartifact.no_session_in_context", "tool", ToolName)
		return marshalErr(errKindNoSession, "no session id in context — tool cannot capture")
	}

	candidates := []coreart.CaptureCandidate{{
		Title:    args.Title,
		MimeType: mimeType,
		Bytes:    []byte(args.Content),
		Source:   coreart.SourceToolOutput,
		SourceRef: coreart.ArtifactSourceRef{
			MessageID: "tool:" + ToolName,
		},
	}}
	captured, err := t.mgr.Capture(ctx, candidates, sessionID)
	if err != nil {
		t.logger.Warn("saveartifact.capture_failed",
			"session_id", sessionID,
			"title", args.Title,
			"err", err.Error())
		return marshalErr(errKindCaptureFailed, fmt.Sprintf("capture failed: %v", err))
	}
	if len(captured) == 0 {
		return marshalErr(errKindCaptureFailed, "manager returned zero artifacts")
	}

	a := captured[0]
	t.logger.Info("saveartifact.captured",
		"session_id", sessionID,
		"artifact_id", a.ID,
		"title", a.Title,
		"size", a.ByteSize,
		"mime_type", a.MimeType,
	)

	out := successResult{
		ArtifactID: a.ID,
		Title:      a.Title,
		Size:       a.ByteSize,
		MimeType:   a.MimeType,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		// json.Marshal failure on a struct with only string + int64
		// fields shouldn't happen in practice; bubble it up so the
		// kernel can surface a generic dispatch failure.
		return nil, fmt.Errorf("saveartifact: marshal success: %w", err)
	}
	return encoded, nil
}

// resolveMime applies the precedence: explicit arg > extension on
// title > DefaultMimeType.
func resolveMime(title, explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit
	}
	if ext := path.Ext(title); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return DefaultMimeType
}

// marshalErr is the JSON-encoded errorResult helper. Returns the
// payload as a json.RawMessage tool result with no Go error so the
// kernel surfaces the failure to the model as a normal (IsError) tool
// result rather than aborting the run.
//
// The kernel adapter's Call wraps this raw payload into a ToolResult
// with IsError=false (the call itself didn't fail at the dispatcher);
// the model sees the structured error in its next turn and can
// recover. Tests can switch on the error field directly.
func marshalErr(kind, message string) (json.RawMessage, error) {
	out := errorResult{Error: kind, Message: message}
	return json.Marshal(out)
}
