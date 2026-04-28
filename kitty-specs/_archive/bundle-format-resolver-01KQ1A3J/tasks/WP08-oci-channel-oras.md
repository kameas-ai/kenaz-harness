---
work_package_id: "WP08"
title: "OCI channel via oras-go/v2 with docker-credential-helpers"
dependencies:
  - "WP05"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 8 - Channels (oci)"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP08 – OCI channel via oras-go/v2

## Goal

Implement the `oci` distribution channel using `oras.land/oras-go/v2` for push/pull/copy and `oras-go/v2/registry/remote/credentials` for auth via `docker-credential-helpers`. Support Distribution-Spec v1.1 Referrers API for signature/SBOM lookup. This is the strategic channel for the harness ecosystem.

## Spec references

- FR-003 Distribution-channel abstraction (oci is the strategic v1 channel)
- FR-007 Optional signature verification (Referrers API surfaces signatures)
- FR-012 Pre-flight validation
- C-002 No covert network egress

## Plan references

- Plan §2 `core/bundle/channels/oci/` (oras-go/v2, auth via docker-credential-helpers, Referrers API)
- Plan §3.4 Channel interface
- Plan §6.2 secrets-keychain (auth refs)
- Plan §6.5 sigstore-go integration shape
- Plan §8 R1 (oras-go primary-source verification gap), R5 (registry auth quirks)
- Research D2, D3, D9

## Cross-mission dependencies

- **secrets-keychain**: OCI auth via docker config / OS keychain via credential helpers.
- **a2a-signed-cards-trust** (downstream WP10): consumes `LookupSignatures` output for verification.

## Subtasks

- T001 30-minute primary-source verification pass on `oras-go/v2` API surface (Plan R1, Research Next Action 8). Pin a specific minor version in `go.mod`.
- T002 Implement `oci.Channel.Reachable(ctx)`: probe the registry's `/v2/` endpoint via oras-go's `remote.Registry`. Return `ErrChannelUnreachable` on auth or network failure.
- T003 Implement `oci.Channel.Fetch(ctx, coord, sink)`: pull the artifact manifest by digest (or tag → digest), stream blobs to sink. Verify the registry-returned digest matches the requested digest; mismatch returns `ErrIntegrityMismatch` (channel-layer defense; resolver re-verifies again).
- T004 Implement `oci.Channel.LookupSignatures(ctx, coord)` using the OCI v1.1 Referrers API (`/v2/<name>/referrers/<digest>`). Filter referrers by `artifactType` matching known signature media types (sigstore bundle, in-toto attestation). Fall back to tag-based referrers when the registry doesn't support the API (oras-go handles this transparently — verify and document).
- T005 Auth via `oras-go/v2/registry/remote/credentials`: read `~/.docker/config.json`, integrate `credsStore` and `credHelpers` (macOS Keychain, Windows Credential Manager, `pass` on Linux). Honor a `secrets.Resolver` ref for a per-channel override.
- T006 Document registry quirks (ECR token expiry, GAR short-lived tokens, GHCR PAT scopes) in package doc; add a CI integration-test fixture per registry kind where feasible.

## Acceptance criteria

- Integration test against a Zot or `registry:2` fixture container pulls an artifact and lists its referrers.
- Auth via docker config works on macOS, Linux, and Windows runners (or documented gap with a follow-up issue).
- Tag-based referrer fallback exercised against a registry without v1.1 support; results are equivalent to the API path.
- Channel-layer digest verification rejects a corrupted blob with `ErrIntegrityMismatch`.
- A bundle pulled by tag is resolved to its digest before any blob fetch.

## Files to create/modify

- `core/bundle/channels/oci/oci.go` (new — Channel impl)
- `core/bundle/channels/oci/auth.go` (new — docker-credential-helpers wiring)
- `core/bundle/channels/oci/referrers.go` (new — Referrers API + tag fallback)
- `go.mod`, `go.sum` (add `oras.land/oras-go/v2` pinned version)

## Definition of done

- All acceptance criteria pass.
- oras-go version pinned and primary-source verification recorded in WP notes (R1 mitigation).
- Package imports only `channels`, `cache`, `errors`, `secrets` interface, and oras-go.
- Registry-quirk documentation reviewed and committed.
- Cross-mission contract with a2a-signed-cards-trust (WP10) is satisfied — `LookupSignatures` output shape is the input to verification.
