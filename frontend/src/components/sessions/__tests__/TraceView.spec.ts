import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import TraceView from '@/components/sessions/TraceView.vue';

describe('TraceView', () => {
  it('renders nothing when actualProvider is empty', () => {
    const w = mount(TraceView, { props: { actualProvider: '', actualModel: '' } });
    expect(w.find('[data-testid="trace-view-fallback"]').exists()).toBe(false);
  });

  it('renders nothing when actualProvider is absent', () => {
    const w = mount(TraceView, { props: {} });
    expect(w.find('[data-testid="trace-view-fallback"]').exists()).toBe(false);
  });

  it('renders the provider when actualProvider is set', () => {
    const w = mount(TraceView, {
      props: { actualProvider: 'openrouter', actualModel: 'openai/gpt-4o' },
    });
    const el = w.find('[data-testid="trace-view-fallback"]');
    expect(el.exists()).toBe(true);
    expect(el.text()).toContain('openrouter');
    expect(el.text()).toContain('openai/gpt-4o');
  });

  it('renders provider without model separator when actualModel is absent', () => {
    const w = mount(TraceView, {
      props: { actualProvider: 'bedrock' },
    });
    const el = w.find('[data-testid="trace-view-fallback"]');
    expect(el.exists()).toBe(true);
    expect(el.text()).toContain('bedrock');
    expect(el.text()).not.toContain('/');
  });

  it('has role note for accessibility', () => {
    const w = mount(TraceView, {
      props: { actualProvider: 'openrouter', actualModel: '' },
    });
    const el = w.find('[data-testid="trace-view-fallback"]');
    expect(el.attributes('role')).toBe('note');
  });
});
