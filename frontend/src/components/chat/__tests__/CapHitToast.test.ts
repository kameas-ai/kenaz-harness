import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CapHitToast from '@/components/chat/CapHitToast.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function mountWith(props: {
  visible: boolean;
  reason: string;
  used: number;
  limit: number;
}) {
  const bumpAndResume = vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    dials: {
      get: async () => ({}),
      set: async () => undefined,
      getEffective: async () =>
        ({} as unknown as Awaited<
          ReturnType<typeof client.dials.getEffective>
        >),
      bumpAndResume,
    },
  });
  const wrapper = mount(CapHitToast, {
    props: {
      visible: props.visible,
      runID: 'run-1',
      reason: props.reason,
      limit: props.limit,
      used: props.used,
    },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, bumpAndResume };
}

describe('CapHitToast', () => {
  it('does not render when visible=false', () => {
    const { wrapper } = mountWith({
      visible: false,
      reason: 'max_tokens_per_run',
      limit: 1000,
      used: 1500,
    });
    expect(wrapper.find('[data-testid="cap-hit-toast"]').exists()).toBe(false);
  });

  it('renders with default bump amount derived from reason', async () => {
    const { wrapper } = mountWith({
      visible: true,
      reason: 'max_tokens_per_run',
      limit: 1000,
      used: 1500,
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="cap-hit-toast"]').exists()).toBe(true);
    const input = wrapper.find('[data-testid="cap-hit-amount"]')
      .element as HTMLInputElement;
    // Token reason defaults to 5000.
    expect(input.value).toBe('5000');
  });

  it('emits resumed after a successful bump', async () => {
    const { wrapper, bumpAndResume } = mountWith({
      visible: true,
      reason: 'max_tokens_per_run',
      limit: 1000,
      used: 1500,
    });
    await flushPromises();
    await wrapper.find('[data-testid="cap-hit-resume"]').trigger('click');
    await flushPromises();
    expect(bumpAndResume).toHaveBeenCalledTimes(1);
    expect(wrapper.emitted('resumed')).toBeTruthy();
  });

  it('emits close when dismissed', async () => {
    const { wrapper } = mountWith({
      visible: true,
      reason: 'max_cost_usd_per_run',
      limit: 1,
      used: 1.5,
    });
    await flushPromises();
    await wrapper.find('[data-testid="cap-hit-close"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('disables resume when bump amount is zero', async () => {
    const { wrapper } = mountWith({
      visible: true,
      reason: 'max_tokens_per_run',
      limit: 1000,
      used: 1500,
    });
    await flushPromises();
    await wrapper.find('[data-testid="cap-hit-amount"]').setValue('0');
    const btn = wrapper.find('[data-testid="cap-hit-resume"]')
      .element as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
  });
});
