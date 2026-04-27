// Package chat hosts the ChatRunner — the kernel-driven entry point
// that replaces core/toolloop as the chassis chat path (mission
// agent-kernel-graph-chat-migration WP03).
//
// Architecture
//
// On every user message the chassis calls ChatRunner.StartStream. The
// runner:
//
//   1. Resolves the provider profile via the corellm.Registry.
//   2. Loads session history through the session manager.
//   3. Constructs a kernel agentgraph.Env containing:
//        - LLMProvider: an adapter that wraps registry.Stream and pumps
//          deltas into the kernel-bound StreamSink.
//        - ToolRegistry: an adapter that routes through the existing
//          permission resolver + MCP pool + builtin registry.
//        - HistoryWriter: the session manager (for SessionWriteNode).
//        - StreamSink: a bridge that fans agentgraph.StreamEvent into
//          the existing `llm:stream-chunk` broker topic so the chat
//          surface sees byte-equal deltas with no frontend change.
//   4. Applies the per-run MaxAgentTurns dial onto the chat graph's
//      LoopNode max_iterations.
//   5. Spawns kernel.Run(ctx, env) on a goroutine. The first fire
//      pauses on the AskNode; subsequent user messages start fresh
//      runs (per the open-questions decision in the mission brief —
//      run-level state doesn't persist across user turns within a
//      session in v1).
//   6. Returns the subscription id; goroutine emits llm:stream-closed
//      on completion.
//
// Wire-shape compatibility
//
// The runner emits StreamChunkPayload values on the existing
// "llm:stream-chunk" topic. The chat surface (frontend useSession.ts)
// is byte-untouched.
//
// Status
//
// This package is the foundation drop. The StartStream pipeline is
// scaffolded with the seams it needs but the chassis-side wiring of
// the LLM impl onto the runner happens in WP04. The toolloop-driven
// chat path remains the production default until parity tests in WP03
// + WP06 turn green.
package chat
