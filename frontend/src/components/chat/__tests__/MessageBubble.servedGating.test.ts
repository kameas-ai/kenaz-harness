/**
 * MessageBubble.servedGating.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP04. `Branches_*` is GATED (ten methods; list-but-not-merge is "half a
 * flow"), so the "Branch from this turn" button that fires
 * client.branches.createExplicit must not render under served mode, even
 * when the branchingPolish flag is on and the message otherwise qualifies.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageBubble from '@/components/chat/MessageBubble.vue';

let servedModeFlag = false;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return { ...actual, isServedMode: () => servedModeFlag };
});

describe('MessageBubble — branch-from-turn under served mode (WP04)', () => {
  beforeEach(() => {
    localStorage.removeItem('harness.feature.branchingPolish');
  });

  afterEach(() => {
    localStorage.removeItem('harness.feature.branchingPolish');
    servedModeFlag = false;
  });

  it('renders the branch button in desktop mode (control)', () => {
    servedModeFlag = false;
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'reply', messageId: 'm-1' },
    });
    expect(w.find('[data-testid="branch-from-turn-button"]').exists()).toBe(true);
  });

  it('does NOT render the branch button under served mode, even with the flag on and a qualifying message', () => {
    servedModeFlag = true;
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'reply', messageId: 'm-2' },
    });
    // *Falsify*: drop `&& !servedMode` from the v-if → this goes red.
    expect(w.find('[data-testid="branch-from-turn-button"]').exists()).toBe(false);
  });
});
