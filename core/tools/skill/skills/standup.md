---
name: standup
kind: prompt
scope: global
description: Draft a daily standup update from recent work context.
when_to_use: When the user asks for a standup update, daily report, "what did I do yesterday", or wants to summarize recent work for a team sync.
does_not_handle: Sprint planning, project management tasks, or generating work that hasn't been described.
model_invokable: true
icon: calendar
inputs:
  - name: period
    type: enum
    required: false
    hint: Time period covered by the standup
    default: yesterday
    enum_values:
      - yesterday
      - last-week
      - today
  - name: blockers
    type: text
    required: false
    hint: Any blockers to mention
    default: ""
---

Draft a concise daily standup update covering {{period}}.

Context about recent work:
{{selection}}

Format as three sections:
- **Done**: what was completed
- **Doing**: what is in progress or planned for today
- **Blockers**: {{if blockers}}{{blockers}}{{else}}none{{end}}

Keep it brief — standup should take under 2 minutes to read aloud.
