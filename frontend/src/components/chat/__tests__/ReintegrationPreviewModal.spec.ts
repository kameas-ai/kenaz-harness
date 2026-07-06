import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ReintegrationPreviewModal from '@/components/chat/ReintegrationPreviewModal.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { BranchReintegrationProposal } from '@/lib/types';

function mountModal(
  props: { open: boolean; branchSessionId: string; parentSessionId: string },
  seed: Partial<HarnessClient> = {},
) {
  return mount(ReintegrationPreviewModal, {
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

const defaultProposal: BranchReintegrationProposal = {
  proposedSummary: 'Branch found that the answer is 42.',
  tokenCount: 128,
  model: 'claude-haiku-4',
};

// Helper: build a branches seed with overrides
function branchesSeed(overrides: Partial<HarnessClient['branches']> = {}): Partial<HarnessClient> {
  return {
    branches: {
      list: vi.fn().mockResolvedValue([]),
      create: vi.fn(),
      createExplicit: vi.fn(),
      status: vi.fn(),
      merge: vi.fn(),
      abandon: vi.fn(),
      recommendModel: vi.fn(),
      proposeReintegrationSummary: vi.fn().mockResolvedValue(defaultProposal),
      commitReintegration: vi.fn().mockResolvedValue(undefined),
      setAdvisorDismissed: vi.fn(),
      listWithBranchTree: vi.fn(),
      ...overrides,
    },
  };
}

describe('ReintegrationPreviewModal', () => {
  it('does not render when open is false', () => {
    const w = mountModal({ open: false, branchSessionId: 'b1', parentSessionId: 'p1' });
    expect(w.find('[data-testid="reintegration-preview-modal"]').exists()).toBe(false);
  });

  it('shows "Generating…" on initial open before proposal arrives', async () => {
    // Never resolves during this test
    const propose = vi.fn(() => new Promise<BranchReintegrationProposal>(() => {}));
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({ proposeReintegrationSummary: propose }),
    );
    // Don't flush — should still be loading
    expect(w.find('[data-testid="reintegration-generating"]').exists()).toBe(true);
  });

  it('renders editable textarea with proposal summary after load', async () => {
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed(),
    );
    await flushPromises();
    const textarea = w.find<HTMLTextAreaElement>('[data-testid="reintegration-summary-textarea"]');
    expect(textarea.exists()).toBe(true);
    expect(textarea.element.value).toBe(defaultProposal.proposedSummary);
  });

  it('shows token count and model name', async () => {
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed(),
    );
    await flushPromises();
    expect(w.find('[data-testid="reintegration-tokens"]').text()).toContain('128');
    expect(w.find('[data-testid="reintegration-model"]').text()).toContain('claude-haiku-4');
  });

  it('calls commitReintegration with edited summary on confirm', async () => {
    const commit = vi.fn().mockResolvedValue(undefined);
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({ commitReintegration: commit }),
    );
    await flushPromises();

    // Edit the textarea
    const textarea = w.find<HTMLTextAreaElement>('[data-testid="reintegration-summary-textarea"]');
    await textarea.setValue('Edited: the answer is still 42.');

    await w.find('[data-testid="reintegration-confirm"]').trigger('click');
    await flushPromises();

    expect(commit).toHaveBeenCalledWith({
      branchSessionId: 'b1',
      finalSummaryText: 'Edited: the answer is still 42.',
      wasEdited: true,
    });
  });

  it('emits committed and close after successful confirm', async () => {
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed(),
    );
    await flushPromises();
    await w.find('[data-testid="reintegration-confirm"]').trigger('click');
    await flushPromises();
    expect(w.emitted('committed')).toBeTruthy();
    expect(w.emitted('close')).toBeTruthy();
  });

  it('emits close on Cancel click without calling commit', async () => {
    const commit = vi.fn().mockResolvedValue(undefined);
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({ commitReintegration: commit }),
    );
    await flushPromises();
    await w.find('[data-testid="reintegration-cancel"]').trigger('click');
    expect(w.emitted('close')).toBeTruthy();
    expect(commit).not.toHaveBeenCalled();
  });

  it('shows empty-branch affordance when proposedSummary is empty', async () => {
    const emptyProposal: BranchReintegrationProposal = {
      proposedSummary: '',
      tokenCount: 0,
      model: 'claude-haiku-4',
    };
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({
        proposeReintegrationSummary: vi.fn().mockResolvedValue(emptyProposal),
      }),
    );
    await flushPromises();
    expect(w.find('[data-testid="reintegration-empty"]').exists()).toBe(true);
    expect(w.find('[data-testid="reintegration-confirm"]').exists()).toBe(false);
  });

  it('shows error + retry button when propose fails', async () => {
    const propose = vi.fn().mockRejectedValue(new Error('network error'));
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({ proposeReintegrationSummary: propose }),
    );
    await flushPromises();
    expect(w.find('[data-testid="reintegration-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="reintegration-error"]').text()).toContain('network error');
    expect(w.find('[data-testid="reintegration-retry"]').exists()).toBe(true);
  });

  it('re-invokes propose on Regenerate click', async () => {
    const propose = vi.fn().mockResolvedValue(defaultProposal);
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed({ proposeReintegrationSummary: propose }),
    );
    await flushPromises();
    expect(propose).toHaveBeenCalledTimes(1);

    await w.find('[data-testid="reintegration-regenerate"]').trigger('click');
    await flushPromises();
    expect(propose).toHaveBeenCalledTimes(2);
  });

  it('emits close on Esc keydown', async () => {
    const w = mountModal(
      { open: true, branchSessionId: 'b1', parentSessionId: 'p1' },
      branchesSeed(),
    );
    await flushPromises();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(w.emitted('close')).toBeTruthy();
  });
});
