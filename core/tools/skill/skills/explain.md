---
name: explain
kind: prompt
scope: global
description: Explain a concept, code snippet, or technical term in plain language.
when_to_use: When the user asks "what is X?", "how does X work?", "explain this code", "I don't understand X", or wants a plain-language breakdown of something technical.
does_not_handle: Debugging, fixing bugs, writing new code, summarizing long documents.
model_invokable: true
icon: book-open
inputs:
  - name: audience
    type: enum
    required: false
    hint: Target audience for the explanation
    default: developer
    enum_values:
      - developer
      - beginner
      - non-technical
      - expert
---

Explain the following to a {{audience}} audience:

{{selection}}

Be clear and concrete. Use analogies where helpful. If explaining code, describe what it does and why — not just a line-by-line translation.
