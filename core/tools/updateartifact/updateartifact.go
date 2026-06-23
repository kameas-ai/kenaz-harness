// Package updateartifact implements the kenaz__update_artifact built-in
// tool. It accepts (artifact_id, content[, summary][, path]) from the
// model and writes a NEW row in artifact_versions rather than mutating
// the parent artifact row. The bytes ride into the existing MediaStore
// CAS pipeline so dedup and refcounting work unchanged.
//
// This package mirrors the saveartifact package structure:
// core/tools/saveartifact implements kenaz__save_artifact (create);
// this package implements kenaz__update_artifact (revise).
//
// Default-on by design, parallel to save_artifact: updating a
// deliverable is a low-risk primitive. The Settings dial uses the same
// FSWriteEnabled toggle as the write-family fs builtins so the user can
// gate both from a single panel control.
package updateartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced tool identifier surfaced to the model.
	ToolName = "kenaz__update_artifact"

	// ToolDescription is the user-facing description sent to the model
	// via the tool catalog. Phrased to bias model selection toward this
	// tool when the user asks to update, revise, or edit a previously
	// saved artifact.
	ToolDescription = "Update an existing artifact's content, writing a new version. " +
		"Use this when the user asks you to revise, edit, or update a previously saved artifact. " +
		"Returns the new version number and artifact_id."

	// MaxContentBytes caps the content payload at 10 MiB, matching the
	// save_artifact cap. Above this the call returns error="content_too_large".
	MaxContentBytes = 10 * 1024 * 1024
)

// inputSchema is the JSON Schema for kenaz__update_artifact.
const inputSchema = `{
  "type": "object",
  "properties": {
    "artifact_id": {
      "type": "string",
      "description": "ID of the artifact to update (as returned by kenaz__save_artifact or a prior kenaz__update_artifact call)."
    },
    "content": {
      "type": "string",
      "description": "The new bytes to save as this revision. UTF-8."
    },
    "summary": {
      "type": "string",
      "description": "Optional one-line description of what changed in this revision."
    },
    "path": {
      "type": "string",
      "description": "Optional updated canonical path for file-source artifacts. Omit to keep the original path."
    }
  },
  "required": ["artifact_id", "content"]
}`

// Updater is the narrow surface the tool consumes from *coreart.Manager.
// The interface keeps tests independent of the concrete Manager and its
// MediaStore dependency.
type Updater interface {
	WriteVersion(ctx context.Context, artifactID string, bytes []byte, mimeType string, summary, path *string) (coreart.ArtifactVersion, error)
}

// Options configures a Tool at construction. Updater is required.
//
// Enabled is consulted on every Call as defence-in-depth: the chassis
// EnabledFilter already gates dispatch, but stale tool catalogs can
// still produce calls after the user toggles the dial off. nil Enabled
// is treated as "always enabled".
//
// SessionResolver is consulted to derive the sessionID for logging.
// Default = toolloop.SessionIDFromContext. Tests can pass a fixed resolver.
//
// Logger is optional; nil falls back to slog.Default.
type Options struct {
	Updater         Updater
	Enabled         func() bool
	SessionResolver func(ctx context.Context) string
	Logger          *slog.Logger
}

// Tool implements the kenaz__update_artifact built-in. Safe for
// concurrent use; all state is read-only after construction.
type Tool struct {
	updater  Updater
	enabled  func() bool
	resolver func(ctx context.Context) string
	logger   *slog.Logger
}

