import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ModelSizeBadge from '@/views/providers/ModelSizeBadge.vue';

const GB = 1024 * 1024 * 1024;

describe('ModelSizeBadge', () => {
  it('renders nothing when sizeBytes is 0', () => {
    const wrapper = mount(ModelSizeBadge, { props: { sizeBytes: 0 } });
    expect(wrapper.find('[data-testid="model-size-badge"]').exists()).toBe(false);
  });

  it('renders nothing when sizeBytes is undefined', () => {
    const wrapper = mount(ModelSizeBadge, { props: {} });
    expect(wrapper.find('[data-testid="model-size-badge"]').exists()).toBe(false);
  });

  it('renders size without quant when quantLevel is empty', () => {
    const wrapper = mount(ModelSizeBadge, {
      props: { sizeBytes: Math.floor(4.7 * GB) },
    });
    const badge = wrapper.find('[data-testid="model-size-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('4.7 GB');
    expect(badge.text()).not.toContain('·');
  });

  it('renders "4.7 GB · Q4_K_M" format', () => {
    const wrapper = mount(ModelSizeBadge, {
      props: { sizeBytes: Math.floor(4.7 * GB), quantLevel: 'Q4_K_M' },
    });
    expect(wrapper.find('[data-testid="model-size-badge"]').text()).toContain('4.7 GB · Q4_K_M');
  });

  it('shows "Needs more RAM" pill when model exceeds 85% of RAM', () => {
    const wrapper = mount(ModelSizeBadge, {
      props: {
        sizeBytes: Math.floor(14.5 * GB),
        quantLevel: 'Q4_K_M',
        effectiveRamBytes: 16 * GB,
      },
    });
    expect(wrapper.find('[data-testid="needs-more-ram-pill"]').exists()).toBe(true);
  });

  it('does NOT show pill when model fits in RAM', () => {
    const wrapper = mount(ModelSizeBadge, {
      props: {
        sizeBytes: Math.floor(8 * GB),
        effectiveRamBytes: 16 * GB,
      },
    });
    expect(wrapper.find('[data-testid="needs-more-ram-pill"]').exists()).toBe(false);
  });

  it('does NOT show pill when effectiveRamBytes is 0 (unknown)', () => {
    const wrapper = mount(ModelSizeBadge, {
      props: {
        sizeBytes: Math.floor(14.5 * GB),
        effectiveRamBytes: 0,
      },
    });
    expect(wrapper.find('[data-testid="needs-more-ram-pill"]').exists()).toBe(false);
  });
});
