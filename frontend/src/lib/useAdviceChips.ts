/**
 * useAdviceChips — laya-advisors-01LAYA001 WP07's chip-delivery
 * composable. Subscribes to the `advice:recommendation` /
 * `advice:auto-acted` backend topics (core/rpc/views/agentgraph/chat/
 * advice_hook.go) for one session and exposes:
 *   - activeChip: the most recent chip-eligible recommendation for THIS
 *     session (null when none, or after accept/dismiss/replacement).
 *   - autoActedBanner: the most recent auto-act notice for this session
 *     (spec §3: "auto-act and notify").
 *   - accept() / dismiss(): call Advice_Respond and clear activeChip.
 *
 * Session scoping is done HERE (payload.session_id === sessionID), not
 * assumed from the transport: useEventStream forwards every event on the
 * topic regardless of which session it belongs to (desktop Wails events
 * are process-wide; served mode's D-705 filter scopes by the WS
 * connection's subscribed session, which already matches the active
 * session on both transports in practice, but re-checking here keeps
 * this composable correct even if a future multi-window desktop build
 * ever shares one event bus across sessions).
 */

import { ref, watch, type Ref } from 'vue';
import { useHarnessClient } from './useHarnessAPI';
import { useEventStream } from './useEventStream';
import type { AdviceAutoActedPayload, AdviceChipPayload } from './types';

export interface UseAdviceChipsOptions {
  /** Reactive session id to scope chips to. */
  sessionID: Ref<string>;
}

export interface UseAdviceChipsReturn {
  /** The currently visible chip for this session, or null. */
  activeChip: Ref<AdviceChipPayload | null>;
  /** The most recent auto-act notice for this session, or null. */
  autoActedBanner: Ref<AdviceAutoActedPayload | null>;
  /** Accept the active chip. Resolves to the new child session id for a branch_now accept. */
  accept(): Promise<string>;
  /** Dismiss the active chip. */
  dismiss(): Promise<void>;
  /** Clears the auto-act banner (the banner is a one-shot notice, not persistent). */
  clearAutoActedBanner(): void;
}

export function useAdviceChips(opts: UseAdviceChipsOptions): UseAdviceChipsReturn {
  const client = useHarnessClient();
  const activeChip = ref<AdviceChipPayload | null>(null) as Ref<AdviceChipPayload | null>;
  const autoActedBanner = ref<AdviceAutoActedPayload | null>(
    null,
  ) as Ref<AdviceAutoActedPayload | null>;

  // useEventStream wires both the desktop (Wails EventsOn) and served
  // (WS) transports transparently and cleans itself up on unmount.
  useEventStream<AdviceChipPayload>('advice:recommendation', (payload) => {
    if (payload.session_id !== opts.sessionID.value) return;
    activeChip.value = payload;
  });

  useEventStream<AdviceAutoActedPayload>('advice:auto-acted', (payload) => {
    if (payload.session_id !== opts.sessionID.value) return;
    autoActedBanner.value = payload;
    // An auto-act for a kind supersedes any still-pending chip for that
    // same kind (the decision has already been made for the user —
    // spec §3's tier-gated auto-act path).
    if (activeChip.value?.kind_id === payload.kind_id) {
      activeChip.value = null;
    }
  });

  // A chip belongs to the session it was published for; switching
  // sessions must not leave a stale chip rendered against the new one.
  watch(opts.sessionID, () => {
    activeChip.value = null;
    autoActedBanner.value = null;
  });

  async function accept(): Promise<string> {
    const chip = activeChip.value;
    if (!chip) return '';
    activeChip.value = null;
    return client.advice.respond(chip.session_id, chip.kind_id, 'accept');
  }

  async function dismiss(): Promise<void> {
    const chip = activeChip.value;
    if (!chip) return;
    activeChip.value = null;
    await client.advice.respond(chip.session_id, chip.kind_id, 'dismiss');
  }

  function clearAutoActedBanner() {
    autoActedBanner.value = null;
  }

  return { activeChip, autoActedBanner, accept, dismiss, clearAutoActedBanner };
}
