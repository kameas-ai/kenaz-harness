package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// env_context.go builds the *environment* layers that the LLM seam stacks
// on top of the composed graph-base + node-role system prompt (WP01/WP02).
// They state facts the model cannot otherwise know — product + platform,
// current date, the model in use, the sandboxed workspace, the
// filesystem-approval affordance, and a category-level tool inventory.
// Schemas are NOT included here (they are sent separately on the wire);
// the two blocks together stay compact (≤ ~120 tokens).
//
// The facts are split by lifetime (tool-context-budget-01TCBUD01 WP05):
// buildEnvContext renders the session-stable ones into the cacheable
// system prefix; buildEnvState renders the per-call ones (date, workspace
// entry count, tool inventory) into the segment after the cache marker.
//
// buildEnvContext is deliberately pure: every fact it renders arrives via
// envContextInput so tests can pin a deterministic clock, GOOS/GOARCH,
// model, workspace state, and tool list. The adapter's buildEnvBlock
// gathers the live values (runtime.GOOS, an injected clock, os.ReadDir)
// and calls this. (system-prompt-layers WP03)

// envContextInput carries the facts the environment block renders. It is
// the pure-function boundary that keeps buildEnvContext testable — the
// caller injects the clock (Now) and every host-derived value rather than
// letting the builder reach for time.Now()/runtime/os directly.
type envContextInput struct {
	// Now is the injected clock reading used for the "current date" line.
	Now time.Time
	// GOOS / GOARCH are the platform identifiers (runtime.GOOS/GOARCH in
	// production). Empty values render as "unknown".
	GOOS   string
	GOARCH string
	// Model is the effective model id for the turn. Empty omits the line.
	Model string
	// WorkspaceDir is the absolute agent-workspace path. Only rendered
	// when WorkspaceKnown is true.
	WorkspaceDir string
	// WorkspaceNote is the trailing description for the workspace line
	// (spec 089 FR-4): says whether the path is the user's granted
	// workspace mount, the harness-private sandbox, or a fallback.
	// Empty keeps the pre-089 generic sandboxed-workspace wording.
	WorkspaceNote string
	// WorkspaceKnown reports whether WorkspaceDir is populated. When
	// false the block states a generic sandboxed workspace instead of a
	// concrete path.
	WorkspaceKnown bool
	// WorkspaceEntries is the top-level entry count. Only rendered when
	// WorkspaceCounted is true.
	WorkspaceEntries int
	// WorkspaceCounted reports whether WorkspaceEntries is meaningful (a
	// cheap os.ReadDir succeeded).
	WorkspaceCounted bool
	// Tools is the discovered tool catalog for this turn; summarised at
	// category level, never with full schemas.
	Tools []corellm.ToolSpec
}

// buildEnvContext renders the stable half of the environment: facts that
// hold for the whole session (platform, model, workspace location, the
// approval affordance). It sits in the cacheable system prefix, so it
// must not render anything that changes call to call — that belongs in
// buildEnvState. The output carries no trailing newline; callers stack it
// via composeSystemPrompt.
func buildEnvContext(in envContextInput) string {
	goos := in.GOOS
	if goos == "" {
		goos = "unknown"
	}
	goarch := in.GOARCH
	if goarch == "" {
		goarch = "unknown"
	}

	var b strings.Builder
	b.WriteString("## Environment\n")
	fmt.Fprintf(&b, "- Kenaz Harness on %s/%s.\n", goos, goarch)
	if m := strings.TrimSpace(in.Model); m != "" {
		fmt.Fprintf(&b, "- Model in use: %s.\n", m)
	}
	if in.WorkspaceKnown && strings.TrimSpace(in.WorkspaceDir) != "" {
		note := strings.TrimSpace(in.WorkspaceNote)
		if note == "" {
			note = "a sandboxed agent workspace, not the user's project."
		}
		fmt.Fprintf(&b, "- Workspace: %s — %s\n", strings.TrimSpace(in.WorkspaceDir), note)
	} else {
		b.WriteString("- Workspace: a sandboxed agent workspace, not the user's project.\n")
	}
	b.WriteString("- Some paths require approval via the request-filesystem-access tool.")
	return b.String()
}

