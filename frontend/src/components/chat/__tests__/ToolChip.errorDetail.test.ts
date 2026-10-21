/**
 * ToolChip — a failed call is always expandable (audit finding HR-7,
 * 2026-10-21). Before, a chip with status "error" and no output rendered
 * as a bare "ERROR" with nothing to click.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import ToolChip from '@/components/chat/ToolChip.vue';
import type { ToolChip as ToolChipModel } from '@/lib/transcript';

async function mountChip(c: ToolChipModel) {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  });
  await r.push('/');
  await r.isReady();
  return mount(ToolChip, { props: { chip: c }, global: { plugins: [r] } });
}

const base: ToolChipModel = {
  callId: 'c1',
  name: 'kenaz__list_dir',
  argsSummary: 'path=<string>',
  status: 'error',
  output: '',
};

describe('ToolChip — error detail', () => {
  it('expands an error chip with no output and says there is no message', async () => {
    const w = await mountChip(base);
    const btn = w.get('[data-testid="tool-chip-c1"]');
    expect(btn.element.tagName).toBe('BUTTON');
    expect(btn.attributes('aria-expanded')).toBe('false');
    await btn.trigger('click');
    const out = w.get('[data-testid="tool-chip-output"]');
    expect(out.text()).toContain('returned no message');
    expect(out.attributes('role')).toBe('alert');
  });

  it('shows the error message when the result carried one', async () => {
    const w = await mountChip({ ...base, output: 'permission denied: /workspace' });
    await w.get('[data-testid="tool-chip-c1"]').trigger('click');
    expect(w.get('[data-testid="tool-chip-output"]').text()).toBe('permission denied: /workspace');
  });

  it('keeps a successful chip with no output inert', async () => {
    const w = await mountChip({ ...base, status: 'ok' });
    expect(w.get('[data-testid="tool-chip-c1"]').element.tagName).toBe('DIV');
  });

  it('shows argument types only, and says so', async () => {
    const w = await mountChip(base);
    const args = w.get('[data-testid="tool-chip-args"]');
    expect(args.text()).toBe('path=<string>');
    expect(args.attributes('title')).toMatch(/types only/i);
  });
});
