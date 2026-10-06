/**
 * kenaz__fork_conversation affordance on the transcript's tool chip
 * (model fork tool WP02/WP03). The fork the model creates must be one
 * click away from the tool call that created it, via the same
 * /sessions/:id route the branches sidebar's "Open" uses.
 */

import { describe, it, expect } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import ToolChip from '@/components/chat/ToolChip.vue';
import type { ToolChip as ToolChipModel } from '@/lib/transcript';
import { forkBranchSessionId, isForkToolName } from '@/lib/forkTool';

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/sessions/:id?', component: { template: '<div />' } },
    ],
  });
}

const OK_OUTPUT = JSON.stringify({
  branch_id: 'br-1',
  branch_session_id: 'child-sess-1',
  title: 'Tangent',
  handoff_seeded: true,
  message: 'Created conversation branch "Tangent".',
});

function chip(overrides: Partial<ToolChipModel>): ToolChipModel {
  return {
    callId: 'call-f',
    name: 'kenaz__fork_conversation',
    argsSummary: 'title:string',
    status: 'ok',
    output: OK_OUTPUT,
    ...overrides,
  };
}

async function mountChip(c: ToolChipModel) {
  const r = router();
  await r.push('/');
  await r.isReady();
  const w = mount(ToolChip, { props: { chip: c }, global: { plugins: [r] } });
  return { w, r };
}

describe('ToolChip — fork_conversation "Open branch"', () => {
  it('renders a link to the branch session and navigates to it', async () => {
    const { w, r } = await mountChip(chip({}));
    const link = w.get('[data-testid="tool-chip-open-branch"]');
    expect(link.text()).toBe('Open branch');
    expect(link.attributes('href')).toBe('/sessions/child-sess-1');
    await link.trigger('click');
    await flushPromises();
    expect(r.currentRoute.value.fullPath).toBe('/sessions/child-sess-1');
  });

  it('accepts the dispatch-stripped tool name too', async () => {
    const { w } = await mountChip(chip({ name: 'fork_conversation' }));
    expect(w.find('[data-testid="tool-chip-open-branch"]').exists()).toBe(true);
  });

  it('offers no link while running, on a failed fork, or for other tools', async () => {
    for (const c of [
      chip({ status: 'running', output: '' }),
      chip({ output: JSON.stringify({ error: 'fork_failed', message: 'boom' }) }),
      chip({ output: 'not json' }),
      chip({ name: 'kenaz__save_artifact' }),
    ]) {
      const { w } = await mountChip(c);
      expect(w.find('[data-testid="tool-chip-open-branch"]').exists()).toBe(false);
    }
  });

  it('still links a partially-successful fork (branch exists, seed failed)', async () => {
    const { w } = await mountChip(
      chip({
        output: JSON.stringify({
          error: 'handoff_not_seeded',
          message: 'branch exists',
          branch_id: 'br-2',
          branch_session_id: 'child-2',
        }),
      }),
    );
    expect(w.get('[data-testid="tool-chip-open-branch"]').attributes('href')).toBe('/sessions/child-2');
  });
});

describe('forkTool helpers', () => {
  it('recognises both name spellings only', () => {
    expect(isForkToolName('kenaz__fork_conversation')).toBe(true);
    expect(isForkToolName('fork_conversation')).toBe(true);
    expect(isForkToolName('kenaz__subagent_dispatch')).toBe(false);
  });

  it('rejects ids that are not opaque tokens', () => {
    expect(forkBranchSessionId(JSON.stringify({ branch_session_id: '../settings' }))).toBe('');
    expect(forkBranchSessionId(JSON.stringify({ branch_session_id: 42 }))).toBe('');
    expect(forkBranchSessionId('')).toBe('');
    expect(forkBranchSessionId('null')).toBe('');
    expect(forkBranchSessionId(OK_OUTPUT)).toBe('child-sess-1');
  });
});
