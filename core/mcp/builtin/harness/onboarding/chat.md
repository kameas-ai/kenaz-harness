---
id: chat
title: Just chat
description: Skip the assisted setup and start an empty chat session. You can configure providers and MCP recipes manually in Settings later.
recommended_provider: ""
recommended_model: ""
recommended_recipes: []
# ---------------------------------------------------------------------------
# ENGINEERING NOTE — front matter only. Everything below the closing `---`
# is the SystemPrompt handed verbatim to the model (starters.go
# parseStarter), and since first-run-onboarding-01PMOB01 WP02 it is
# delivered as a position-0 session-scope attachment that rides every turn
# of the session for its whole life.
#
# The body used to read: "This starter dismisses the onboarding flow without
# proposing any configuration changes. Selecting it sets
# Settings.OnboardingCompleted = true and opens an empty chat session."
# That is documentation ABOUT the starter, addressed to an engineer reading
# this file — not an instruction to a model. Before WP02 it was inert.
# After WP02 it became the standing system prompt of every "Just chat"
# session, which is the one starter whose entire point is to open a chat
# with no onboarding framing at all.
#
# The body is therefore intentionally EMPTY. StartOnboardingSession's
# empty-prompt guard (core/rpc/onboarding_wiring.go) skips delivery
# entirely, so "Just chat" persists no attachment and the session composes
# byte-identically to an ordinary new chat. TestEmptyStarterPromptWritesNothing
# uses this starter's id as its exemplar for exactly that reason.
# ---------------------------------------------------------------------------
---
