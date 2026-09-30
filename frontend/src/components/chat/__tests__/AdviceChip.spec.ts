/**
 * AdviceChip.vue component tests — laya-advisors-01LAYA001 WP07.
 *
 * Coverage:
 *  1. Renders title/body and both action buttons.
 *  2. Accept emits 'accept' with the chip payload.
 *  3. Dismiss emits 'dismiss' with the chip payload.
 *  4. Accept button label is kind-specific (spec §2's consumer-action table).
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import AdviceChip from '@/components/chat/AdviceChip.vue';
import type { AdviceChipPayload } from '@/lib/types';

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

describe('AdviceChip', () => {
  it('renders the title, body, and both action buttons', () => {
    const w = mount(AdviceChip, { props: { chip: makeChip() } });
    expect(w.find('[data-testid="advice-chip"]').exists()).toBe(true);
    expect(w.find('[data-testid="advice-chip-title"]').text()).toBe('Branch this off?');
    expect(w.find('[data-testid="advice-chip-body"]').text()).toBe(
      'This looks like a separable thread.',
    );
    expect(w.find('[data-testid="advice-chip-accept"]').exists()).toBe(true);
    expect(w.find('[data-testid="advice-chip-dismiss"]').exists()).toBe(true);
  });

  it('emits accept with the chip payload when the accept button is clicked', async () => {
    const chip = makeChip();
    const w = mount(AdviceChip, { props: { chip } });
    await w.find('[data-testid="advice-chip-accept"]').trigger('click');
    const emitted = w.emitted('accept');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toEqual(chip);
  });

  it('emits dismiss with the chip payload when the dismiss button is clicked', async () => {
    const chip = makeChip({ session_id: 'sess-2' });
    const w = mount(AdviceChip, { props: { chip } });
    await w.find('[data-testid="advice-chip-dismiss"]').trigger('click');
    const emitted = w.emitted('dismiss');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toEqual(chip);
  });

  it.each([
    ['branch_now', 'Branch it off'],
    ['compact_now', 'Compact now'],
    ['escalate_model', 'Switch model'],
  ])('renders the kind-specific accept label for %s', (kindID, label) => {
    const w = mount(AdviceChip, { props: { chip: makeChip({ kind_id: kindID }) } });
    expect(w.find('[data-testid="advice-chip-accept"]').text()).toBe(label);
  });

  it('falls back to a generic accept label for an unknown kind id', () => {
    const w = mount(AdviceChip, { props: { chip: makeChip({ kind_id: 'some_future_kind' }) } });
    expect(w.find('[data-testid="advice-chip-accept"]').text()).toBe('Accept');
  });

  it('sets data-kind-id for CSS/test targeting per kind', () => {
    const w = mount(AdviceChip, { props: { chip: makeChip({ kind_id: 'compact_now' }) } });
    expect(w.find('[data-testid="advice-chip"]').attributes('data-kind-id')).toBe('compact_now');
  });
});
