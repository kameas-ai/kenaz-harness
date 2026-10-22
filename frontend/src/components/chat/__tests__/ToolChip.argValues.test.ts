/**
 * ToolChip — argument values (audit finding HR-7, 2026-10-21, decision
 * revised to show non-secret values). The backend renders the chip
 * string through core/tooldisplay; the chip shows it verbatim, keeps
 * `<redacted>` markers intact, and puts the full text on hover because
 * the inline span truncates.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import ToolChip from '@/components/chat/ToolChip.vue';
import type { ToolChip as ToolChipModel } from '@/lib/transcript';
import { projectTranscript } from '@/lib/transcript';
import type { Message } from '@/lib/types';

async function mountChip(c: ToolChipModel) {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  });
  await r.push('/');
  await r.isReady();
  return mount(ToolChip, { props: { chip: c }, global: { plugins: [r] } });
}

describe('ToolChip — argument values', () => {
  it('renders non-secret values and keeps redaction markers', async () => {
    const summary = 'kenaz__read_file(path=/workspace/src, token=<redacted>)';
    const w = await mountChip({
      callId: 'c1',
      name: 'kenaz__read_file',
      argsSummary: summary,
      status: 'ok',
      output: '',
    });
    const args = w.get('[data-testid="tool-chip-args"]');
    expect(args.text()).toBe(summary);
    expect(args.text()).toContain('path=/workspace/src');
    expect(args.text()).toContain('token=<redacted>');
    expect(args.attributes('title')).toContain(summary);
    expect(args.attributes('title')).toMatch(/<redacted>/);
  });
});

describe('projectTranscript — chip args source', () => {
  const base: Message = {
    id: 't-1',
    sessionId: 's-1',
    role: 'tool',
    kind: 'tool_call',
    moveIndex: 0,
    turnSpanId: 'u-1',
    createdAt: '2026-10-21T00:00:00Z',
    content: 'kenaz__read_file(path=<string>)',
  };

  function chipArgs(m: Message): string {
    const item = projectTranscript([m])[0];
    if (item.type !== 'trail' || item.steps[0].type !== 'tool') throw new Error('expected chip');
    return item.steps[0].chip.argsSummary;
  }

  it('prefers the backend display string over the persisted content', () => {
    expect(
      chipArgs({
        ...base,
        toolCalls: [
          { id: 'c1', name: 'kenaz__read_file', argsSummary: 'kenaz__read_file(path=/workspace/src)' },
        ],
      }),
    ).toBe('kenaz__read_file(path=/workspace/src)');
  });

  it('falls back to the names-and-types content for older rows', () => {
    expect(
      chipArgs({ ...base, toolCalls: [{ id: 'c1', name: 'kenaz__read_file', argsSummary: '' }] }),
    ).toBe('kenaz__read_file(path=<string>)');
  });
});
