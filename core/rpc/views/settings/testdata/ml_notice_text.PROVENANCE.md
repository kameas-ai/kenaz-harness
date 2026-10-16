# Provenance — ml_notice_text.json

`ml_notice_text.json` is a byte-identical copy of kenaz-fleet's shared
notice-text fixture:

- Source: `kameas-ai/kenaz-fleet`, `service/testdata/ml_notice_text.json`
- Commit: `c9b08ca27f2d18f3838b855d34d8d16aef3f0110` (kenaz-fleet PR #289,
  "fix(ml): candid legacy-exclusions sentence; versioned exclusions template
  (no revision bump)"; unmerged at copy time, stacked on #286)
- SHA-256: `3e883321964db9ac65d82bd3cdbfa919a23f22a29de2dcef0cc0ca82bf0d88b4`
- Copied: 2026-10-16, for legal kameas-ai-11 (the legacy free-text
  exclusions sentence, Fleet exclusions template 2).

Read by `ml_fixture_test.go` (package settings): for EVERY case,
`MLExclusionsSummary` (the port of Fleet's `exclusionsSummary`, template 2)
must produce the case's exclusions paragraph byte-identically ("" when the
case excludes nothing), and every revision-1 case must match the local
fallback notice (`RenderMLNotice` + `ExclusionsText`, heading excluded)
in full.

Do not edit by hand. To refresh, copy the file again from a newer
kenaz-fleet commit, update the commit and hash above, and bump
`localExclusionsTemplate` in `ml.go` if the mirrored exclusions text
changed.
