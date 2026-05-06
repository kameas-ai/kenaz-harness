/**
 * LongSessionNudge — unit tests (v0.5.6 memory-trust-signals).
 *
 * Covers:
 *   1. Renders the banner with the three action buttons.
 *   2. "Branch from here" emits 'branch'.
 *   3. "+ New session" emits 'newSession'.
 *   4. "Dismiss for this session" emits 'dismiss'.
 *   5. data-testid attributes are present for all interactive elements.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import LongSessionNudge from '@/components/chat/LongSessionNudge.vue';

function mountNudge() {
  return mount(LongSessionNudge);
}

describe('LongSessionNudge (v0.5.6)', () => {
  it('renders the banner element', () => {
    const w = mountNudge();
    expect(w.find('[data-testid="long-session-nudge"]').exists()).toBe(true);
  });

  it('renders all three action buttons', () => {
    const w = mountNudge();
    expect(w.find('[data-testid="long-session-nudge-branch"]').exists()).toBe(true);
    expect(w.find('[data-testid="long-session-nudge-new-session"]').exists()).toBe(true);
    expect(w.find('[data-testid="long-session-nudge-dismiss"]').exists()).toBe(true);
  });

  it('"Branch from here" emits "branch"', async () => {
    const w = mountNudge();
    await w.find('[data-testid="long-session-nudge-branch"]').trigger('click');
    expect(w.emitted('branch')).toBeTruthy();
    expect(w.emitted('branch')!.length).toBe(1);
  });

  it('"+ New session" emits "newSession"', async () => {
    const w = mountNudge();
    await w.find('[data-testid="long-session-nudge-new-session"]').trigger('click');
    expect(w.emitted('newSession')).toBeTruthy();
    expect(w.emitted('newSession')!.length).toBe(1);
  });

  it('"Dismiss for this session" emits "dismiss"', async () => {
    const w = mountNudge();
    await w.find('[data-testid="long-session-nudge-dismiss"]').trigger('click');
    expect(w.emitted('dismiss')).toBeTruthy();
    expect(w.emitted('dismiss')!.length).toBe(1);
  });

  it('has role="note" on the banner', () => {
    const w = mountNudge();
    expect(w.find('[data-testid="long-session-nudge"]').attributes('role')).toBe('note');
  });
});
