package agentgraph

import (
	"context"
	"errors"
	"fmt"
)

// State-primitive executors (FR-019 .. FR-025): Memory, CorpusRead,
// CorpusWrite, Attachment, HistoryRead, TraceWrite, Checkpoint.

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
	hits, err := env.Corpus.Search(ctx, a.CorpusIDs, query, a.TopK)
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
	job, err := env.Corpus.Enqueue(ctx, a.CorpusID, a.SourcePath)
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
	block, err := env.Attachments.Resolve(ctx, a.AttachmentID)
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
