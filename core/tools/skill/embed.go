package skill

import "embed"

// EmbeddedSkills holds the five bundled skill templates that ship with
// the harness binary. Each file is a markdown document with YAML
// frontmatter declaring name, kind, description, when_to_use,
// does_not_handle, and model_invokable=true.
//
// Power users can extend or override the defaults by adding their own
// *.md files in <DataDir>/slash_commands/ — the slashcmd store merges
// user-authored commands over the embedded set at query time.
//
// Bundled skills (model-invoked-skills-catalog-01KZNP3E WP04):
//
//   - summarize.md      — summarize a document or selection
//   - explain.md        — explain a concept or code in plain language
//   - improve-writing.md — polish prose, docs, commit messages
//   - generate-tests.md — generate unit tests for a code snippet
//   - standup.md        — draft a daily standup update
//
//go:embed skills/*.md
var EmbeddedSkills embed.FS
