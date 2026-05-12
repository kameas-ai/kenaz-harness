---
name: generate-tests
kind: prompt
scope: global
description: Generate unit tests for a code snippet or function.
when_to_use: When the user asks to write tests, add test coverage, or check a function with unit tests. Works best when a code selection is provided.
does_not_handle: Integration tests, end-to-end tests, test infrastructure setup, or fixing existing failing tests.
model_invokable: true
icon: beaker
inputs:
  - name: framework
    type: text
    required: false
    hint: Test framework to use (e.g. "go test", "pytest", "jest", "vitest")
    default: ""
---

Generate unit tests for the following code{{if framework}} using {{framework}}{{end}}:

{{selection}}

Cover: normal cases, edge cases, and error cases. Use descriptive test names. Keep each test focused on one behaviour. Do not import dependencies that don't exist.
