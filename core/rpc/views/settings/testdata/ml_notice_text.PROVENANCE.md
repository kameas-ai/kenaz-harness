# Provenance — ml_notice_text.json

`ml_notice_text.json` is a byte-identical copy of kenaz-fleet's shared
notice-text fixture:

- Source: `kameas-ai/kenaz-fleet`, `service/testdata/ml_notice_text.json`
- Commit: `84338a0e4bd98632c8569a045d937fc0ce03fb2c` (kenaz-fleet PR #289
  as merged to main, "fix(ml): candid legacy-exclusions sentence; versioned
  exclusions template (no revision bump)"; adds the top-level
  `exclusions_template`: 2)
- SHA-256: `5c77e6ea0c4e6936b4fffc28d71afe05c259635f874fd53a009a5a2e9e67af88`
- Copied: 2026-10-16 (re-copied from kenaz-fleet main after #289 merged), for legal kameas-ai-11 (the legacy free-text
  exclusions sentence, Fleet exclusions template 2).

Read by `ml_fixture_test.go` (package settings): for EVERY case,
`MLExclusionsSummary` (the port of Fleet's `exclusionsSummary`, template 2)
must produce the case's exclusions paragraph byte-identically ("" when the
case excludes nothing), and every revision-1 case must match the local
fallback notice (`RenderMLNotice` + `ExclusionsText`, heading excluded)
in full; and `localExclusionsTemplate` must equal the fixture's top-level
`exclusions_template`.

Do not edit by hand. To refresh, copy the file again from a newer
kenaz-fleet commit, update the commit and hash above, and bump
`localExclusionsTemplate` in `ml.go` if the mirrored exclusions text
changed.
