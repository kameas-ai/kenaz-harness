/**
 * pendingApprovalsDialog — open/close state for the pending-approvals modal
 * (ml-producer-01MLPRD01 WP06).
 *
 * The settings banner's "N items need your approval" issue (settingsIssues.ts)
 * opens the modal from its fix button. The banner contract is "run() resolves
 * when the fix landed, then every provider re-runs", so open() returns a
 * promise that resolves when the modal closes: the banner then re-reads the
 * hub and the issue disappears once nothing is pending (or stays, because
 * closing the modal never approves anything).
 */
import { reactive, readonly } from 'vue';

const state = reactive({ open: false });
let waiters: Array<() => void> = [];

/** Read-only view of the dialog state, for the component that mounts it. */
export const pendingApprovalsDialog = readonly(state);

/** Opens the modal; resolves when it closes. */
export function openPendingApprovalsDialog(): Promise<void> {
  state.open = true;
  return new Promise<void>((resolve) => {
    waiters.push(resolve);
  });
}

/** Closes the modal and releases every open() waiter. */
export function closePendingApprovalsDialog(): void {
  state.open = false;
  const w = waiters;
  waiters = [];
  for (const resolve of w) resolve();
}
