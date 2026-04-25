import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import LiveRateIndicator from '@/components/ui/LiveRateIndicator.vue';

describe('LiveRateIndicator (FR-001g)', () => {
  it('formats the rate with default precision = 1', () => {
    const w = mount(LiveRateIndicator, { props: { rate: 0.42, unit: 'e/s' } });
    expect(w.text()).toContain('0.4');
    expect(w.text()).toContain('e/s');
  });

  it('respects an override precision', () => {
    const w = mount(LiveRateIndicator, {
      props: { rate: 12.345, unit: 'req/s', precision: 2 },
    });
    expect(w.text()).toContain('12.35');
  });
});
