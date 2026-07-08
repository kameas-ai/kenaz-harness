/**
 * BootstrapStep.spec.ts — WP07 (context-bootstrap-harness-integration).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BootstrapStep from '@/components/context/BootstrapStep.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function setup(sources: string[]) {
  const client = createFakeHarnessClient();
  const runFn = vi.spyOn(client.onboarding, 'runBootstrap').mockResolvedValue('run-xyz');
  const wrapper = mount(BootstrapStep, {
    props: { sources },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, runFn };
}

describe('BootstrapStep', () => {
  it('seeds the interview checklist from the sources prop (all selected)', () => {
    const { wrapper } = setup(['gmail', 'slack']);
    expect(wrapper.find('[data-testid="bootstrap-src-gmail"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="bootstrap-src-slack"]').exists()).toBe(true);
  });

  it('Start kicks a run over the consented sources and emits done', async () => {
    const { wrapper, runFn } = setup(['gmail', 'slack']);
    await wrapper.find('[data-testid="bootstrap-start"]').trigger('click');
    await flushPromises();
    expect(runFn).toHaveBeenCalledWith(['gmail', 'slack']);
    expect(wrapper.emitted('done')?.[0]).toEqual(['run-xyz']);
  });

  it('deselecting a connector drops it from the consented set', async () => {
    const { wrapper, runFn } = setup(['gmail', 'slack']);
    await wrapper.find('[data-testid="bootstrap-src-slack"]').setValue(false);
    await wrapper.find('[data-testid="bootstrap-start"]').trigger('click');
    await flushPromises();
    expect(runFn).toHaveBeenCalledWith(['gmail']);
  });

  it('disables Start when no connector is selected', async () => {
    const { wrapper } = setup(['gmail']);
    await wrapper.find('[data-testid="bootstrap-src-gmail"]').setValue(false);
    const btn = wrapper.find('[data-testid="bootstrap-start"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
  });
});
