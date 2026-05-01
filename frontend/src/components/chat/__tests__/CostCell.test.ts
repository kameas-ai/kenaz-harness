import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import CostCell from '@/components/chat/CostCell.vue';
import type { SessionUsage } from '@/lib/types';

function makeUsage(overrides: Partial<SessionUsage> = {}): SessionUsage {
  return {
    promptTokens: 1000,
    completionTokens: 500,
    totalTokens: 1500,
    costUsd: 0.0045,
    costSource: 'derived',
    messageCount: 3,
    pricingDataDate: '2026-04-27',
    ...overrides,
  };
}

describe('CostCell', () => {
  it('renders nothing when usage is null', () => {
    const w = mount(CostCell, { props: { usage: null } });
    expect(w.find('[data-testid="session-cost-cell"]').exists()).toBe(false);
  });

  it('renders nothing when messageCount is 0', () => {
    const w = mount(CostCell, {
      props: { usage: makeUsage({ messageCount: 0 }) },
    });
    expect(w.find('[data-testid="session-cost-cell"]').exists()).toBe(false);
  });

  it('renders token count only when costSource is unknown', () => {
    const w = mount(CostCell, {
      props: {
        usage: makeUsage({ costSource: 'unknown', costUsd: 0 }),
      },
    });
    const el = w.find('[data-testid="session-cost-cell"]');
    expect(el.exists()).toBe(true);
    expect(el.text()).toContain('1.5k');
    expect(el.text()).not.toContain('$');
  });

  it('renders exact cost for provider source', () => {
    const w = mount(CostCell, {
      props: { usage: makeUsage({ costSource: 'provider', costUsd: 0.0045 }) },
    });
    const text = w.find('[data-testid="session-cost-cell"]').text();
    expect(text).not.toContain('~');
    expect(text).toContain('$');
    expect(text).toContain('1.5k');
  });

  it('renders approximate cost with tilde for derived source', () => {
    const w = mount(CostCell, {
      props: { usage: makeUsage({ costSource: 'derived', costUsd: 0.0045 }) },
    });
    const text = w.find('[data-testid="session-cost-cell"]').text();
    expect(text).toContain('~');
    expect(text).toContain('$');
    expect(text).toContain('1.5k');
  });

  it('renders approximate cost with tilde for mixed source', () => {
    const w = mount(CostCell, {
      props: { usage: makeUsage({ costSource: 'mixed', costUsd: 0.012 }) },
    });
    const text = w.find('[data-testid="session-cost-cell"]').text();
    expect(text).toContain('~');
    expect(text).toContain('$');
  });

  it('formats large token counts in millions', () => {
    const w = mount(CostCell, {
      props: {
        usage: makeUsage({ totalTokens: 1_500_000, costSource: 'unknown', costUsd: 0 }),
      },
    });
    const text = w.find('[data-testid="session-cost-cell"]').text();
    expect(text).toContain('1.5M');
  });

  it('uses no raw hex color literals', () => {
    const w = mount(CostCell, { props: { usage: makeUsage() } });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
