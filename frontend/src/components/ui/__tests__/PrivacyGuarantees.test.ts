import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import PrivacyGuarantees from '@/components/ui/PrivacyGuarantees.vue';

describe('PrivacyGuarantees (FR-001e)', () => {
  it('renders status + each guarantee with on/off styling', () => {
    const w = mount(PrivacyGuarantees, {
      props: {
        status: 'APPLIED',
        guarantees: [
          { label: 'Credentials never persisted', on: true },
          { label: 'Local-first: zero outbound traffic', on: true },
          { label: 'Org policy applied', on: false },
        ],
      },
    });
    expect(w.text()).toContain('APPLIED');
    expect(w.text()).toContain('Credentials never persisted');
    expect(w.text()).toContain('Local-first: zero outbound traffic');
    expect(w.text()).toContain('Org policy applied');
  });
});
