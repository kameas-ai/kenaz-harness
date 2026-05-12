---
name: summarize
kind: prompt
scope: global
description: Summarize the current text, document, or selection concisely.
when_to_use: When the user asks for a summary, TL;DR, overview, or brief description of content. Also triggers on "what does this do?", "explain briefly", or "give me the highlights".
does_not_handle: Deep code analysis, line-by-line explanations, refactoring suggestions, or bug reports.
model_invokable: true
icon: sparkles
inputs:
  - name: focus
    type: text
    required: false
    hint: Optional focus area (e.g. "key decisions", "action items", "technical details")
    default: ""
---

Summarize the following content concisely{{if focus}}, focusing on {{focus}}{{end}}:

{{selection}}

Provide a clear, structured summary. Use bullet points for multiple distinct points. Keep it brief — aim for the essential information a reader needs.
