/**
 * updateDownloadEvents — the accelerator half of self-update-repair
 * -01PMUP01 WP03.
 *
 * DC-1: `installLatest` (lib/updateClient.ts, WP02) decides completion by
 * POLLING Update_Status; it never reads a broker event. These three pure
 * functions exist only to repaint the Updates panel's already-true state
 * a little faster than the next poll tick would — see spec §4.1: "the
 * poll decides; the event repaints." Deleting every call site of these
 * functions must change frame rate only, never correctness (DC-1's
 * stated invariant, machine-checked by
 * UpdatesPanel.spec.ts's correctness-neutrality test).
 *
 * Kept pure (status in, status out) rather than inlined into
 * UpdatesPanel.vue's useEventStream handlers so this WP's mutation
 * ("no-op the handler → fails") pins something unit-testable without
 * mounting a component.
 */

import type { UpdateStatus } from './updateClient';

export interface UpdateDownloadProgressPayload {
  bytes: number;
  total: number;
  /** 0-100 integer percent — same DC-2 unit as StatusOutput.DownloadProgress. */
  percent: number;
}

export interface UpdateStagedPayload {
  targetVersion: string;
}

export interface UpdateDownloadFailedPayload {
  err: string;
}

/** Merges a TopicDownloadProgress frame into the current snapshot. */
export function applyDownloadProgressEvent(
  status: UpdateStatus | null,
  payload: UpdateDownloadProgressPayload,
): UpdateStatus | null {
  if (!status) return status;
  return {
    ...status,
    downloadState: 'downloading',
    downloadProgress: payload.percent,
  };
}

/** Merges a TopicDownloadComplete frame into the current snapshot. */
export function applyDownloadCompleteEvent(
  status: UpdateStatus | null,
  _payload: UpdateStagedPayload,
): UpdateStatus | null {
  if (!status) return status;
  return {
    ...status,
    downloadState: 'staged',
    downloadProgress: 100,
    downloadError: undefined,
  };
}

/** Merges a TopicDownloadFailed frame into the current snapshot. */
export function applyDownloadFailedEvent(
  status: UpdateStatus | null,
  payload: UpdateDownloadFailedPayload,
): UpdateStatus | null {
  if (!status) return status;
  return {
    ...status,
    downloadState: 'failed',
    downloadError: payload.err,
  };
}
