# Cedar Credential Policy — Rollout Guide

Mission: `cedar-credential-policy-01KQ8TDE`

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | Cedar engine + four resource families + embedded defaults | Merged |
| WP02 | Interactive prompt registry (`prompt.go`) | Merged |
| WP03 | Bash gate (pattern derivation, dangerous detection, grant write) | Merged |
| WP04 | Filesystem gate (canonical path, recipe-dir match, grant write) | Merged |
| WP05 | MCP spawn credential flow | **Partial — pending** |
| WP06 | Credstore (Issue / Use / Revoke, keychain backend) | Merged |
| WP07 | Permissions RPC view (Resolve, ListGrants, RevokeGrant) | Merged |
| WP08 | Frontend permission modals (bash / fs / cred / tool) | Merged |
| WP09 | Settings: PermissionMode toggle (permissive / strict / paranoid) | Merged |
| WP10 | Migration: seed historical bash allowlist on cold start | Merged |
| WP11 | Audit log view (RecentDecisions panel) | Merged |
| WP12 | Integration tests + this acceptance doc | **This branch** |

**WP05 partial gap**: the `mcp_spawn` credential-prompt flow waits for the
credstore WP06 to be fully wired into the MCP spawn hook. Until WP05 is
complete, `mcp_spawn` evaluates to `NotApplicable` and the gate passes
(default-allow). See the dedicated gap section below.

---

## Architecture Overview

The universal permission system gates four resource families through a
Cedar policy engine:

| Family | Cedar resource type | Cedar action |
|---|---|---|
| Bash commands | `BashCommand::"<argv[0]> [argv[1]?]"` | `run_bash_command` |
| Filesystem ops | `FilesystemOp::"<canonical-path>"` | `read_filesystem` / `write_filesystem` |
| Tool dispatch | `Tool::"<server>__<tool>"` | `use_tool` |
| Credential access | `Credential::"<provider>::<purpose>"` | `use_credential` |

Every gate call follows the same cycle:

1. Evaluate via `engine.Evaluate(action, resource, contextAttrs)`.
2. `Allow` → proceed silently.
3. `NotApplicable` → fire the prompt flow (WP02 `Registry.RequestInteractive`).
4. `Deny` → surface `PolicyDeniedError` to the caller; op is NOT invoked.

After the user picks **Allow Once**, a transient in-memory grant covers
the current session. After **Allow Always**, the gate writes a `.cedar`
snippet to `<DataDir>/policy/` and calls `Engine.Reload()`.

---

## Rollout Steps

1. **Back up `<DataDir>/policy/`** if the user has custom `.cedar` files.
2. Deploy the new binary. On first boot, WP10 migration runs automatically:
   - Writes `bash_migrated_NN.cedar` files for each entry in the historical
     `DefaultAllowlist` (`ls`, `cat`, `git`, `python`, etc.).
   - Emits a one-time migration toast ("Bash permissions migrated").
3. Verify `Settings → Permissions → Bash` shows the migrated entries.
4. Confirm `PermissionMode` is set to `permissive` (default). Users who
   want fail-closed can switch to `strict` or `paranoid` in Settings.
5. Test a routine bash command (`git status`). No prompt should appear
   (migrated entry covers it).
6. Test a new bash command (`kubectl get pods`). A permission dialog
   should appear. Pick **Allow Always** and verify the new `.cedar`
   file appears in `<DataDir>/policy/`.
7. Test a filesystem write inside a recipe directory. A write dialog
   should appear. Pick **This directory and below** → verify the
   `like "<dir>/*"` snippet is written.
8. Verify dangerous-command handling: attempt `rm -rf /tmp/test`.
   The dialog should offer Allow Once (not Allow Always). A permanent
   grant must NOT be written.
9. Test credential access: open Settings → Credentials, attempt to
   **Export** a credential. The modal should deny immediately
   (embedded `manual_export` forbid).
10. Test builtin tool dispatch (`kaneaz__bash`). Should be silent
    (pre-permitted by `default_tool_policy.cedar`).
11. Test an MCP tool on first launch (`filesystem__read_file`).
    A prompt should appear. Pick Allow Always. Verify the grant file.
12. Verify `RecentDecisions` panel (Settings → Audit) shows the last
    N decisions with outcome, action, resource, and matched policy.
