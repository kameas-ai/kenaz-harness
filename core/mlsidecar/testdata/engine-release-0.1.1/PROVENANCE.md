# kenaz-ml 0.1.1 published-release fixtures

Fetched 2026-10-07 from the **prod** engine channel the harness release
build pins (`.github/workflows/release.yml`, `KENAZ_ML_ENGINE_VERSION` /
`KENAZ_ML_ENGINE_CHANNEL_BASE`). Consumed by
`core/mlsidecar/published_release_test.go`, which runs the harness's own
verifier stack (baked release anchor → `core/trust` engine →
`DefaultEngineVerifier`, `SigningRequired`) over these bytes with no
network access.

| File | Source URL | sha256 of the fixture file |
|---|---|---|
| `index.json` | `https://downloads.kameas.ai/kenaz-ml/index.json` | `2b69280cd06519507e0c52218cf75911037c0c565fa5040ca14b61530c175f60` |
| `kenaz-ml-0.1.1-darwin-arm64.dmg.sig` | `https://downloads.kameas.ai/kenaz-ml/0.1.1/kenaz-ml-0.1.1-darwin-arm64.dmg.sig` (CDN `last-modified: Wed, 07 Oct 2026 16:13:01 GMT`) | `5efdfd90e42cee8443b43dcf437252e57ad683cc3d84c1f6f3504af0440826e0` |

Both files are byte-for-byte what the CDN served; do not edit them. The
`.sig` is the raw 64-byte detached ed25519 signature `kenaz-ml-sign sign`
wrote over `mlsidecar.EngineManifest("0.1.1",
"kenaz-ml-0.1.1-darwin-arm64.dmg", <sha256>, nil).SigningPayload()`.

The 189 MB DMG itself is NOT a fixture. It was downloaded once on
2026-10-07 and checked out of band: 189403636 bytes (== `size_bytes`,
== the CDN `content-length`), sha256
`235188a2cdde7043cb302272e29728c5b96a85bb06e3ebcac4291be40c242738`
(== the index `sha256`). A one-off `go run` (not committed) also drove
the real `mlsidecar.Install` against the live `http_mirror` channel with
a trust store seeded only by `BakedReleaseAnchor()`: download → verify →
mount → copy → quarantine-clear succeeded with `verified=true`,
`provenance=kameas-channel-manifest`.

To refresh for a new pinned engine version: re-fetch both files from the
URLs above (with the new version), update the table, and bump the
version/artifact constants in `published_release_test.go`.
