import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ScopePicker from '@/components/contexts/ScopePicker.vue';
import type { AttachmentScopeKind } from '@/lib/types';

describe('ScopePicker', () => {
  it('renders all three options by default', () => {
    const w = mount(ScopePicker, {
      props: { modelValue: 'session' as AttachmentScopeKind },
    });
    expect(w.find('[data-testid=scope-picker-global]').exists()).toBe(true);
    expect(w.find('[data-testid=scope-picker-project]').exists()).toBe(true);
    expect(w.find('[data-testid=scope-picker-session]').exists()).toBe(true);
  });

  it('hides the project radio when showProject is false', () => {
    const w = mount(ScopePicker, {
      props: {
        modelValue: 'session' as AttachmentScopeKind,
        showProject: false,
      },
    });
    expect(w.find('[data-testid=scope-picker-project]').exists()).toBe(false);
  });

  it('hides the session radio when showSession is false', () => {
    const w = mount(ScopePicker, {
      props: {
        modelValue: 'global' as AttachmentScopeKind,
        showSession: false,
      },
    });
    expect(w.find('[data-testid=scope-picker-session]').exists()).toBe(false);
  });

  it('reflects the active selection via aria-checked', () => {
    const w = mount(ScopePicker, {
      props: { modelValue: 'project' as AttachmentScopeKind },
    });
    const project = w.find('[data-testid=scope-picker-project]');
    expect(project.attributes('aria-checked')).toBe('true');
    const global = w.find('[data-testid=scope-picker-global]');
    expect(global.attributes('aria-checked')).toBe('false');
  });

  it('emits update:modelValue with the picked kind', async () => {
    const w = mount(ScopePicker, {
      props: { modelValue: 'session' as AttachmentScopeKind },
    });
    await w.find('[data-testid=scope-picker-global]').trigger('click');
    const emitted = w.emitted('update:modelValue');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['global']);
  });

  it('uses only design tokens — no raw hex/rgba', () => {
    const w = mount(ScopePicker, {
      props: { modelValue: 'session' as AttachmentScopeKind },
    });
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
