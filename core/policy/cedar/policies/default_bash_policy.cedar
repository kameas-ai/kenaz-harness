// default_bash_policy.cedar — Bash family default bundle
// for mission `cedar-credential-policy-01KQ8TDE` (WP01).
//
// Resource family: `BashCommand::"<argv[0]> <argv[1]?>"`
//                  pattern derivation per spec FR-014.
// Action:          `Action::"run_bash_command"`
// Context attrs:   `pattern` (string), `working_dir` (string),
//                  `dangerous_tier` (bool — true when the derived
//                  pattern is in the spec §2 dangerous list).
//
// Default posture (spec FR-003):
//   This file is intentionally empty of permits and forbids. EVERY
//   bash command starts as NotApplicable. The universal prompt flow
//   (WP02 + WP03) takes over on NotApplicable; the user's "Allow
//   always" answer writes a sibling `bash_allow_<sanitized>.cedar`
//   file under `<DataDir>/policy/` that composes with this bundle.
//
//   Migration (WP10) seeds the historical `DefaultAllowlist` contents
//   as `bash_allow_*.cedar` files on first boot so existing users
//   don't see a prompt-storm for `git status`, `ls`, `cat`, etc.
//
// Header-only by design — do NOT add permits / forbids here. Per-
// command grants belong in their own files so the user can revoke
// them individually via the Settings → Bash permissions panel.
