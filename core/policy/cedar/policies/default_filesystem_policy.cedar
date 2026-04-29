// default_filesystem_policy.cedar — Filesystem family default bundle
// for mission `cedar-credential-policy-01KQ8TDE` (WP01).
//
// Resource family: `FilesystemOp::"<canonical_path>"`
//                  Path is `filepath.Abs` + `filepath.Clean`'d before
//                  becoming part of the UID (spec FR-017).
// Actions:         `Action::"read_filesystem"`, `Action::"write_filesystem"`
// Context attrs:
//   - `canonical_path`        (string) — same as the resource id, for
//                              `like` comparisons against directory
//                              prefixes.
//   - `recipe_dir_match`      (bool)   — true when the canonical path
//                              is contained in any recipe-declared
//                              allowed-directory at evaluation time.
//   - `dangerous_tier`        (bool)   — true when the path is in the
//                              spec §2 dangerous-fs list.
//
// Default posture (spec FR-004):
//   - READ inside a recipe-declared allowed-directory → permit.
//     Reads are pre-permitted so chat-runner state-reads + filesystem
//     MCP reads inside recipe scope are silent.
//   - WRITE inside a recipe-dir → no rule (NotApplicable). The prompt
//     flow (WP02 + WP04) takes over so writes are explicitly granted.
//   - Anything outside recipe-dirs (read OR write) → no rule
//     (NotApplicable). Same prompt path.
//
// Path canonicalization happens in `core/tools/fs/canonical.go` (WP04).
// Traversal segments (`..`) are rejected with `ErrInvalidPath` BEFORE
// the request reaches this engine, so this policy can assume
// `canonical_path` is already absolute and cleaned.

permit (
    principal == User::"local",
    action == Action::"read_filesystem",
    resource is FilesystemOp
) when {
    context.recipe_dir_match == true
};