// New constructs a Tool. Updater is required; Enabled defaults to
// "always on"; SessionResolver defaults to toolloop.SessionIDFromContext.
func New(opts Options) *Tool {
	if opts.Updater == nil {
		panic("updateartifact.New: nil Updater")
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
		updater:  opts.Updater,
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

// updateArtifactArgs is the wire shape parsed from the model's args.
type updateArtifactArgs struct {
	ArtifactID string `json:"artifact_id"`
	Content    string `json:"content"`
	Summary    string `json:"summary,omitempty"`
	Path       string `json:"path,omitempty"`
}

// successResult is the JSON the model sees when an update succeeds.
type successResult struct {
	ArtifactID string `json:"artifact_id"`
	Version    int    `json:"version"`
	Size       int64  `json:"size"`
	MimeType   string `json:"mime_type"`
}

// errorResult is the JSON the model sees when an update fails.
type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Error kinds returned in errorResult.Error.
const (
	errKindDisabled        = "disabled"
	errKindInvalidArgs     = "invalid_args"
	errKindContentTooLarge = "content_too_large"
	errKindNotFound        = "artifact_not_found"
	errKindUpdateFailed    = "update_failed"
	errKindNoSession       = "no_session"
)

// Call dispatches a single update_artifact invocation: parse args →
// validate → resolve session → WriteVersion → format result.
//
// The Go-error return is reserved for "couldn't even produce a tool
// result" (json.Marshal failure). All expected failure modes return a
// JSON-encoded errorResult with no Go error.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if t == nil {
		return nil, errors.New("updateartifact: nil tool")
	}

	// Defence-in-depth: honour the live enabled state even if the chassis
	// EnabledFilter already gated dispatch.
	if !t.enabled() {
		return marshalErr(errKindDisabled, "update_artifact tool is disabled in Settings")
	}

	if len(argsJSON) == 0 {
		return marshalErr(errKindInvalidArgs, "empty args")
	}
	var args updateArtifactArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("parse args: %v", err))
	}

	args.ArtifactID = strings.TrimSpace(args.ArtifactID)
	if args.ArtifactID == "" {
		return marshalErr(errKindInvalidArgs, "artifact_id is required")
	}
	if args.Content == "" {
		return marshalErr(errKindInvalidArgs, "content is required")
	}
	if len(args.Content) > MaxContentBytes {
		return marshalErr(errKindContentTooLarge, fmt.Sprintf("content exceeds %d bytes", MaxContentBytes))
	}

	sessionID := t.resolver(ctx)
	if sessionID == "" {
		t.logger.Warn("updateartifact.no_session_in_context", "tool", ToolName)
		return marshalErr(errKindNoSession, "no session id in context — tool cannot update")
	}

	var summaryPtr *string
	if s := strings.TrimSpace(args.Summary); s != "" {
		summaryPtr = &s
	}
	var pathPtr *string
	if p := strings.TrimSpace(args.Path); p != "" {
		pathPtr = &p
	}

	ver, err := t.updater.WriteVersion(ctx, args.ArtifactID, []byte(args.Content), "", summaryPtr, pathPtr)
	if err != nil {
		if errors.Is(err, coreart.ErrArtifactNotFound) {
			return marshalErr(errKindNotFound,
				fmt.Sprintf("artifact %q not found", args.ArtifactID))
		}
		t.logger.Warn("updateartifact.write_version_failed",
			"session_id", sessionID,
			"artifact_id", args.ArtifactID,
			"err", err.Error())
		return marshalErr(errKindUpdateFailed, fmt.Sprintf("write version failed: %v", err))
	}

	t.logger.Info("updateartifact.version_written",
		"session_id", sessionID,
		"artifact_id", ver.ArtifactID,
		"version", ver.Version,
		"size", ver.ByteSize,
		"mime_type", ver.MimeType,
	)

	out := successResult{
		ArtifactID: ver.ArtifactID,
		Version:    ver.Version,
		Size:       ver.ByteSize,
		MimeType:   ver.MimeType,
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("updateartifact: marshal success: %w", err)
	}
	return encoded, nil
}

// marshalErr encodes an errorResult as a tool result payload with no
// Go error so the kernel surfaces it to the model as a normal result.
func marshalErr(kind, message string) (json.RawMessage, error) {
	out := errorResult{Error: kind, Message: message}
	return json.Marshal(out)
}
