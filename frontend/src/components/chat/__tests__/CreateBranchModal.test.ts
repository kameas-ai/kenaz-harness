import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CreateBranchModal from '@/components/chat/CreateBranchModal.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Branch, BranchRecommendedModel } from '@/lib/types';

function mountModal(props: { parentSessionId: string; open: boolean }, seed: Partial<HarnessClient> = {}) {
  return mount(CreateBranchModal, {
    props,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

describe('CreateBranchModal', () => {
  it('renders nothing when open is false', () => {
    const w = mountModal({ parentSessionId: 'p1', open: false });
    expect(w.find('[data-testid="create-branch-modal"]').exists()).toBe(false);
  });

  it('renders the modal when open is true and shows a recommendation chip', async () => {
    const recommendation: BranchRecommendedModel = {
      providerId: 'anthropic',
      modelId: 'claude-haiku-4',
      tier: 'small',
      reason: 'contained_task_smaller_model',
      notes: 'Smaller model for the contained lookup',
    };
    const w = mountModal(
      { parentSessionId: 'p1', open: true },
      {
        branches: {
          list: vi.fn(),
          create: vi.fn(),
          status: vi.fn(),
          merge: vi.fn(),
          abandon: vi.fn(),
          recommendModel: vi.fn().mockResolvedValue(recommendation),
        },
      },
    );
    await flushPromises();
    expect(w.find('[data-testid="create-branch-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="create-branch-recommendation"]').text()).toContain(
      'claude-haiku-4',
    );
  });

  it('emits created on successful submit and emits close', async () => {
    const created: Branch = {
      id: 'br-1',
      parentSessionId: 'p1',
      childSessionId: 'c1',
      kind: 'fork',
      status: 'active',
      title: 'side',
      createdAt: '2026-04-25T00:00:00Z',
      updatedAt: '2026-04-25T00:00:00Z',
    };
    const create = vi.fn().mockResolvedValue(created);
    const w = mountModal(
      { parentSessionId: 'p1', open: true },
      {
        branches: {
          list: vi.fn(),
          create,
          status: vi.fn(),
          merge: vi.fn(),
          abandon: vi.fn(),
          recommendModel: vi.fn().mockResolvedValue({
            providerId: '',
            modelId: '',
            tier: 'medium',
            reason: 'default',
          }),
        },
      },
    );
    await flushPromises();
    await w.find('[data-testid="create-branch-title"]').setValue('side');
    await w.find('[data-testid="create-branch-submit"]').trigger('click');
    await flushPromises();
    expect(create).toHaveBeenCalled();
    expect(w.emitted('created')?.[0]).toEqual([created]);
    expect(w.emitted('close')).toBeTruthy();
  });

  it('shows the cross-provider warning when present', async () => {
    const w = mountModal(
      { parentSessionId: 'p1', open: true },
      {
        branches: {
          list: vi.fn(),
          create: vi.fn(),
          status: vi.fn(),
          merge: vi.fn(),
          abandon: vi.fn(),
          recommendModel: vi.fn().mockResolvedValue({
            providerId: 'openai',
            modelId: 'gpt-4o-mini',
            tier: 'small',
            reason: 'contained_task_smaller_model',
            crossProviderWarning: 'Cross-provider fork: image bytes may lose fidelity.',
          }),
        },
      },
    );
    await flushPromises();
    expect(w.find('[data-testid="create-branch-cross-provider-warn"]').exists()).toBe(true);
  });

  it('emits close on Cancel click', async () => {
    const w = mountModal({ parentSessionId: 'p1', open: true });
    await flushPromises();
    await w.find('[data-testid="create-branch-cancel"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
  });
});
