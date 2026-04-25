# Tasks Directory

This directory contains work package (WP) prompt files for the
`mcp-client-01KQ2WHG` mission.

## Directory Structure (v0.9.0+)

```
tasks/
├── WP01-core-types-and-pool-extension.md
├── WP02-jsonrpc-framing-and-method-types.md
├── ...
└── README.md
```

All WP files are stored flat in `tasks/`. Status is tracked in
`status.events.jsonl`, not in WP frontmatter.

## Work Package File Format

See `kitty-specs/llm-connector-01KQ1770/tasks/README.md` for the canonical
frontmatter shape.

## File Naming

- Format: `WP01-kebab-case-slug.md`
- Examples: `WP01-core-types-and-pool-extension.md`,
  `WP02-jsonrpc-framing-and-method-types.md`