13. Flip `PermissionMode` to `strict`. Confirm that previously-granted
    commands still pass (permanent grants survive) but un-granted
    commands are now Deny (not NotApplicable).
14. Restore `PermissionMode` to `permissive` after testing.

---

## WP05 Gap: mcp_spawn Credential Flow

**Status**: Partial — not fully exercised in WP12.

When an MCP server spawns and requests a credential for the first time,
the intended flow is:

1. `credstore.Use(credID, "mcp_spawn")` → Cedar evaluates
   `use_credential` with `purpose = "mcp_spawn"`.
2. Default policy: no permit, no forbid → `NotApplicable`.
3. `Registry.RequestInteractive` fires and presents the **Credential
   Permission Modal** (WP08) with `purpose = mcp_spawn`.
4. User picks Allow Once or Allow Always → transient or permanent grant.
5. Second spawn attempt evaluates Allow silently.

Until WP05 is merged, step 3 does not fire. `NotApplicable` propagates
as nil from the gate so the spawn proceeds without prompting. This is
the safe-by-default posture (not blocked, but not explicitly granted).

**Action required before tagging the release**: confirm WP05 is merged
and run the full mcp_spawn smoke manually.

---

## 14-Step Smoke Checklist

Run this checklist on a clean data directory before tagging a release.

- [ ] **1. Migration smoke**: cold start writes `bash_migrated_NN.cedar`
  files and shows migration toast once. `ls`, `git status`, `cat` all
  evaluate `Allow` without a prompt.

- [ ] **2. New bash prompt**: run a command not in the historical list
  (e.g. `kubectl get pods`). Permission dialog appears. Pick Allow
  Always. Subsequent call is silent.

- [ ] **3. Bash dangerous demote**: attempt `rm -rf /tmp/test`. Dialog
  appears but offers only Allow Once (Allow Always button absent or
  disabled). Permanent grant is NOT written.

- [ ] **4. Filesystem read inside recipe-dir**: silent Allow (no dialog).
  Verify via `RecentDecisions`: outcome = `allow`, matched policy
  includes `read_filesystem`.

- [ ] **5. Filesystem write inside recipe-dir**: dialog appears. Pick
  **Allow Always — this directory and below**. Cedar snippet contains
  `like "<dir>/*"`. Subsequent write is silent.

- [ ] **6. Filesystem dangerous path**: attempt write to `~/.ssh`.
  Dialog appears with dangerous-path warning. Allow Always is blocked
  (Cedar forbid wins); only Allow Once is available.

- [ ] **7. Credential provider_call**: silent Allow (pre-permitted by
  embedded default). Verify via audit log.

- [ ] **8. Credential manual_export**: immediate Deny. No dialog.
  Audit entry shows `forbid policy matched`.

- [ ] **9. Credential mcp_spawn**: **WP05 pending** — skip until WP05
  is merged. After WP05: dialog appears on first spawn, resolves, second
  spawn is silent.

- [ ] **10. Builtin tool (kaneaz__bash)**: silent Allow (embedded tool
  policy permits `server_name == "kaneaz"`). No dialog.

- [ ] **11. MCP tool first call**: dialog appears for `filesystem__read_file`.
  Pick Allow Always. Grant file written. Second call silent.

- [ ] **12. Audit panel**: open Settings → Audit. Recent decisions list
  shows all of the above calls with correct outcome, action, resource
  string, and matched-policy ID.

- [ ] **13. PermissionMode=strict**: flip to strict. Un-granted commands
  evaluate Deny (not NotApplicable). Previously-granted commands still
  Allow.

- [ ] **14. PermissionMode=paranoid**: all families Deny unless explicitly
  permitted. Builtin kaneaz tools still Allow (embedded default permits
  them). Reset to permissive after confirming.

---

## Running the Test Suite

```bash
# Go integration tests (all four families + migration + credstore + fs)
go test -race -count=1 \
  ./core/policy/cedar/... \
  ./core/credstore/... \
  ./core/tools/bash/... \
  ./core/tools/fs/...

# Frontend e2e (four modal stubs — bash, fs, cred, tool)
npx vitest run \
  frontend/src/views/sessions/__tests__/PermissionFlow.e2e.spec.ts
```

All tests should be green before tagging.
