/**
 * Launch spec L09 — the words for the host policy default, shared by the
 * first-run notice (PolicyDefaultNotice.vue) and the Settings → Permissions
 * control (PolicyDefaultSetting.vue) so the two can never describe it
 * differently. Must match core/rpc/policy_default.go and the fs gate /
 * network resolver behaviour it names.
 */
export const POLICY_DEFAULT_WHERE = 'Settings → Permissions';

export const STARTER_SUMMARY =
  'The agent reads files and edits files in its workspace without asking. ' +
  'Shell commands, web requests and writes outside the workspace ask you first.';

export const LEGACY_SUMMARY =
  'Every file read and write asks you first, unless you allowed it before. ' +
  'Web requests run without asking.';

export const WORKBENCH_SUMMARY =
  'Inside a workbench the virtual machine is the boundary. Shell commands are on ' +
  'unless your organization turns them off in Fleet.';
