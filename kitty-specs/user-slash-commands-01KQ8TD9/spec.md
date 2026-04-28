# Spec: User-defined slash commands

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The slash command surface (`/help`, `/clear`, `/model`, `/memorize`, `/recall`, `/forget`, `/branch`) is built into the harness. Power users want project-specific commands: `/deploy`, `/standup`, `/lint`, etc. — text expansions or tool-call shortcuts.

## 2. Goals

- New `/commands` view for authoring user commands.
- A user command is one of: text expansion (insert pre-written text), tool dispatch (auto-call a tool with fixed args), agent prompt (stuff a templated system message).
- Per-project scope; project-scoped commands surface only in sessions of that project.
- Global commands always available.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `slash_commands_user` table: id, name, scope (global/project), kind (text/tool/prompt), payload, project_id (nullable). | proposed |
| FR-002 | Slash autocomplete merges built-in + user commands; user commands marked with a chip. | proposed |
| FR-003 | Editor view supports CRUD with payload validation per kind. | proposed |
| FR-004 | Tool-dispatch user commands resolve a tool name + arg template at execute time. | proposed |
| FR-005 | Prompt user commands support template variables (`{{selection}}`, `{{cwd}}`, `{{date}}`). | proposed |
| FR-006 | Audit log entry on each user command execution. | proposed |

## 4. Success criteria

- User authors `/deploy` mapped to a bash invocation; it executes correctly via the slash autocomplete.
- Project-scoped command absent from global sessions.
