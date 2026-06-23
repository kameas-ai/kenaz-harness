// skill_catalog.go — pre_send builtin hook that injects the
// model-invokable skill catalog into the LLM context.
//
// When registered, the hook prepends a system message containing a
// compact catalog of all user commands with model_invokable=true so
// the model knows which kenaz__skill invocations are valid in the
// current session.
//
// Catalog format (injected as a system message):
//
//	## Available skills
//	You can invoke the following skills using the kenaz__skill tool.
//
//	### /summarize
//	Summarize the current document or selection.
//	When to use: when the user asks for a summary or TL;DR.
//	Does not handle: code explanations, refactors.
//	...
//
// Budget cap: the catalog is truncated when it would exceed
// MaxCatalogBytes (default 4096) to prevent token budget blow-out.
// A truncation note is appended so the model knows the catalog may
// be incomplete.
//
// model-invoked-skills-catalog-01KZNP3E WP03.
package hooks

import (
	"context"
	"fmt"
	"strings"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

const (
	// BuiltinSkillCatalog is the hook ID for the skill-catalog pre_send builtin.
	BuiltinSkillCatalog = "skill.catalog"

	// MaxCatalogBytes is the upper bound on the injected catalog system message.
	// Exceeding this triggers per-entry truncation. At ~150 bytes/entry a 4096
	// byte budget covers ~27 skills, which exceeds the expected default set of 5.
	MaxCatalogBytes = 4096

	// catalogHeader is prepended to every catalog injection.
	catalogHeader = "## Available skills\n" +
		"You can invoke the following skills using the `kenaz__skill` tool.\n" +
		"Call `kenaz__skill` with `{\"name\": \"<skill-name>\", \"args\": {...}}` " +
		"when the user's request matches a skill's description.\n\n"

	// catalogTruncationNote is appended when the budget forces truncation.
	catalogTruncationNote = "\n_(Skill catalog truncated due to context budget.)_"
)

// SkillLister is the narrow interface the catalog builtin needs to load
// the model-invokable command list. *coreslashcmd.Store satisfies this.
type SkillLister interface {
	ListModelInvokable(ctx context.Context) ([]coreslashcmd.UserCommand, error)
}

// SkillCatalogDeps bundles the dependencies RegisterSkillCatalogBuiltin needs.
type SkillCatalogDeps struct {
	// Store is the skill lister. Required; when nil the builtin registers as
	// a no-op so the chat pipeline stays usable even with a partially-wired
	// chassis.
	Store SkillLister
}

// RegisterSkillCatalogBuiltin wires the skill-catalog builtin into the
// supplied BuiltinRegistry. The builtin fires on pre_send and prepends a
// system message listing every model-invokable skill.
//
// Calling with a nil reg or nil deps.Store is a no-op.
func RegisterSkillCatalogBuiltin(reg *BuiltinRegistry, deps SkillCatalogDeps) {
	if reg == nil {
		return
	}
	reg.RegisterPreSend(BuiltinSkillCatalog, makeSkillCatalogBuiltin(deps), BuiltinDescriptor{
		ID:          BuiltinSkillCatalog,
		Name:        "Skill catalog: inject",
		Description: "Pre-send hook. Injects the catalog of model-invokable skills as a system message so the model can invoke them via kenaz__skill.",
		Events:      []string{EventPreSend},
		DefaultConfig: map[string]any{
			"max_catalog_bytes": MaxCatalogBytes,
		},
	})
}

// makeSkillCatalogBuiltin returns the PreSendBuiltin function that
// loads the catalog and injects it into the message slice.
func makeSkillCatalogBuiltin(deps SkillCatalogDeps) PreSendBuiltin {
	return func(ctx context.Context, ev PreSendEvent, cfg map[string]any) (PreSendEvent, error) {
		if deps.Store == nil {
			return ev, nil
		}

		budgetBytes := MaxCatalogBytes
		if v, ok := cfg["max_catalog_bytes"].(int); ok && v > 0 {
			budgetBytes = v
		}
		if vf, ok := cfg["max_catalog_bytes"].(float64); ok && vf > 0 {
			budgetBytes = int(vf)
		}

		skills, err := deps.Store.ListModelInvokable(ctx)
		if err != nil {
			// Non-fatal: log implicitly via caller's error swallow; return ev unchanged.
			return ev, fmt.Errorf("skill.catalog: list: %w", err)
		}
		if len(skills) == 0 {
			return ev, nil
		}

		catalog := buildCatalog(skills, budgetBytes)
		if catalog == "" {
			return ev, nil
		}

		// Inject after any leading system messages (same convention as memory.retrieve).
		insertAt := 0
		for i, m := range ev.Messages {
			if m.Role == "system" {
				insertAt = i + 1
				continue
			}
			break
		}
		injected := HookMessage{Role: "system", Content: catalog}
		newMsgs := make([]HookMessage, 0, len(ev.Messages)+1)
		newMsgs = append(newMsgs, ev.Messages[:insertAt]...)
		newMsgs = append(newMsgs, injected)
		newMsgs = append(newMsgs, ev.Messages[insertAt:]...)
		ev.Messages = newMsgs
		return ev, nil
	}
}

// buildCatalog formats the skill list as a markdown block, respecting
// the byte budget. Returns empty when the list is nil/empty.
func buildCatalog(skills []coreslashcmd.UserCommand, budgetBytes int) string {
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(catalogHeader)

	truncated := false
	for _, s := range skills {
		entry := formatSkillEntry(s)
		// Reserve room for the truncation note.
		if b.Len()+len(entry) > budgetBytes-len(catalogTruncationNote) {
			truncated = true
			break
		}
		b.WriteString(entry)
	}

	if truncated {
		b.WriteString(catalogTruncationNote)
	}

	return b.String()
}

// formatSkillEntry renders one skill as a markdown section.
func formatSkillEntry(s coreslashcmd.UserCommand) string {
	var b strings.Builder
	b.WriteString("### /")
	b.WriteString(s.Name)
	b.WriteString("\n")

	if s.Description != "" {
		b.WriteString(s.Description)
		b.WriteString("\n")
	}
	if s.WhenToUse != "" {
		b.WriteString("When to use: ")
		b.WriteString(strings.TrimSpace(s.WhenToUse))
		b.WriteString("\n")
	}
	if s.DoesNotHandle != "" {
		b.WriteString("Does not handle: ")
		b.WriteString(strings.TrimSpace(s.DoesNotHandle))
		b.WriteString("\n")
	}
	if len(s.Inputs) > 0 {
		b.WriteString("Arguments:")
		for _, inp := range s.Inputs {
			req := ""
			if inp.Required {
				req = " (required)"
			}
			b.WriteString(fmt.Sprintf("\n  - %s%s", inp.Name, req))
			if inp.Hint != "" {
				b.WriteString(": ")
				b.WriteString(inp.Hint)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
