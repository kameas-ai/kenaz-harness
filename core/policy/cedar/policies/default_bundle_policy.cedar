// default_bundle_policy.cedar — Bundle-install default policy for
// mission `bundle-download-and-verify-01PMZ909` (UNIT-7).
//
// Resource family: `Bundle::"<kind>:<locator>"` — the channel kind and
// the install locator (a local path or a mirror URL) the caller
// supplied. The bundle's own name is not part of the resource id: this
// gate is consulted BEFORE any channel is opened, so the manifest that
// would reveal the name has not been fetched yet (spec §5.6).
// Actions:
//   `Action::"bundle.install"` — Bundle_Install, gated before any
//                                fetch (local_path or http_mirror).
//
// Default posture: permit for the local user (default-allow, single-
// user harness) — matching every other locally-initiated, explicit
// action in this bundle (default_session_export_policy.cedar,
// default_workflows_policy.cedar's run/save). E-001 (spec §12) leaves
// "who may install a bundle, and from which channel URLs" as an
// explicit owner call this file does not resolve; an operator who
// wants a channel allowlist or a deny-by-default posture layers a
// forbid rule in their own `<DataDir>/policy/*.cedar` file — this
// default policy's whole purpose is to make sure that layered rule is
// EVALUATED (no policy naming the action → NotApplicable → enforce()
// maps to allow, silently gating nothing) rather than to encode the
// allowlist itself.
permit (
    principal == User::"local",
    action == Action::"bundle.install",
    resource is Bundle
);
