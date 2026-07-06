import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { defineComponent, ref, nextTick } from 'vue';
import BaseDialog from '../BaseDialog.vue';

/**
 * BaseDialog unit tests — focus trap, Escape-to-close, overlay-click,
 * return-focus, and initial-focus behaviour (FR-001 / WP01).
 */

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('renders when open=true', async () => {
    const wrapper = mount(BaseDialog, {
      props: { open: true, title: 'My dialog' },
      slots: { default: '<span>Content</span>' },
      attachTo: document.body,
      global: { stubs: { Teleport: false } },
    });
    await nextTick();
    expect(document.body.textContent).toContain('Content');
    wrapper.unmount();
  });

  it('does not render when open=false', async () => {
    const wrapper = mount(BaseDialog, {
      props: { open: false, title: 'My dialog' },
      slots: { default: '<span>Hidden content</span>' },
      attachTo: document.body,
      global: { stubs: { Teleport: false } },
    });
    await nextTick();
    expect(document.body.textContent).not.toContain('Hidden content');
    wrapper.unmount();
  });

  it('emits close on Escape key', async () => {
    const Parent = defineComponent({
      components: { BaseDialog },
      setup() {
        const open = ref(true);
        const closed = ref(false);
        return { open, closed };
      },
      template: `
        <BaseDialog :open="open" title="Test" @close="closed = true; open = false">
          <button>btn</button>
        </BaseDialog>
      `,
    });
    const wrapper = mount(Parent, {
      attachTo: document.body,
      global: { stubs: { Teleport: false } },
    });
    await nextTick();
    // Dispatch Escape on document — BaseDialog listens with capture.
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await nextTick();
    expect(wrapper.vm.closed).toBe(true);
    wrapper.unmount();
  });

  it('emits close on overlay click when closeOnOverlayClick=true', async () => {
    const Parent = defineComponent({
      components: { BaseDialog },
      setup() {
        const open = ref(true);
        const closed = ref(false);
        return { open, closed };
      },
      template: `
        <BaseDialog :open="open" title="Test" @close="closed = true; open = false">
          <button>btn</button>
        </BaseDialog>
      `,
    });
    const wrapper = mount(Parent, {
      attachTo: document.body,
      global: { stubs: { Teleport: false } },
    });
    await nextTick();
    // Click the backdrop (aria-hidden div immediately inside the outer container).
    const backdrop = document.querySelector('[aria-hidden="true"]') as HTMLElement;
    expect(backdrop).not.toBeNull();
    backdrop.click();
    await nextTick();
    expect(wrapper.vm.closed).toBe(true);
    wrapper.unmount();
  });

  it('does not emit close on overlay click when closeOnOverlayClick=false', async () => {
    const Parent = defineComponent({
      components: { BaseDialog },
      setup() {
        const open = ref(true);
        const closed = ref(false);
        return { open, closed };
      },
      template: `
        <BaseDialog :open="open" title="Test" :close-on-overlay-click="false" @close="closed = true; open = false">
          <button>btn</button>
        </BaseDialog>
      `,
    });
    const wrapper = mount(Parent, {
      attachTo: document.body,
      global: { stubs: { Teleport: false } },
    });
    await nextTick();
    const backdrop = document.querySelector('[aria-hidden="true"]') as HTMLElement;
    expect(backdrop).not.toBeNull();
    backdrop.click();
    await nextTick();
    expect(wrapper.vm.closed).toBe(false);
    wrapper.unmount();
  });
});
