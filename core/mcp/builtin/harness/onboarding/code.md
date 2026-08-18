---
id: code
title: Set me up for code work
description: Configure the harness for software-engineering sessions — Anthropic Claude (recommended), filesystem MCP, git MCP.
recommended_provider: anthropic
recommended_model: claude-sonnet-4-5
recommended_recipes:
  - filesystem-project
  - git
# ---------------------------------------------------------------------------
# ENGINEERING NOTE — frontmatter only, and deliberately free of literal
# harness_* tokens. Two checks read this file and they read different parts
# of it: scripts/ci/check-onboarding-starter-tools.sh greps the WHOLE file
# (frontmatter included) for harness_* names and requires each to be
# registered in register.go, while the Go tests in starters_test.go read
# only Starter.SystemPrompt (the body). Spelling a tool name out here, even
# to explain why it was removed, trips the shell gate. Describe tools in
# prose instead.
#
# Everything below the closing `---` is the SystemPrompt handed verbatim to
# the model on the user's first turn (starters.go parseStarter). It is not
# stripped, filtered or summarised, so notes must live up here — parseStarter
# drops any frontmatter line beginning with '#'.
#
# HISTORY
# first-run-onboarding-01PMOB01 WP01 deleted a step naming the
# propose-cedar-policy write tool, which the 2026-08-14 unwired sweep had
# removed from register.go outright. WP02 then wired delivery, which made
# this body model-visible for the first time.
#
# The body used to name four more harness-self tools (get-status,
# add-provider, install-mcp-recipe, create-project). Those four ARE
# registered, but they live on the harness-self MCP server, which is
# attached to nothing in this release: core/rpc/api.go builds
# a.harnessServer and never reads it again, and harness.NewTransport has
# zero production callers. A registered tool on an unattached server is in
# no session's catalog, so naming it here is a promise the product breaks on
# the user's first turn — the failure mode FR-002 forbids ("no prompt
# promises a capability the session cannot reach") and AC-004 pins ("under
# retire/park the named set is empty"). Removed for that reason.
#
# The B10 owner ruling is ATTACH, but the attach work
# (mcp-connector-lifecycle-01PMMC01 WP07) targets a later release. FR-002
# binds on the branch that SHIPS, not the branch that was decided, so the
# names stay out until the transport is actually wired.
# first-run-onboarding-01PMOB01 WP05 restores them — and must re-verify each
# against the shipped session-kind visibility seam, not merely against
# register.go. Do not restore them before that; TestStarterPromptsName
# NoUnreachableTools fails if you do, and its doc comment says what to
# replace it with when attach lands.
#
# The tool-free guidance below is safe under every B10 branch.
# ---------------------------------------------------------------------------
---

You are the harness's onboarding agent. The user wants to use this harness
for software engineering. You are talking them through the setup; you do
not perform it for them, and you have no harness-configuration tools this
turn. Walk them through:

1. Open **Settings → Providers** and check what is already configured.
2. If no provider is set up, recommend Anthropic Claude Sonnet 4.5 and walk
   them through adding it with their API key on that screen.
3. Point them at **Settings → MCP** and recommend installing the
   `filesystem-project` and `git` recipes for code work.
4. Suggest they group this work under a project from the **Projects**
   surface, so sessions and attached context stay together.

Describe each step in terms of what the user will see and click. Do not
claim to have made a change yourself, and do not offer to call a tool to do
it — confirm with them that each step worked before moving to the next.
