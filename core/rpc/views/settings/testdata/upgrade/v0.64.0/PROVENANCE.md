# settings.json fixture — v0.64.0 shape

Hand-written for `controls-and-readouts-that-tell-the-truth-01PMZ808` WP-PI
(AC-PI-1, settings half). Before this mission there was **no `settings.json`
fixture of any kind** in the repo — `scripts/ci/upgrade-snapshot.sh` covers
`data.db` only, and every settings-shape trace in the closing sweep was
against the *current* struct shape, which is exactly the blind spot this
fixture exists to close (see `docs/dead-code-audit-2026-08-18.md:1622`).

This file is a **representative subset**, not a field-complete dump of a
real v0.64.0 install — it was constructed by hand from the v0.64.0-tagged
`core/rpc/views/settings/api.go` field set, not captured from a real
process. It intentionally includes only the fields load-bearing for this
mission's persistence tests plus a few realistic neighbours (theme, accent,
crash tier, monthly cost) so the fixture reads as a plausible partial file
rather than a synthetic minimum.

**Load-bearing omission:** the file has **no `autoCollapseBranchesInSidebar`
key at all** — that key did not exist as a persisted field until WP03 of
this mission. `FileStore.LoadAll` on this file must resolve
`EffectiveAutoCollapseBranchesInSidebar()` to `true` (the documented
default), not `false` (the JSON zero-value a naive `bool` + `omitempty`
field would silently produce). See `wp03_test.go`
`TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue`.

Also absent: any key introduced by other v0.65.0 missions after v0.64.0.
This fixture is owned by `01PMZ808`'s persistence tests only; it is not a
general-purpose "the app's whole settings surface at v0.64.0" reference.

Falsifiability: reverting WP03's `*bool` shape change (restoring
`AutoCollapseBranchesInSidebar bool` and the old `return
s.AutoCollapseBranchesInSidebar` accessor body) makes
`TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue` fail,
because a plain `bool` field unmarshalled from a file with no matching key
stays at its Go zero-value, `false`.
