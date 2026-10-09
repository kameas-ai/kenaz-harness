import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ContextCompositionPopover from '@/components/chat/ContextCompositionPopover.vue';
import type { UsageComposition } from '@/lib/types';

const composition: UsageComposition = {
  system: 660,
  tools: 220_000,
  history: 4,
  attachments: 120,
  memory: 0,
  cached: 200_000,
  toolsFull: 143,
};

function mountPopover(c: UsageComposition | null) {
  return mount(ContextCompositionPopover, {
    props: { composition: c },
    slots: { default: '<span data-testid="meter">context 22%</span>' },
  });
}

describe('ContextCompositionPopover', () => {
  it('wraps the meter and keeps the breakdown closed until asked', () => {
    const w = mountPopover(composition);
    expect(w.find('[data-testid="meter"]').exists()).toBe(true);
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(false);
  });

  it('lists every part of the last request on click', async () => {
    const w = mountPopover(composition);
    await w.find('[data-testid="context-composition-trigger"]').trigger('click');
    const pop = w.find('[data-testid="context-composition-popover"]');
    expect(pop.exists()).toBe(true);
    expect(w.find('[data-testid="context-composition-system"]').text()).toContain('660');
    expect(w.find('[data-testid="context-composition-tools"]').text()).toContain('143 definitions');
    expect(w.find('[data-testid="context-composition-tools"]').text()).toContain('220k');
    expect(w.find('[data-testid="context-composition-history"]').text()).toContain('4');
    expect(w.find('[data-testid="context-composition-attachments"]').text()).toContain('120');
    expect(w.find('[data-testid="context-composition-cached"]').text()).toContain('200k');
    // system + tools + history + attachments = 220,784 -> "221k"
    expect(w.find('[data-testid="context-composition-total"]').text()).toBe('221k');
    // Memory is zero here: it is not listed (memory that arrives as a
    // message is counted in history).
    expect(w.find('[data-testid="context-composition-memory"]').exists()).toBe(false);
  });

  it('lists memory when the request carried any', async () => {
    const w = mountPopover({ ...composition, memory: 300 });
    await w.trigger('mouseenter');
    expect(w.find('[data-testid="context-composition-memory"]').text()).toContain('300');
  });

  it('a click while hovering pins it open; mouseleave then keeps it; Escape closes', async () => {
    const w = mountPopover(composition);
    const trigger = w.find('[data-testid="context-composition-trigger"]');
    await w.trigger('mouseenter');
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(true);
    await trigger.trigger('click');
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(true);
    await w.trigger('mouseleave');
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(true);
    expect(trigger.attributes('aria-expanded')).toBe('true');
    await trigger.trigger('keydown', { key: 'Escape' });
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(false);
    expect(trigger.attributes('aria-expanded')).toBe('false');
  });

  it('a second click on a pinned popover closes it; focus leaving closes it', async () => {
    const w = mountPopover(composition);
    const trigger = w.find('[data-testid="context-composition-trigger"]');
    await trigger.trigger('click');
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(true);
    await trigger.trigger('click');
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(false);
    await trigger.trigger('click');
    await trigger.trigger('focusout', { relatedTarget: null });
    expect(w.find('[data-testid="context-composition-popover"]').exists()).toBe(false);
  });

  it('is a tooltip described by the trigger, and tells the slot it is open', async () => {
    const w = mount(ContextCompositionPopover, {
      props: { composition },
      slots: { default: `<template #default="{ open }"><span data-testid="slot-state">{{ open }}</span></template>` },
    });
    expect(w.find('[data-testid="slot-state"]').text()).toBe('false');
    await w.find('[data-testid="context-composition-trigger"]').trigger('click');
    const pop = w.find('[data-testid="context-composition-popover"]');
    expect(pop.attributes('role')).toBe('tooltip');
    expect(w.find('[data-testid="context-composition-trigger"]').attributes('aria-describedby')).toBe(pop.attributes('id'));
    expect(w.find('[data-testid="slot-state"]').text()).toBe('true');
  });

  it('shows the schema budget the tools were fitted to, and how many were left out', async () => {
    const w = mountPopover({ ...composition, tools: 7_453, toolsFull: 16, schemaBudget: 8_000, toolsEvicted: 91 });
    await w.trigger('mouseenter');
    const line = w.find('[data-testid="context-composition-budget"]');
    expect(line.exists()).toBe(true);
    expect(line.text()).toContain('7.5k / 8k');
    expect(line.text()).toContain('91 left out');
  });

  it('has no budget line when no budget applied to the call', async () => {
    const w = mountPopover(composition);
    await w.trigger('mouseenter');
    expect(w.find('[data-testid="context-composition-budget"]').exists()).toBe(false);
  });

  it('says there is no breakdown yet when no measured call has completed', async () => {
    const w = mountPopover(null);
    await w.find('[data-testid="context-composition-trigger"]').trigger('click');
    expect(w.find('[data-testid="context-composition-empty"]').exists()).toBe(true);
  });
});
