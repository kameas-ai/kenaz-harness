package openaiwire

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// ToolCallState tracks the in-flight assembly of one OpenAI streaming
// tool call. OpenAI streams tool calls as a sequence of deltas keyed
// by index; the JSON arguments arrive in fragments that must be
// concatenated across frames. One ToolCallState per index.
type ToolCallState struct {
	ID        string
	Name      string
	Arguments strings.Builder
	Emitted   bool
}

// ToolCallAccumulator accumulates streaming tool call deltas and
// materializes them as llm.ToolUse values on flush. It is safe for
// concurrent use (the pump goroutine writes; the test may snapshot).
type ToolCallAccumulator struct {
	mu       sync.Mutex
	partial  map[int]*ToolCallState
	order    []int
	toolCalls []llm.ToolUse
}

// Reset clears all accumulated state. Call between requests.
func (a *ToolCallAccumulator) Reset() {
	a.mu.Lock()
	a.partial = nil
	a.order = nil
	a.toolCalls = nil
	a.mu.Unlock()
}

// ApplyDelta applies one streaming tool_call delta entry.
// index, id, fnName, fnArgsFrag are taken from the SSE frame's
// choices[0].delta.tool_calls[i] fields.
func (a *ToolCallAccumulator) ApplyDelta(index int, id, fnName, fnArgsFrag string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.partial == nil {
		a.partial = map[int]*ToolCallState{}
	}
	state, ok := a.partial[index]
	if !ok {
		state = &ToolCallState{}
		a.partial[index] = state
		a.order = append(a.order, index)
	}
	if id != "" {
		state.ID = id
	}
	if fnName != "" {
		state.Name = fnName
	}
	if fnArgsFrag != "" {
		state.Arguments.WriteString(fnArgsFrag)
	}
}

// FlushLocked materializes all not-yet-emitted tool calls into
// llm.ToolUse values and emits StreamTool events on the provided
// channel. Caller must not hold a.mu (this method acquires it).
//
// Re-entrant: already-emitted calls are skipped.
func (a *ToolCallAccumulator) FlushLocked(events chan<- llm.StreamEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, idx := range a.order {
		state, ok := a.partial[idx]
		if !ok || state.Emitted {
			continue
		}
		args := state.Arguments.String()
		if args == "" {
			args = "{}"
		}
		tool := llm.ToolUse{
			ID:    state.ID,
			Name:  state.Name,
			Input: json.RawMessage(args),
		}
		a.toolCalls = append(a.toolCalls, tool)
		state.Emitted = true
		events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
	}
}

// ToolCalls returns a snapshot of all assembled tool calls. Safe to
// call from any goroutine after FlushLocked.
func (a *ToolCallAccumulator) ToolCalls() []llm.ToolUse {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.toolCalls) == 0 {
		return nil
	}
	out := make([]llm.ToolUse, len(a.toolCalls))
	copy(out, a.toolCalls)
	return out
}
