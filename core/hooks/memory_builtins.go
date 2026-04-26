// Memory builtins. These wire core/memory's Retriever and Store into
// the hooks subsystem so the harness ships with two preinstalled
// hooks:
//
//   - memory.retrieve  (pre_send)  — k-NN lookup of relevant chunks,
//                                    prepended as a system message.
//   - memory.persist   (post_send) — embeds the user+assistant turn
//                                    and writes a chunk to the store.
//
// The builtins are intentionally independent of any concrete embedder
// or store impl: callers pass interfaces. Tests substitute fakes.
package hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/memory"
)

// MemoryRetriever is the surface the retrieve builtin needs. Mirrors
// memory.Retriever's Retrieve method without taking a hard type
// dependency, so tests can supply a fake.
type MemoryRetriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]memory.Snippet, error)
}

// MemoryStore is the surface the persist builtin needs.
type MemoryStore interface {
	Add(ctx context.Context, chunk memory.Chunk) error
}

// MemoryEmbedder is the surface the persist builtin needs.
type MemoryEmbedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// MemoryDeps bundles the dependencies the memory builtins need.
type MemoryDeps struct {
	Retriever MemoryRetriever
	Store     MemoryStore
	Embedder  MemoryEmbedder
	// IDFunc returns a unique chunk id. When nil a default impl
	// derives one from the SessionID + nano timestamp.
	IDFunc func() string
}

// RegisterMemoryBuiltins wires memory.retrieve + memory.persist into
// the supplied builtin registry. Either dep may be nil — the matching
// builtin will return early with a no-op.
func RegisterMemoryBuiltins(reg *BuiltinRegistry, deps MemoryDeps) {
	if reg == nil {
		return
	}
	reg.RegisterPreSend(BuiltinMemoryRetrieve, makeMemoryRetrieve(deps.Retriever), BuiltinDescriptor{
		ID:          BuiltinMemoryRetrieve,
		Name:        "Memory: retrieve",
		Description: "Pre-send hook. Embeds the latest user turn and prepends matching long-term-memory snippets as a system message.",
		Events:      []string{EventPreSend},
		DefaultConfig: map[string]any{
			"top_k":         3,
			"min_score":     0.3,
			"system_prefix": "Relevant memory snippets:",
		},
	})
	reg.RegisterPostSend(BuiltinMemoryPersist, makeMemoryPersist(deps), BuiltinDescriptor{
		ID:          BuiltinMemoryPersist,
		Name:        "Memory: persist",
		Description: "Post-send hook. Embeds the user+assistant turn pair and writes a chunk to the long-term memory store.",
		Events:      []string{EventPostSend},
		DefaultConfig: map[string]any{
			"min_user_chars":         80,
			"min_assistant_chars":    200,
			"skip_on_finish_reason":  []string{"error", "stop-called"},
		},
	})
}

func makeMemoryRetrieve(retriever MemoryRetriever) PreSendBuiltin {
	return func(ctx context.Context, ev PreSendEvent, cfg map[string]any) (PreSendEvent, error) {
		if retriever == nil {
			return ev, nil
		}
		topK := intFromConfig(cfg, "top_k", 3)
		prefix := stringFromConfig(cfg, "system_prefix", "Relevant memory snippets:")
		query := ev.UserText
		if query == "" {
			// Fall back to the last user turn in Messages.
			for i := len(ev.Messages) - 1; i >= 0; i-- {
				if ev.Messages[i].Role == "user" {
					query = ev.Messages[i].Content
					break
				}
			}
		}
		if query == "" {
			return ev, nil
		}
		snippets, err := retriever.Retrieve(ctx, query, topK)
		if err != nil {
			return ev, err
		}
		if len(snippets) == 0 {
			return ev, nil
		}
		var b strings.Builder
		b.WriteString(prefix)
		b.WriteString("\n")
		for _, s := range snippets {
			b.WriteString("- ")
			b.WriteString(s.Content)
			b.WriteString("\n")
		}
		// Prepend the system message. Mission A's system-prompt work
		// will land another system message earlier in the slice; the
		// retrieve hook always inserts AFTER the system prompt block
		// (i.e. just before conversation history). We achieve that by
		// inserting after any leading system messages.
		insertAt := 0
		for i, m := range ev.Messages {
			if m.Role == "system" {
				insertAt = i + 1
				continue
			}
			break
		}
		injected := HookMessage{Role: "system", Content: b.String()}
		newMessages := make([]HookMessage, 0, len(ev.Messages)+1)
		newMessages = append(newMessages, ev.Messages[:insertAt]...)
		newMessages = append(newMessages, injected)
		newMessages = append(newMessages, ev.Messages[insertAt:]...)
		ev.Messages = newMessages
		return ev, nil
	}
}

