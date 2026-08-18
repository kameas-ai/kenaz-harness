/**
 * usePermissionReconcile — shared reconnect/reload reconciliation for the
 * four family-specific permission modals (Bash / Filesystem / Credential /
 * Tool).
 *
 * (consent-surfaces-truth-01PMTR01 WP03 / dead-code-audit finding A11)
 *
 * The pause a permission prompt represents lives in the harness PROCESS —
 * a goroutine blocked on a channel inside `cedar.Registry`, no different
 * from the confirm-each pause `ConfirmToolModal.vue` reconciles, or the
 * elicitation pause `AskUserQuestion.vue` reconciles. A frontend reload,
 * hot-reload, or WS reconnect forgets the modal's in-memory queue but does
 * NOT un-park the goroutine — without this, the user watches a chat that
 * has silently stalled until the registry's 5-minute timeout fires and
 * fail-closed denies the request.
 *
 * This is a near-copy of `ConfirmToolModal.vue`'s `reconcile()`
 * (:243-258) generalised to the four permission families, written once
 * and shared rather than pasted four times (tasks.md WP03). Contract,
 * copied verbatim in spirit:
 *   - fail-silent: a failed listPending() call changes nothing — "I could
 *     not reach the harness" must never render as "nothing is pending".
 *   - a DIFF, not a union: rows the server no longer has (resolved from
 *     another window, or by a decision that raced this one) are dropped,
 *     not just added to. The diff is scoped to rows queued at fetch time:
 *     a request that parks DURING the round trip is absent from the
 *     server snapshot through no fault of its own and must survive.
 *   - de-duped by request_id.
 *   - respects the caller's MAX_QUEUE: a family already at capacity
 *     auto-denies any pending row a fetch would otherwise add, mirroring
 *     the overflow behaviour each modal's live event handler already
 *     applies to newly-ARRIVING requests.
 */
import type { Ref } from 'vue';
import type { HarnessClient } from './harnessClient';
import type { PermissionRequest } from './types';

/**
 * Builds a `reconcile()` function scoped to one permission family and one
 * modal's queue ref. Call the returned function from `onMounted`.
 *
 * @param client   The harness client (Wails / served / fake).
 * @param family   The family string this modal owns — 'bash' | 'fs' |
 *                 'cred' | 'tool' — used to filter the server's
 *                 all-families snapshot down to this modal's rows.
 * @param queue    The modal's own reactive queue ref.
 * @param maxQueue The modal's MAX_QUEUE cap.
 */
export function usePermissionReconcile(
  client: HarnessClient,
  family: string,
  queue: Ref<PermissionRequest[]>,
  maxQueue: number,
): () => Promise<void> {
  return async function reconcile(): Promise<void> {
    // Snapshot the queue's ids BEFORE the await. The staleness diff below
    // may only judge rows that were already queued when the fetch was
    // issued — a row the live `<family>:permission-pending` topic delivers
    // WHILE listPending() is in flight cannot be in the snapshot we are
    // about to receive (it parked after the server answered), so treating
    // its absence as "the server resolved it" drops a genuinely-parked
    // request. The goroutine stays parked, the turn hangs, and the
    // registry's 5-minute timeout fail-closed denies it — which is
    // precisely the bug this composable exists to prevent, reintroduced in
    // a one-round-trip window at mount.
    const atFetch = new Set(queue.value.map((r) => r.request_id));
    let pending: PermissionRequest[];
    try {
      pending = await client.permissions.listPending();
    } catch {
      // Best-effort. The live `<family>:permission-pending` topic still
      // delivers anything that parks from here on, and any row we could
      // not fetch is still parked server-side — so the safe move is to
      // change nothing.
      return;
    }

    const mine = pending.filter((r) => r.family === family);
    const live = new Set(mine.map((r) => r.request_id));

    // Drop stale rows: resolved elsewhere while this modal was gone.
    // Scoped to rows present at fetch time (see `atFetch` above) so a
    // mid-flight live arrival is never mistaken for a resolved row.
    const isStale = (r: PermissionRequest) =>
      atFetch.has(r.request_id) && !live.has(r.request_id);
    if (queue.value.some(isStale)) {
      queue.value = queue.value.filter((r) => !isStale(r));
    }

    // Add rows the server has that this queue does not, respecting
    // MAX_QUEUE the same way a live arrival would: overflow auto-denies
    // rather than growing the queue past its cap.
    const known = new Set(queue.value.map((r) => r.request_id));
    let next = queue.value;
    let mutated = false;
    for (const row of mine) {
      if (known.has(row.request_id)) continue;
      if (next.length >= maxQueue) {
        void client.permissions.resolve(row.request_id, 'deny').catch(() => {});
        continue;
      }
      if (!mutated) {
        next = [...next];
        mutated = true;
      }
      next.push(row);
      known.add(row.request_id);
    }
    if (mutated) {
      queue.value = next;
    }
  };
}