// buildEnvState renders the per-call half of the environment: the date,
// the workspace's current entry count and the tool inventory. It goes in
// GenerationRequest.SystemVolatile, after the cache marker.
func buildEnvState(in envContextInput) string {
	var b strings.Builder
	b.WriteString("## Current state\n")
	fmt.Fprintf(&b, "- Current date: %s.\n", in.Now.Format("2006-01-02"))
	if in.WorkspaceKnown && strings.TrimSpace(in.WorkspaceDir) != "" && in.WorkspaceCounted {
		switch in.WorkspaceEntries {
		case 0:
			b.WriteString("- Workspace contents: empty.\n")
		case 1:
			b.WriteString("- Workspace contents: 1 entry.\n")
		default:
			fmt.Fprintf(&b, "- Workspace contents: %d entries.\n", in.WorkspaceEntries)
		}
	}
	fmt.Fprintf(&b, "- Tools: %s", summarizeToolInventory(in.Tools))
	return b.String()
}

// summarizeToolInventory produces a single high-level sentence describing
// the tool catalog by category. It never emits per-tool schemas (those go
// on the wire separately) — the goal is orientation, not specification.
func summarizeToolInventory(tools []corellm.ToolSpec) string {
	if len(tools) == 0 {
		return "no tools are available this turn."
	}
	var fs, web, artifacts, todo, other int
	for _, t := range tools {
		n := strings.ToLower(t.Name)
		switch {
		case strings.Contains(n, "read_file"),
			strings.Contains(n, "write_file"),
			strings.Contains(n, "edit_file"),
			strings.Contains(n, "list_dir"),
			strings.Contains(n, "glob"),
			strings.Contains(n, "grep"),
			strings.Contains(n, "filesystem"):
			fs++
		case strings.Contains(n, "web_fetch"),
			strings.Contains(n, "web_search"),
			strings.Contains(n, "fetch"),
			strings.Contains(n, "search"):
			web++
		case strings.Contains(n, "artifact"):
			artifacts++
		case strings.Contains(n, "todo"):
			todo++
		default:
			other++
		}
	}
	cats := make([]string, 0, 5)
	if fs > 0 {
		cats = append(cats, "filesystem")
	}
	if web > 0 {
		cats = append(cats, "web")
	}
	if artifacts > 0 {
		cats = append(cats, "artifacts")
	}
	if todo > 0 {
		cats = append(cats, "task tracking")
	}
	if other > 0 {
		cats = append(cats, "connected servers")
	}
	if len(cats) == 0 {
		return fmt.Sprintf("%d available.", len(tools))
	}
	return fmt.Sprintf("%d available across %s.", len(tools), joinWithAnd(cats))
}

// joinWithAnd renders a short human list ("a", "a and b", "a, b, and c").
func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

// composeSystemPrompt stacks system-prompt layers top-to-bottom, trimming
// each and dropping empties, then joining with a blank line. The base
// (graph + node role) comes first; the environment layer and any user
// custom-instructions layer follow. Dropping empty layers means a nil
// clock / absent custom instructions never introduce a dangling
// separator. (system-prompt-layers WP03/WP04)
//
// tmpl is the per-family-message-shaping-01PMDL06 mechanism: the
// active model's ModelProfile.PromptTemplate (llm.PromptTemplateRef,
// versioned-model-profile-01PMDL04), or nil. It delegates to
// prompts.Compose, which is the single implementation that decides how
// a nil vs. populated tmpl renders — nil (or an unregistered Format)
// reproduces this function's pre-WP01 plain join byte-for-byte. This is
// the harness seam (core/rpc/views/agentgraph/chat) where model
// knowledge legitimately lives per the frozen-core discipline
// (core/agentgraph must not string-match model-family names); the
// caller resolving a live ModelProfile for tmpl is deferred (nothing
// resolves one on the request path yet — see llm_provider_adapter.go's
// call site).
func composeSystemPrompt(tmpl *corellm.PromptTemplateRef, layers ...string) string {
	return prompts.Compose(tmpl, layers...)
}
