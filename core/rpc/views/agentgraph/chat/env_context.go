package chat

import (
	"fmt"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// env_context.go builds the dynamic *environment* layer that the LLM seam
// stacks on top of the composed graph-base + node-role system prompt
// (WP01/WP02). The block states facts the model cannot otherwise know —
// product + platform, current date, the model in use, the sandboxed
// workspace, the filesystem-approval affordance, and a category-level
// tool inventory. Schemas are NOT included here (they are sent separately
// on the wire); the inventory is a one-liner so the whole block stays
// compact (≤ ~120 tokens).
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

// buildEnvContext renders the compact Markdown environment block. The
// output carries no trailing newline noise that would survive the
// composeSystemPrompt trim; callers stack it via composeSystemPrompt so
// an empty upstream layer never leaves a dangling separator.
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
	fmt.Fprintf(&b, "- Kenaz Harness on %s/%s. Current date: %s.\n",
		goos, goarch, in.Now.Format("2006-01-02"))
	if m := strings.TrimSpace(in.Model); m != "" {
		fmt.Fprintf(&b, "- Model in use: %s.\n", m)
	}
	if in.WorkspaceKnown && strings.TrimSpace(in.WorkspaceDir) != "" {
		state := ""
		if in.WorkspaceCounted {
			switch in.WorkspaceEntries {
			case 0:
				state = " (empty)"
			case 1:
				state = " (1 entry)"
			default:
				state = fmt.Sprintf(" (%d entries)", in.WorkspaceEntries)
			}
		}
		fmt.Fprintf(&b, "- Workspace: %s%s — a sandboxed agent workspace, not the user's project.\n",
			strings.TrimSpace(in.WorkspaceDir), state)
	} else {
		b.WriteString("- Workspace: a sandboxed agent workspace, not the user's project.\n")
	}
	b.WriteString("- Some paths require approval via the request-filesystem-access tool.\n")
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
