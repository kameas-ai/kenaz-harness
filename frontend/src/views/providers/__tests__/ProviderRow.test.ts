import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ProviderRow from '@/views/providers/ProviderRow.vue';
import type { Provider } from '@/lib/types';

function makeProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 'anth-personal',
    name: 'Anthropic Personal',
    tier: 'personal',
    model: 'claude-sonnet',
    source: 'personal',
    validated: false,
    ...overrides,
  };
}

describe('ProviderRow', () => {
  it('renders unvalidated pill when validated=false', () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider() },
      // Render inside a table to avoid Vue invalid-parent warning.
      global: {},
      attachTo: document.createElement('table'),
    });
    expect(wrapper.text()).toContain('UNVALIDATED');
    expect(wrapper.text()).not.toContain('VALIDATED ');
  });

  it('renders validated pill when validated=true', () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider({ validated: true }) },
    });
    expect(wrapper.text()).toContain('VALIDATED');
  });

  it('shows "bundle" tag for bundle source and hides Remove button', () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider({ source: 'bundle' }) },
    });
    expect(wrapper.text()).toContain('bundle');
    const buttons = wrapper.findAll('button');
    expect(buttons.find((b) => b.text() === 'Remove')).toBeUndefined();
    expect(buttons.find((b) => b.text() === 'Test')).toBeTruthy();
  });

  it('exposes Remove for personal source and emits remove event', async () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider() },
    });
    const remove = wrapper.findAll('button').find((b) => b.text() === 'Remove');
    expect(remove).toBeTruthy();
    await remove!.trigger('click');
    expect(wrapper.emitted('remove')).toBeTruthy();
    expect(wrapper.emitted('remove')![0]).toEqual(['anth-personal']);
  });

  it('emits test event when Test button clicked', async () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider() },
    });
    const test = wrapper.findAll('button').find((b) => b.text() === 'Test');
    expect(test).toBeTruthy();
    await test!.trigger('click');
    expect(wrapper.emitted('test')).toBeTruthy();
    expect(wrapper.emitted('test')![0]).toEqual(['anth-personal']);
  });

  it('disables Test button while testing', () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider(), testing: true },
    });
    const test = wrapper
      .findAll('button')
      .find((b) => b.text().includes('Testing'));
    expect(test).toBeTruthy();
    expect(test!.attributes('disabled')).toBeDefined();
  });

  // served-mode-is-a-real-mode-01PMZ707 WP03, spec.md §5.3: "verify every
  // write affordance is actually behind [readOnly]" — confirmed to exist
  // (ProvidersView.served.test.ts), not confirmed sufficient, until now.
  // A personal-source provider is the adversarial case: isPersonal would
  // be true on its own, so this pins that `readOnly` overrides it rather
  // than Edit/Remove leaking through for exactly the row type that would
  // otherwise show them.
  it('readOnly hides Test, Edit and Remove even for a personal-source provider', () => {
    const wrapper = mount(ProviderRow, {
      props: { provider: makeProvider({ source: 'personal' }), readOnly: true },
    });
    const buttons = wrapper.findAll('button').map((b) => b.text());
    expect(buttons).not.toContain('Test');
    expect(buttons.find((t) => t.includes('Edit'))).toBeUndefined();
    expect(buttons).not.toContain('Remove');
  });
});
