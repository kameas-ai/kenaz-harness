import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import CheatSheetModal from '../CheatSheetModal.vue';
import { SHORTCUTS } from '@/lib/shortcuts/registry';

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  });
}

describe('CheatSheetModal', () => {
  it('does not render when open=false', () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: false },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    expect(wrapper.find('[data-testid="cheat-sheet-modal"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('renders when open=true', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: true },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    expect(wrapper.find('[data-testid="cheat-sheet-modal"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('emits close when close button is clicked', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: true },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    await wrapper.find('[data-testid="cheat-sheet-close"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
    wrapper.unmount();
  });

  it('emits close when backdrop is clicked', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: true },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    // Click on the backdrop (the outermost dialog div).
    const dialog = wrapper.find('[data-testid="cheat-sheet-modal"]');
    await dialog.trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
    wrapper.unmount();
  });

  it('renders all shortcut categories', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: true },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    // Verify each unique category is rendered.
    const categories = [...new Set(SHORTCUTS.map((s) => s.category))];
    for (const cat of categories) {
      expect(
        wrapper.find(`[data-testid="cheat-sheet-category-${cat.toLowerCase()}"]`).exists(),
        `category ${cat} should be present`,
      ).toBe(true);
    }
    wrapper.unmount();
  });

  it('renders a row for each shortcut', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: { open: true },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    for (const sc of SHORTCUTS) {
      expect(
        wrapper.find(`[data-testid="cheat-sheet-row-${sc.id}"]`).exists(),
        `row ${sc.id} should exist`,
      ).toBe(true);
    }
    wrapper.unmount();
  });

  it('shows override binding when provided', async () => {
    const wrapper = mount(CheatSheetModal, {
      props: {
        open: true,
        overrides: { 'chat.send': 'Cmd+Shift+Enter' },
      },
      global: { plugins: [makeRouter()] },
      attachTo: document.body,
    });
    // The KeycapDisplay for chat.send should receive the override, not the default.
    const row = wrapper.find('[data-testid="cheat-sheet-row-chat.send"]');
    expect(row.exists()).toBe(true);
    // aria-label on the keycap span shows the binding.
    expect(row.html()).toContain('Cmd+Shift+Enter');
    wrapper.unmount();
  });
});
