package agentgraph

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State-primitive executors (FR-051 .. FR-058): Memory, CorpusRead,
// CorpusWrite, Attachment, HistoryRead, TraceWrite, Checkpoint, Artifact.

// ---- MemoryNode ----

type memoryExecutor struct{}

func (memoryExecutor) Kind() NodeKind { return NodeKindMemory }

func (memoryExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(MemoryAttrs)
	if !ok {
		return res, fmt.Errorf("memory: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	scope := a.Scope
	if scope == "" {
		scope = "session"
	}
	switch a.Mode {
	case "read":
		filter := MemoryReadFilter{
			Scopes: []string{scope},
			Query:  a.Query,
			TopK:   a.TopK,
		}
		hits, err := env.Memory.Read(ctx, filter)
		if err != nil {
			return res, fmt.Errorf("memory: node %q read: %w", node.ID, err)
		}
		res.Outputs["out"] = hits
		res.Outputs["count"] = len(hits)
		_ = res.Events.AppendKind(env.RunID, node.ID, EventMemoryRead, map[string]any{
			"scope": scope, "query_len": len(a.Query), "top_k": a.TopK, "hits": len(hits),
		})
	case "write", "upsert":
		content := a.Content
		if content == "" {
			if v, ok := inputs.GetString("content"); ok {
				content = v
			}
		}
		if content == "" {
			return res, fmt.Errorf("memory: node %q: write/upsert requires content", node.ID)
		}
		w := MemoryWrite{
			Scope:     scope,
			ScopeID:   resolveScopeID(scope, env.ProjectID, env.SessionID),
			SessionID: env.SessionID,
			ProjectID: env.ProjectID,
			Content:   content,
			Title:     "memory_node:" + node.ID,
			Source:    "MemoryNode",
			Pinned:    a.Pin,
		}
		id, deduped, err := env.Memory.Write(ctx, w)
		if err != nil {
			return res, fmt.Errorf("memory: node %q write: %w", node.ID, err)
		}
		// wiring:deferred(no reader found for the written chunk's id — deduped (below) is the only field a downstream node/YAML condition currently keys off; surfaced by check-output-ports.sh, wiring-integrity-01PMAG04 WP05)
		res.Outputs["chunk_id"] = id
		res.Outputs["deduped"] = deduped
		_ = res.Events.AppendKind(env.RunID, node.ID, EventMemoryWrite, map[string]any{
			"scope": scope, "deduped": deduped, "id": id,
		})
	default:
		return res, fmt.Errorf("memory: node %q: unknown mode %q", node.ID, a.Mode)
	}
	return res, nil
}

// ---- CorpusReadNode (stub when Corpus seam unwired) ----

type corpusReadExecutor struct{}

func (corpusReadExecutor) Kind() NodeKind { return NodeKindCorpusRead }

func (corpusReadExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(CorpusReadAttrs)
	if !ok {
		return res, fmt.Errorf("corpus_read: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if env.Corpus == nil {
		return res, ErrNotImplemented
	}
	query := a.Query
	if v, ok := inputs.GetString("query"); ok && query == "" {
		query = v
	}
	hits, err := env.Corpus.Search(ctx, a.CorpusIds, query, a.TopK)
	if err != nil {
		if errors.Is(err, ErrNoCorpus) || errors.Is(err, ErrNotImplemented) {
			res.Outputs["hits"] = []CorpusHit{}
			res.Outputs["count"] = 0
			return res, nil
		}
		return res, fmt.Errorf("corpus_read: node %q: %w", node.ID, err)
	}
	res.Outputs["hits"] = hits
	res.Outputs["count"] = len(hits)
	return res, nil
}

// ---- CorpusWriteNode (stub when Corpus seam unwired) ----

type corpusWriteExecutor struct{}

func (corpusWriteExecutor) Kind() NodeKind { return NodeKindCorpusWrite }

func (corpusWriteExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(CorpusWriteAttrs)
	if !ok {
		return res, fmt.Errorf("corpus_write: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if env.Corpus == nil {
		return res, ErrNotImplemented
	}
	job, err := env.Corpus.Enqueue(ctx, a.CorpusId, a.SourcePath)
	if err != nil {
		if errors.Is(err, ErrNoCorpus) || errors.Is(err, ErrNotImplemented) {
			res.Outputs["job_handle"] = ""
			return res, nil
		}
		return res, fmt.Errorf("corpus_write: node %q: %w", node.ID, err)
	}
	res.Outputs["job_handle"] = job
	_ = inputs
	return res, nil
}

// ---- AttachmentNode ----

type attachmentExecutor struct{}

func (attachmentExecutor) Kind() NodeKind { return NodeKindAttachment }

func (attachmentExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(AttachmentAttrs)
	if !ok {
		return res, fmt.Errorf("attachment: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if env.Attachments == nil {
		return res, ErrNotImplemented
	}
	block, err := env.Attachments.Resolve(ctx, a.AttachmentId)
	if err != nil {
		return res, fmt.Errorf("attachment: node %q: %w", node.ID, err)
	}
	res.Outputs["block"] = block
	return res, nil
}

// ---- HistoryReadNode ----

type historyReadExecutor struct{}

func (historyReadExecutor) Kind() NodeKind { return NodeKindHistoryRead }

func (historyReadExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(HistoryReadAttrs)
	if !ok {
		return res, fmt.Errorf("history_read: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if env.History == nil {
		return res, ErrNotImplemented
	}
	msgs, err := env.History.History(ctx, env.SessionID, a.N)
	if err != nil {
		return res, fmt.Errorf("history_read: node %q: %w", node.ID, err)
	}
	res.Outputs["messages"] = msgs
	return res, nil
}

// ---- SessionWriteNode ----

// sessionWriteExecutor persists one chat-history row via the
// HistoryWriter seam. The kind ships under the `state` archetype
// because it writes durable session-message rows; the Memory kind is
// reserved for long-term embedded memory and uses a separate store.
//
// Pulled from the typed `SessionWriteAttrs.TextInputPort` (default
// "text"); the input value MUST be a string. The role attr is one of
// {"assistant", "system"} — the manifest enum gates this at decode
// time so we don't repeat the validation here.
type sessionWriteExecutor struct{}

func (sessionWriteExecutor) Kind() NodeKind { return NodeKindSessionWrite }

func (sessionWriteExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(SessionWriteAttrs)
	if !ok {
		return res, fmt.Errorf("session_write: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	role := a.Role
	if role == "" {
		role = "assistant"
	}
	port := a.TextInputPort
	if port == "" {
		port = "text"
	}
	text, ok := inputs.GetString(port)
	if !ok {
		// Fallback: accept a Message slice on the named port and pull
		// the trailing assistant turn — this is the common shape the
		// modelExecutor emits on its `response` port, so a graph can
		// wire `response → text` without an explicit transform.
		if v, vok := inputs.Get(port); vok {
			if msgs, mok := v.([]Message); mok && len(msgs) > 0 {
				text = msgs[len(msgs)-1].Content
				ok = true
			} else if m, mok := v.(Message); mok {
				text = m.Content
				ok = true
			}
		}
	}
	if !ok || text == "" {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err":  "missing or empty text input",
			"port": port,
		})
		return res, fmt.Errorf("session_write: node %q: missing text on port %q", node.ID, port)
	}
	if env.SessionID == "" {
		// No session — best-effort no-op so a graph that runs outside
		// a chat session (batch / activity tests) doesn't hard-error.
		res.Outputs["message_id"] = ""
		res.Outputs["appended"] = false
		return res, nil
	}
	if env.HistoryWriter == nil {
		return res, ErrNoHistoryWriter
	}
	// The move fields stay zero here: WP01 lands the seam without
	// changing what session_write writes. WP02 is what starts stamping
	// per-iteration MoveKind / MoveIndex / TurnSpanID onto this entry.
	mid, err := env.HistoryWriter.AppendEntry(ctx, env.SessionID, HistoryEntry{
		Role:    role,
		Content: text,
	})
	if err != nil {
		_ = res.Events.AppendKind(env.RunID, node.ID, EventNodeError, map[string]any{
			"err":  err.Error(),
			"role": role,
		})
		return res, fmt.Errorf("session_write: node %q: %w", node.ID, err)
	}
	res.Outputs["message_id"] = mid
	res.Outputs["appended"] = true
	_ = res.Events.AppendKind(env.RunID, node.ID, EventSessionWrite, map[string]any{
		"role":       role,
		"message_id": mid,
		"bytes":      len(text),
	})
	// Fire post-LLM listeners registered via HookManager.RegisterPostHook
	// (chat-migration WP-D). The artifacts code-block detector subscribes
	// here so the SessionWriteNode-driven persistence path produces the
	// same "scan assistant message → capture artifacts" side-effect the
	// deleted toolloop pump used to drive.
	if role == "assistant" && env.Hooks != nil {
		env.Hooks.FirePostHooks(ctx, HookPostLLM, env.SessionID, mid, text)
	}
	return res, nil
}

// ---- TraceWriteNode ----

type traceWriteExecutor struct{}

func (traceWriteExecutor) Kind() NodeKind { return NodeKindTraceWrite }

func (traceWriteExecutor) Execute(_ context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(TraceWriteAttrs)
	if !ok {
		return res, fmt.Errorf("trace_write: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	severity := a.Severity
	if severity == "" {
		severity = "info"
	}
	res.Outputs["ack"] = "ok"
	_ = res.Events.AppendKind(env.RunID, node.ID, EventTraceWrite, map[string]any{
		"severity": severity,
		"message":  a.Message,
		"attrs":    a.Attrs,
	})
	_ = inputs
	return res, nil
}

// ---- CheckpointNode ----

type checkpointExecutor struct{}

func (checkpointExecutor) Kind() NodeKind { return NodeKindCheckpoint }

func (checkpointExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(CheckpointAttrs)
	if !ok {
		return res, fmt.Errorf("checkpoint: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	res.Outputs["ack"] = "ok"
	_ = res.Events.AppendKind(env.RunID, node.ID, EventCheckpoint, map[string]any{
		"label": a.Label,
	})
	if env.Hooks != nil {
		// Synthesize a memory-write hook so checkpoints land in greedy
		// memory just like LLM/tool boundaries (FR-027 'on-checkpoint').
		hookContent := "checkpoint:" + a.Label
		if hookContent == "checkpoint:" {
			hookContent = "checkpoint:" + node.ID
		}
		hookBatch := env.Hooks.Fire(ctx, HookOnCheckpoint, "session",
			"checkpoint — "+node.ID, hookContent, node.ID)
		for _, e := range hookBatch.Events {
			e.RunID = env.RunID
			e.NodeID = node.ID
			res.Events.Append(e)
		}
	}
	_ = inputs
	return res, nil
}

// ---- ArtifactNode (NEW, FR-058) ----

// artifactExecutor implements ExecArtifact — terminal output that
// replaces ad-hoc end-of-graph "dump the final answer to chat" patterns.
// The output_target attr discriminates session-message vs. file-path
// vs. report delivery. v1 emits a structured `artifact_emitted` event;
// the harness UI / file-write seam ships in WP06+.
type artifactExecutor struct{}

func (artifactExecutor) Kind() NodeKind { return NodeKindArtifact }

func (artifactExecutor) Execute(_ context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ArtifactAttrs)
	if !ok {
		return res, fmt.Errorf("artifact: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	target := a.OutputTarget
	if target == "" {
		target = "session_message"
	}

	// Pull content from attrs first, then fall back to the inbound port.
	content := a.Content
	if content == "" {
		if v, ok := inputs.GetString("content"); ok {
			content = v
		}
	}

	res.Outputs["ack"] = "ok"
	_ = res.Events.AppendKind(env.RunID, node.ID, EventArtifactEmitted, map[string]any{
		"mime_type":      a.MimeType,
		"output_target":  target,
		"attachment_ref": a.AttachmentRef,
		"content_bytes":  len(content),
	})
	return res, nil
}

// ---- read_file (FR-057a) ----

// readFileTokenCap caps file reads at ~256 KiB by default. Beyond that
// we truncate with a warning so an oversized file doesn't blow the
// kernel's per-run token budget. The cap is intentionally conservative
// — graphs that need to read a large file should use the corpus
// pipeline instead.
const readFileTokenCap = 256 * 1024

type readFileExecutor struct{}

func (readFileExecutor) Kind() NodeKind { return NodeKindReadFile }

func (readFileExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ReadFileAttrs)
	if !ok {
		return res, fmt.Errorf("read_file: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.Path == "" {
		return res, fmt.Errorf("read_file: node %q: path is required", node.ID)
	}
	cleanPath := filepath.Clean(a.Path)

	// Cedar gates: the broad Filesystem::"<path>" / file_read action
	// fires first, then the FR-058b finer-grained Read::"file" action.
	// Either deny short-circuits with *cedar.PolicyDeniedError surfaced
	// through the agentgraph error chain.
	if env.Policy != nil {
		if err := env.Policy.CheckFileRead(ctx, cleanPath); err != nil {
			return res, err
		}
		if err := env.Policy.CheckStateRead(ctx, "file"); err != nil {
			return res, err
		}
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return res, fmt.Errorf("read_file: node %q: stat %s: %w", node.ID, cleanPath, err)
	}
	raw, err := os.ReadFile(cleanPath)
	if err != nil {
		return res, fmt.Errorf("read_file: node %q: read %s: %w", node.ID, cleanPath, err)
	}

	truncated := false
	if len(raw) > readFileTokenCap {
		raw = raw[:readFileTokenCap]
		truncated = true
	}

	hash := sha256.Sum256(raw)
	sum := hex.EncodeToString(hash[:])

	encoding := a.Encoding
	if encoding == "" {
		encoding = "utf8"
	}
	var encoded string
	switch encoding {
	case "utf8":
		encoded = string(raw)
	case "base64":
		encoded = base64.StdEncoding.EncodeToString(raw)
	default:
		return res, fmt.Errorf("read_file: node %q: unknown encoding %q", node.ID, encoding)
	}

	// Provenance lands in the EventLog regardless of the output port
	// we choose so audit consumers see every read.
	_ = res.Events.AppendKind(env.RunID, node.ID, EventFileRead, map[string]any{
		"path":         cleanPath,
		"sha256":       sum,
		"mtime":        info.ModTime().UTC().Format(time.RFC3339Nano),
		"size_bytes":   info.Size(),
		"truncated":    truncated,
		"encoding":     encoding,
		"state_action": "Read::file",
	})

	if a.AsAttachment {
		// Best-effort registration via the AttachmentRegistry seam. When
		// the seam is missing we fall back to inline content so the
		// node still produces a usable result.
		if env.AttachmentRegistry != nil {
			id, regErr := env.AttachmentRegistry.Register(ctx, AttachmentBlock{
				MIME:   detectMIME(cleanPath),
				Data:   raw,
				URI:    cleanPath,
				Title:  filepath.Base(cleanPath),
				Inline: false,
			})
			if regErr == nil {
				res.Outputs["attachment_ref"] = id
				res.Outputs["result"] = encoded
				res.Outputs["sha256"] = sum
				res.Outputs["truncated"] = truncated
				fireGreedyHook(ctx, env, node, "post-read-file",
					"read_file:"+filepath.Base(cleanPath), encoded, &res)
				return res, nil
			}
			if !errors.Is(regErr, ErrNotImplemented) {
				return res, fmt.Errorf("read_file: node %q: register attachment: %w", node.ID, regErr)
			}
			// ErrNotImplemented falls through to inline mode.
		}
	}

	res.Outputs["result"] = encoded
	res.Outputs["sha256"] = sum
	res.Outputs["truncated"] = truncated
	fireGreedyHook(ctx, env, node, "post-read-file",
		"read_file:"+filepath.Base(cleanPath), encoded, &res)
	return res, nil
}

// ---- read_bash_output (FR-057b) ----

type readBashOutputExecutor struct{}

func (readBashOutputExecutor) Kind() NodeKind { return NodeKindReadBashOutput }

func (readBashOutputExecutor) Execute(ctx context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ReadBashOutputAttrs)
	if !ok {
		return res, fmt.Errorf("read_bash_output: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.BashRunId == "" {
		return res, fmt.Errorf("read_bash_output: node %q: bash_run_id is required", node.ID)
	}
	if env.BashOutput == nil {
		return res, ErrNoBashOutput
	}

	// Cedar gate: Read::"bash_output". No filesystem check here — the
	// underlying bash run already passed its own gates.
	if env.Policy != nil {
		if err := env.Policy.CheckStateRead(ctx, "bash_output"); err != nil {
			return res, err
		}
	}

	out, err := env.BashOutput.Get(ctx, a.BashRunId)
	if err != nil {
		return res, fmt.Errorf("read_bash_output: node %q: %w", node.ID, err)
	}

	// Manifest declares `include_stderr` defaults to true, but Go's
	// zero-value for bool is false. We treat an explicit-false attr as
	// "stdout only EXCEPT when stdout is empty and stderr has content"
	// so the node never silently returns nothing — this matches the
	// bash tool's transcript-merging behaviour. Tests cover both
	// branches; the kernel never sees a "default-true" decision tree
	// because it always passes ReadBashOutputAttrs through with the
	// raw bool the user authored.
	body := out.Stdout
	mergeStderr := a.IncludeStderr || (out.Stdout == "" && out.Stderr != "")
	if mergeStderr && out.Stderr != "" {
		if body != "" {
			body = out.Stderr + "\n" + body
		} else {
			body = out.Stderr
		}
	}

	if a.TailBytes > 0 && len(body) > a.TailBytes {
		body = body[len(body)-a.TailBytes:]
	}

	hash := sha256.Sum256([]byte(body))
	sum := hex.EncodeToString(hash[:])

	_ = res.Events.AppendKind(env.RunID, node.ID, EventBashOutputRead, map[string]any{
		"bash_run_id":  a.BashRunId,
		"command":      out.Command,
		"exit_code":    out.ExitCode,
		"sha256":       sum,
		"size_bytes":   len(body),
		"state_action": "Read::bash_output",
	})

	res.Outputs["result"] = body
	res.Outputs["exit_code"] = out.ExitCode
	res.Outputs["sha256"] = sum
	fireGreedyHook(ctx, env, node, "post-read-bash-output",
		"read_bash_output:"+a.BashRunId, body, &res)
	return res, nil
}

// ---- write_file (FR-057c) ----

type writeFileExecutor struct{}

func (writeFileExecutor) Kind() NodeKind { return NodeKindWriteFile }

func (writeFileExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(WriteFileAttrs)
	if !ok {
		return res, fmt.Errorf("write_file: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.Path == "" {
		return res, fmt.Errorf("write_file: node %q: path is required", node.ID)
	}
	cleanPath := filepath.Clean(a.Path)

	// Pull content from the named port reference. The schema marks
	// `content` required; surface a useful error if the port is empty.
	contentPort := a.Content
	if contentPort == "" {
		contentPort = "payload"
	}
	body, ok := inputs.GetString(contentPort)
	if !ok {
		// Fall back to a `content` literal when present (test
		// convenience; production graphs use the port reference).
		if v, ok2 := inputs.GetString("content"); ok2 {
			body = v
			ok = true
		}
	}
	if !ok {
		return res, fmt.Errorf("write_file: node %q: missing content on port %q", node.ID, contentPort)
	}

	// Cedar gates: file-level + FR-058b State::"file" gate. Either
	// deny short-circuits with *cedar.PolicyDeniedError. The policy
	// label, when set, is recorded in the EventLog so a downstream
	// reviewer can attribute the decision back to a labelled rule.
	if env.Policy != nil {
		if err := env.Policy.CheckFileWrite(ctx, cleanPath); err != nil {
			return res, err
		}
		if err := env.Policy.CheckStateWrite(ctx, "file"); err != nil {
			return res, err
		}
	}

	mode := a.Mode
	if mode == "" {
		mode = "create"
	}

	// Atomic-rename writes for create/replace; append uses an O_APPEND
	// open since rename on append would lose the file. The temp file
	// lives in the same directory as the target so cross-volume
	// renames don't fail (NFR-005 spirit).
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return res, fmt.Errorf("write_file: node %q: mkdir %s: %w", node.ID, dir, err)
	}

	switch mode {
	case "create":
		if _, err := os.Stat(cleanPath); err == nil {
			return res, fmt.Errorf("write_file: node %q: %s exists (mode=create)", node.ID, cleanPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("write_file: node %q: stat %s: %w", node.ID, cleanPath, err)
		}
		if err := atomicWrite(cleanPath, []byte(body)); err != nil {
			return res, fmt.Errorf("write_file: node %q: %w", node.ID, err)
		}
	case "replace":
		if err := atomicWrite(cleanPath, []byte(body)); err != nil {
			return res, fmt.Errorf("write_file: node %q: %w", node.ID, err)
		}
	case "append":
		f, err := os.OpenFile(cleanPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return res, fmt.Errorf("write_file: node %q: open append %s: %w", node.ID, cleanPath, err)
		}
		if _, werr := f.WriteString(body); werr != nil {
			_ = f.Close()
			return res, fmt.Errorf("write_file: node %q: append %s: %w", node.ID, cleanPath, werr)
		}
		if cerr := f.Close(); cerr != nil {
			return res, fmt.Errorf("write_file: node %q: close %s: %w", node.ID, cleanPath, cerr)
		}
	default:
		return res, fmt.Errorf("write_file: node %q: unknown mode %q", node.ID, mode)
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return res, fmt.Errorf("write_file: node %q: post-write stat: %w", node.ID, err)
	}
	hash := sha256.Sum256([]byte(body))
	sum := hex.EncodeToString(hash[:])

	_ = res.Events.AppendKind(env.RunID, node.ID, EventFileWrite, map[string]any{
		"path":         cleanPath,
		"sha256":       sum,
		"mtime":        info.ModTime().UTC().Format(time.RFC3339Nano),
		"size_bytes":   info.Size(),
		"mode":         mode,
		"policy_label": a.PolicyLabel,
		"state_action": "Write::file",
	})

	res.Outputs["ack"] = true
	res.Outputs["sha256"] = sum
	fireGreedyHook(ctx, env, node, "post-write-file",
		"write_file:"+filepath.Base(cleanPath), body, &res)
	return res, nil
}

// fireGreedyHook funnels the State-kind boundary memory writes through
// the kernel's HookManager. The boundary tag is a free-form label per
// FR-027's "every kernel boundary" rule — the nine canonical
// HookBoundary constants are reserved for top-level kernel events; the
// State kinds use stringified boundaries here so the audit log can
// distinguish a read_file fire from a generic post-tool boundary.
func fireGreedyHook(ctx context.Context, env *Env, node *Node, source, title, content string, res *Result) {
	if env == nil || env.Hooks == nil || content == "" {
		return
	}
	// Wrap the boundary in the post-tool archetype since State reads
	// and writes are kernel-side side effects, not LLM/user surfaces.
	batch := env.Hooks.Fire(ctx, HookPostTool, "session", title, content, source)
	for _, e := range batch.Events {
		e.RunID = env.RunID
		e.NodeID = node.ID
		res.Events.Append(e)
	}
}

// atomicWrite is the temp-file + rename helper used by the create /
// replace modes. The temp file lives in the same directory so a
// cross-volume rename never fails.
func atomicWrite(path string, body []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write_file-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename failed.
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, werr := tmp.Write(body); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("close temp: %w", cerr)
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, rerr)
	}
	return nil
}

// detectMIME is a stdlib-only MIME guesser based on file extension.
// Keeps the read_file executor self-contained so the AttachmentBlock
// it emits has a usable MIME field. Unknown extensions fall back to
// "application/octet-stream".
func detectMIME(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".html", ".htm":
		return "text/html"
	case ".csv":
		return "text/csv"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
