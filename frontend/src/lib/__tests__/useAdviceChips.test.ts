/**
 * useAdviceChips.test.ts — laya-advisors-01LAYA001 WP07.
 *
 * Runs in served mode (no window.runtime) via dispatchServedEvent, the
 * same test-injection hook useEventStream.test.ts / useEventToasts.test.ts
 * use (vitest's jsdom/happy-dom environment has no window.runtime by
 * default, so useEventStream already falls back to the served-event bus).
 *
 * Coverage:
 *  1. A recommendation for the active session becomes activeChip.
 *  2. A recommendation for a DIFFERENT session is ignored (session scoping).
 *  3. accept() calls client.advice.respond("accept") and clears activeChip.
 *  4. dismiss() calls client.advice.respond("dismiss") and clears activeChip.
 *  5. An auto-act notice for the active kind clears any pending chip for
 *     that kind and populates autoActedBanner.
 *  6. Switching sessions clears both activeChip and autoActedBanner.
 */
import { describe, it, expect } from 'vitest';
import { defineComponent, h, ref, nextTick, type Ref } from 'vue';
import { mount } from '@vue/test-utils';
import { useAdviceChips } from '@/lib/useAdviceChips';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { AdviceAutoActedPayload, AdviceChipPayload } from '@/lib/types';

function makeChip(overrides: Partial<AdviceChipPayload> = {}): AdviceChipPayload {
  return {
    session_id: 'sess-1',
    kind_id: 'branch_now',
    decision: true,
    confidence: 88,
    model: 'heuristic/branch-regex-v1',
    rung: 'heuristic',
    title: 'Branch this off?',
    body: 'This looks like a separable thread.',
    prompt_version: 'v1',
    ...overrides,
  };
}

type RespondFn = (sessionID: string, kindID: string, action: string) => Promise<string>;

function mountHook(sessionID: Ref<string>, respond: RespondFn = () => Promise.resolve('')) {
  let hook: ReturnType<typeof useAdviceChips> | null = null;
  const Comp = defineComponent({
    setup() {
      hook = useAdviceChips({ sessionID });
      return () => h('div');
    },
  });
  const w = mount(Comp, {
    global: {
      plugins: [
        {
          install: (app) =>
            provideFakeClient(app, {
              advice: { respond } as never,
            }),
        },
      ],
    },
  });
  return {
    w,
    get hook() {
      if (!hook) throw new Error('no hook');
      return hook;
    },
  };
}

describe('useAdviceChips', () => {
  it('sets activeChip for a recommendation matching the active session', async () => {
    const sessionID = ref<string>('sess-1');
    const { hook } = mountHook(sessionID);
    dispatchServedEvent('advice:recommendation', makeChip({ session_id: 'sess-1' }));
    await nextTick();
    expect(hook.activeChip.value?.kind_id).toBe('branch_now');
  });

  it('ignores a recommendation for a different session', async () => {
    const sessionID = ref<string>('sess-1');
    const { hook } = mountHook(sessionID);
    dispatchServedEvent('advice:recommendation', makeChip({ session_id: 'sess-OTHER' }));
    await nextTick();
    expect(hook.activeChip.value).toBeNull();
  });

  it('accept() calls client.advice.respond("accept") and clears activeChip', async () => {
    const sessionID = ref<string>('sess-1');
    const calls: Array<[string, string, string]> = [];
    const respond = (s: string, k: string, a: string) => {
      calls.push([s, k, a]);
      return Promise.resolve('child-session-1');
    };
    const { hook } = mountHook(sessionID, respond);
    dispatchServedEvent('advice:recommendation', makeChip());
    await nextTick();
    expect(hook.activeChip.value).not.toBeNull();

    const childID = await hook.accept();
    expect(childID).toBe('child-session-1');
    expect(hook.activeChip.value).toBeNull();
    expect(calls).toEqual([['sess-1', 'branch_now', 'accept']]);
  });

  it('dismiss() calls client.advice.respond("dismiss") and clears activeChip', async () => {
    const sessionID = ref<string>('sess-1');
    const calls: Array<[string, string, string]> = [];
    const respond = (s: string, k: string, a: string) => {
      calls.push([s, k, a]);
      return Promise.resolve('');
    };
    const { hook } = mountHook(sessionID, respond);
    dispatchServedEvent('advice:recommendation', makeChip());
    await nextTick();

    await hook.dismiss();
    expect(hook.activeChip.value).toBeNull();
    expect(calls).toEqual([['sess-1', 'branch_now', 'dismiss']]);
  });

  it('an auto-act notice for the pending chip kind clears the chip and sets the banner', async () => {
    const sessionID = ref<string>('sess-1');
    const { hook } = mountHook(sessionID);
    dispatchServedEvent('advice:recommendation', makeChip({ kind_id: 'branch_now' }));
    await nextTick();
    expect(hook.activeChip.value).not.toBeNull();

    const autoActed: AdviceAutoActedPayload = {
      session_id: 'sess-1',
      kind_id: 'branch_now',
      child_session_id: 'child-session-2',
      confidence: 95,
      model: 'heuristic/branch-regex-v1',
    };
    dispatchServedEvent('advice:auto-acted', autoActed);
    await nextTick();

    expect(hook.activeChip.value).toBeNull();
    expect(hook.autoActedBanner.value?.child_session_id).toBe('child-session-2');
  });

  it('switching sessions clears both activeChip and autoActedBanner', async () => {
    const sessionID = ref<string>('sess-1');
    const { hook } = mountHook(sessionID);
    dispatchServedEvent('advice:recommendation', makeChip());
    await nextTick();
    expect(hook.activeChip.value).not.toBeNull();

    sessionID.value = 'sess-2';
    await nextTick();
    expect(hook.activeChip.value).toBeNull();
    expect(hook.autoActedBanner.value).toBeNull();
  });
});
