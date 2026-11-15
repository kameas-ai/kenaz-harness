import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PolicyDefaultNotice from '@/components/permissions/PolicyDefaultNotice.vue';
import PolicyDefaultSetting from '@/components/permissions/PolicyDefaultSetting.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PolicyDefaultView } from '@/lib/types';

// Launch spec L09 R6/R7: the first-run notice says which default is active
// and where to change it; an upgraded install is offered the starter
// policy and keeps its behaviour until it chooses.
function setup(view: PolicyDefaultView) {
  const setPolicyDefault = vi.fn(async () => undefined);
  const dismissPolicyDefaultNotice = vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    settings: {
      getPolicyDefault: vi.fn(async () => view),
      setPolicyDefault,
      dismissPolicyDefaultNotice,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  });
  const provide = { global: { provide: { [HarnessClientKey as symbol]: client } } };
  return { provide, setPolicyDefault, dismissPolicyDefaultNotice };
}

describe('PolicyDefaultNotice', () => {
  it('announces the starter policy on a fresh install and says where to change it', async () => {
    const { provide, dismissPolicyDefaultNotice } = setup({ mode: 'starter', noticeSeen: false, upgraded: false });
    const w = mount(PolicyDefaultNotice, provide);
    await flushPromises();
    expect(w.find('[data-testid="policy-default-notice-title"]').text()).toContain('starter policy is on');
    expect(w.text()).toContain('Settings → Permissions');
    expect(w.text()).toContain('Shell commands, web requests and writes outside the workspace ask you first');
    await w.find('[data-testid="policy-default-notice-dismiss"]').trigger('click');
    await flushPromises();
    expect(dismissPolicyDefaultNotice).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="policy-default-notice"]').exists()).toBe(false);
  });

  it('offers the starter policy on an upgraded install without switching it', async () => {
    const { provide, setPolicyDefault } = setup({ mode: 'legacy', noticeSeen: false, upgraded: true });
    const w = mount(PolicyDefaultNotice, provide);
    await flushPromises();
    expect(w.find('[data-testid="policy-default-notice-title"]').text()).toContain('starter policy is available');
    expect(setPolicyDefault).not.toHaveBeenCalled();
    await w.find('[data-testid="policy-default-notice-accept"]').trigger('click');
    await flushPromises();
    expect(setPolicyDefault).toHaveBeenCalledWith('starter');
  });

  it('keeps the earlier behaviour when the person chooses to', async () => {
    const { provide, setPolicyDefault } = setup({ mode: 'legacy', noticeSeen: false, upgraded: true });
    const w = mount(PolicyDefaultNotice, provide);
    await flushPromises();
    await w.find('[data-testid="policy-default-notice-keep"]').trigger('click');
    await flushPromises();
    expect(setPolicyDefault).toHaveBeenCalledWith('legacy');
  });

  it('shows nothing once answered, or in a workbench', async () => {
    for (const view of [
      { mode: 'starter', noticeSeen: true, upgraded: false },
      { mode: 'workbench', noticeSeen: true, upgraded: false },
    ]) {
      const { provide } = setup(view);
      const w = mount(PolicyDefaultNotice, provide);
      await flushPromises();
      expect(w.find('[data-testid="policy-default-notice"]').exists()).toBe(false);
    }
  });
});

describe('PolicyDefaultSetting', () => {
  it('switches the default from Settings → Permissions', async () => {
    const { provide, setPolicyDefault } = setup({ mode: 'legacy', noticeSeen: true, upgraded: false });
    const w = mount(PolicyDefaultSetting, provide);
    await flushPromises();
    const starter = w.find('[data-testid="policy-default-option-starter"] input');
    expect((starter.element as HTMLInputElement).checked).toBe(false);
    await starter.trigger('change');
    await flushPromises();
    expect(setPolicyDefault).toHaveBeenCalledWith('starter');
  });
});
