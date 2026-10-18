/**
 * AdviceAutoActedBanner.vue component tests — laya-advisors-01LAYA001
 * WP07 (review blocker 1: the auto-act notice must actually render).
 *
 * Coverage:
 *  1. Renders the kind-specific copy and both actions.
 *  2. "View branch" emits open with the child session id.
 *  3. "Dismiss" emits dismiss.
 *  4. "View branch" is absent when there is no child session id.
 *  5. An unknown kind id falls back to a generic notice.
 *
 * The mounted-SessionsView delivery proof (dispatching the real
 * advice:auto-acted topic and asserting this component actually renders
 * from a live view, not just that the composable computed a value) lives
 * in SessionsView.adviceAutoActed.test.ts.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import AdviceAutoActedBanner from '@/components/chat/AdviceAutoActedBanner.vue';
import type { AdviceAutoActedPayload } from '@/lib/types';

function makeNotice(overrides: Partial<AdviceAutoActedPayload> = {}): AdviceAutoActedPayload {
  return {
    session_id: 'sess-1',
    kind_id: 'branch_now',
    child_session_id: 'child-sess-1',
    confidence: 95,
    model: 'heuristic/branch-regex-v1',
    ...overrides,
  };
}

describe('AdviceAutoActedBanner', () => {
  it('renders the banner with body text and both actions', () => {
    const w = mount(AdviceAutoActedBanner, { props: { notice: makeNotice() } });
    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(true);
    expect(w.find('[data-testid="advice-auto-acted-banner-body"]').text()).toContain(
      'branched off automatically',
    );
    expect(w.find('[data-testid="advice-auto-acted-banner-open"]').exists()).toBe(true);
    expect(w.find('[data-testid="advice-auto-acted-banner-dismiss"]').exists()).toBe(true);
  });

  it('emits open with the child session id when "View branch" is clicked', async () => {
    const notice = makeNotice({ child_session_id: 'child-sess-42' });
    const w = mount(AdviceAutoActedBanner, { props: { notice } });
    await w.find('[data-testid="advice-auto-acted-banner-open"]').trigger('click');
    const emitted = w.emitted('open');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toBe('child-sess-42');
  });

  it('emits dismiss when "Dismiss" is clicked', async () => {
    const w = mount(AdviceAutoActedBanner, { props: { notice: makeNotice() } });
    await w.find('[data-testid="advice-auto-acted-banner-dismiss"]').trigger('click');
    expect(w.emitted('dismiss')).toBeTruthy();
  });

  it('hides "View branch" when there is no child session id', () => {
    const w = mount(AdviceAutoActedBanner, { props: { notice: makeNotice({ child_session_id: '' }) } });
    expect(w.find('[data-testid="advice-auto-acted-banner-open"]').exists()).toBe(false);
  });

  it('falls back to a generic notice for an unknown kind id', () => {
    const w = mount(AdviceAutoActedBanner, {
      props: { notice: makeNotice({ kind_id: 'some_future_kind' }) },
    });
    expect(w.find('[data-testid="advice-auto-acted-banner-body"]').text()).toContain(
      'took an action on this session automatically',
    );
  });
});
