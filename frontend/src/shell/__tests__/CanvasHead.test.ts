import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import CanvasHead from '@/shell/CanvasHead.vue';

describe('CanvasHead (FR-001b numbered-section header)', () => {
  it('renders number / section / title and optional subtitle', () => {
    const w = mount(CanvasHead, {
      props: {
        number: '01',
        section: 'SESSIONS',
        title: 'Recent runs',
        subtitle: 'Switch via the rail.',
      },
    });
    expect(w.text()).toContain('01');
    expect(w.text()).toContain('SESSIONS');
    expect(w.text()).toContain('Recent runs');
    expect(w.text()).toContain('Switch via the rail.');
  });

  it('omits subtitle paragraph when not provided', () => {
    const w = mount(CanvasHead, {
      props: { number: '02', section: 'TOOLS', title: 'Tools' },
    });
    expect(w.find('p').exists()).toBe(false);
  });
});
