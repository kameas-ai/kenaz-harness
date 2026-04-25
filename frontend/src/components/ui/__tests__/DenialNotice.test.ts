import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import DenialNotice from '@/components/ui/DenialNotice.vue';

describe('DenialNotice (FR-012, SC-009)', () => {
  it('renders all four typed fields', () => {
    const w = mount(DenialNotice, {
      props: {
        denial: {
          policyId: 'org-default',
          clauseId: 'no-network',
          violatingInput: 'http://api.example.com',
          remediation: 'Configure an allowed network destination.',
        },
      },
    });
    const text = w.text();
    expect(text).toContain('org-default');
    expect(text).toContain('no-network');
    expect(text).toContain('http://api.example.com');
    expect(text).toContain('Configure an allowed network destination.');
  });
});
