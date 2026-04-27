package agentgraph

import (
	"context"
	"sync"
)

// HookBoundary identifies the kernel firing point where a memory hook
// runs. The nine boundaries from FR-027.
type HookBoundary string

const (
	HookPostLLM         HookBoundary = "post-llm"
	HookPostTool        HookBoundary = "post-tool"
	HookPostUserMessage HookBoundary = "post-user-message"
	HookPreBranchFork   HookBoundary = "pre-branch-fork"
	HookOnBranchClose   HookBoundary = "on-branch-close"
	HookOnMerge         HookBoundary = "on-merge"
	HookOnSessionEnd    HookBoundary = "on-session-end"
	HookOnCheckpoint    HookBoundary = "on-checkpoint"
	HookOnExplicitPin   HookBoundary = "on-explicit-pin"
)

// AllHookBoundaries lists every supported boundary in canonical order.
func AllHookBoundaries() []HookBoundary {
	return []HookBoundary{
		HookPostLLM, HookPostTool, HookPostUserMessage,
		HookPreBranchFork, HookOnBranchClose, HookOnMerge,
		HookOnSessionEnd, HookOnCheckpoint, HookOnExplicitPin,
	}
}

// HookEvent is the EventLog payload for a memory hook fire.
type HookEvent struct {
	Boundary  HookBoundary `json:"boundary"`
	Scope     string       `json:"scope"`
	Title     string       `json:"title,omitempty"`
	Source    string       `json:"source,omitempty"`
	ChunkID   string       `json:"chunk_id,omitempty"`
	Deduped   bool         `json:"deduped,omitempty"`
	Skipped   bool         `json:"skipped,omitempty"`
	SkipReason string      `json:"skip_reason,omitempty"`
}

// Redactor is the optional redaction pipeline that runs before
// content lands in memory. Nil disables redaction.
type Redactor interface {
	Redact(s string) string
}

// HookManager owns the kernel's greedy memory writes. It is kernel-
// owned (FR-026): tools cannot write memory, only the kernel.
type HookManager struct {
	mu        sync.Mutex
	memory    MemoryStore
	sessionID string
	projectID string
	enabled   bool
	redactor  Redactor

	// fired is a per-(boundary, content_hash) seen-set used for
	// in-process dedup on top of the store-level dedup. Belt-and-
	// braces: the store does the canonical hash dedup; this catches
	// the same hook firing twice in one fast pump.
	fired map[string]struct{}

	// Journal records every hook fire (skipped or written) so tests +
	// the inspector RPC can audit hook activity. Append-only; cap
	// pruning is the caller's job (Bundle E ships the prune sweep).
	journal []HookRecord
}

// HookRecord is one entry in the in-memory hook journal.
type HookRecord struct {
	Boundary HookBoundary
	Scope    string
	Title    string
	Source   string
	Written  bool
	Deduped  bool
	Skipped  bool
	Reason   string
}

// NewHookManager wires the hook manager to a memory store. The default
// `enabled = true` matches FR-027.
func NewHookManager(mem MemoryStore, sessionID, projectID string) *HookManager {
	return &HookManager{
		memory:    mem,
		sessionID: sessionID,
		projectID: projectID,
		enabled:   true,
		fired:     make(map[string]struct{}),
	}
}

// SetEnabled toggles the always-on default. Mirrors the
// `memory_hooks_enabled` dial.
func (h *HookManager) SetEnabled(on bool) {
	h.mu.Lock()
	h.enabled = on
	h.mu.Unlock()
}

// SetRedactor installs a redaction pipeline. Pass nil to disable.
func (h *HookManager) SetRedactor(r Redactor) {
	h.mu.Lock()
	h.redactor = r
	h.mu.Unlock()
}

// Fire runs the hook for the given boundary. The content is the text
// the kernel wants captured; scope is global / project / session
// (defaults to session when empty). Returns the EventBatch the kernel
// should append.
//
// The "greedy" rule (FR-027) says we write near-everything. Per-write
// selectivity is explicitly disallowed in v1.
func (h *HookManager) Fire(ctx context.Context, boundary HookBoundary, scope, title, content, source string) EventBatch {
	if scope == "" {
		scope = "session"
	}
	var batch EventBatch
	h.mu.Lock()
	if !h.enabled {
		// Always emit the journal entry so the inspector can see the
		// "would have fired" trace; just skip the write.
		h.journal = append(h.journal, HookRecord{
			Boundary: boundary, Scope: scope, Title: title, Source: source,
			Skipped: true, Reason: "hooks disabled",
		})
		h.mu.Unlock()
		_ = batch.AppendKind("", "", EventHookFired, HookEvent{
			Boundary: boundary, Scope: scope, Title: title, Source: source,
			Skipped: true, SkipReason: "hooks disabled",
		})
		return batch
	}
	// In-process dedup: same boundary + content hash within this run.
	hash := contentHash(content)
	dedupKey := string(boundary) + "|" + hash
	if _, seen := h.fired[dedupKey]; seen {
		h.journal = append(h.journal, HookRecord{
			Boundary: boundary, Scope: scope, Title: title, Source: source,
			Deduped: true,
		})
		h.mu.Unlock()
		_ = batch.AppendKind("", "", EventHookFired, HookEvent{
			Boundary: boundary, Scope: scope, Title: title, Source: source,
			Deduped: true,
		})
		return batch
	}
	h.fired[dedupKey] = struct{}{}
	redacted := content
	if h.redactor != nil {
		redacted = h.redactor.Redact(content)
	}
	mem := h.memory
	sess := h.sessionID
	proj := h.projectID
	h.mu.Unlock()

	id, deduped, err := mem.Write(ctx, MemoryWrite{
		Scope:     scope,
		ScopeID:   resolveScopeID(scope, proj, sess),
		SessionID: sess,
		ProjectID: proj,
		Content:   redacted,
		Title:     title,
		Source:    string(boundary),
	})
	rec := HookRecord{
		Boundary: boundary, Scope: scope, Title: title, Source: source,
		Written: err == nil, Deduped: deduped,
	}
	if err != nil {
		rec.Skipped = true
		rec.Reason = err.Error()
	}
	h.mu.Lock()
	h.journal = append(h.journal, rec)
	h.mu.Unlock()

	hev := HookEvent{
		Boundary: boundary, Scope: scope, Title: title, Source: source,
		ChunkID: id, Deduped: deduped,
	}
	if err != nil {
		hev.Skipped = true
		hev.SkipReason = err.Error()
	}
	_ = batch.AppendKind("", "", EventHookFired, hev)
	return batch
}

// Journal returns a copy of the in-memory hook journal. Tests use it
// to assert hook fires.
func (h *HookManager) Journal() []HookRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HookRecord(nil), h.journal...)
}

func resolveScopeID(scope, projectID, sessionID string) string {
	switch scope {
	case "global":
		return ""
	case "project":
		return projectID
	default:
		return sessionID
	}
}

func contentHash(s string) string {
	// Tiny FNV-1a; we just want a fast in-process dedup key. Memory
	// store uses sha256 via memory.HashContent for canonical dedup.
	const offset uint64 = 14695981039346656037
	const prime uint64 = 1099511628211
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	out := [16]byte{}
	const digits = "0123456789abcdef"
	for i := 15; i >= 0; i-- {
		out[i] = digits[h&0xF]
		h >>= 4
	}
	return string(out[:])
}