func makeMemoryPersist(deps MemoryDeps) PostSendBuiltin {
	return func(ctx context.Context, ev PostSendEvent, cfg map[string]any) error {
		if deps.Store == nil || deps.Embedder == nil {
			return nil
		}
		minUser := intFromConfig(cfg, "min_user_chars", 80)
		minAssistant := intFromConfig(cfg, "min_assistant_chars", 200)
		skipReasons := stringSliceFromConfig(cfg, "skip_on_finish_reason",
			[]string{"error", "stop-called"})

		for _, reason := range skipReasons {
			if reason == ev.FinishReason {
				return nil
			}
		}
		if len(ev.UserTurn) < minUser {
			return nil
		}
		if len(ev.AssistantTurn) < minAssistant {
			return nil
		}
		text := strings.TrimSpace(
			"Q: " + ev.UserTurn + "\nA: " + ev.AssistantTurn,
		)
		vecs, err := deps.Embedder.Embed(ctx, []string{text})
		if err != nil {
			return fmt.Errorf("embed turn: %w", err)
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return nil
		}
		id := ""
		if deps.IDFunc != nil {
			id = deps.IDFunc()
		}
		if id == "" {
			id = fmt.Sprintf("auto-%s-%d", ev.SessionID, NowUnixNano())
		}
		ch := memory.Chunk{
			ID:         id,
			SessionID:  ev.SessionID,
			SourceTurn: "post_send",
			Content:    text,
			Embedding:  vecs[0],
			CreatedAt:  Now(),
		}
		return deps.Store.Add(ctx, ch)
	}
}

// NowUnixNano returns the current time in nanoseconds. Wraps Now so
// tests can override Now and have NowUnixNano follow.
func NowUnixNano() int64 { return Now().UnixNano() }

// ── small typed helpers for hook config maps ──────────────────────────

func intFromConfig(cfg map[string]any, key string, def int) int {
	v, ok := cfg[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return def
	}
}

func stringFromConfig(cfg map[string]any, key, def string) string {
	v, ok := cfg[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func stringSliceFromConfig(cfg map[string]any, key string, def []string) []string {
	v, ok := cfg[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return def
}

// StarterMemoryHooks returns the two starter-hook configs the
// settings panel auto-installs when the user enables long-term
// memory. Each hook gets a fixed, deterministic ID so the
// "auto-uninstall" path can find them.
func StarterMemoryHooks() []Hook {
	return []Hook{
		{
			ID:      "starter:memory.retrieve",
			Name:    "Memory · retrieve",
			Event:   EventPreSend,
			Kind:    KindBuiltin,
			Enabled: true,
			Builtin: BuiltinMemoryRetrieve,
			Config: map[string]any{
				"top_k":         3,
				"min_score":     0.3,
				"system_prefix": "Relevant memory snippets:",
			},
		},
		{
			ID:      "starter:memory.persist",
			Name:    "Memory · persist",
			Event:   EventPostSend,
			Kind:    KindBuiltin,
			Enabled: true,
			Builtin: BuiltinMemoryPersist,
			Config: map[string]any{
				"min_user_chars":        80,
				"min_assistant_chars":   200,
				"skip_on_finish_reason": []string{"error", "stop-called"},
			},
		},
	}
}
