---
name: improve-writing
kind: prompt
scope: global
description: Improve the clarity, tone, and structure of a piece of writing.
when_to_use: When the user asks to improve, polish, rewrite, or clean up prose, documentation, commit messages, emails, or any natural-language text.
does_not_handle: Code improvements, adding new content, changing the meaning, or translating to another language.
model_invokable: true
icon: pencil
inputs:
  - name: style
    type: enum
    required: false
    hint: Writing style to target
    default: clear
    enum_values:
      - clear
      - formal
      - casual
      - concise
      - technical
---

Improve the following writing to be more {{style}}. Preserve the original meaning and intent. Fix grammar, awkward phrasing, and unclear structure. Do not add new information.

{{selection}}

Return only the improved text, without commentary or explanation.
