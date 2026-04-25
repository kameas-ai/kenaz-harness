import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import EventStreamRow from '@/components/ui/EventStreamRow.vue';

describe('EventStreamRow (FR-001c, plan §5.2)', () => {
  it('renders timestamp, category, subject, trailing', () => {
    const w = mount(EventStreamRow, {
      props: {
        timestamp: '2026-04-25T00:00:00Z',
        category: 'FILESYSTEM',
        subject: 'config/policy.toml',
        trailing: '4 KB',
      },
    });
    expect(w.text()).toContain('2026-04-25T00:00:00Z');
    expect(w.text()).toContain('FILESYSTEM');
    expect(w.text()).toContain('config/policy.toml');
    expect(w.text()).toContain('4 KB');
  });

  it('truncates subjects > 256 bytes mid-string', () => {
    const long = 'a'.repeat(300);
    const w = mount(EventStreamRow, {
      props: {
        timestamp: 't',
        category: 'NETWORK',
        subject: long,
      },
    });
    expect(w.text()).toContain('…');
    expect(w.text().includes('aaaaa')).toBe(true);
  });

  it('uses the category-token CSS variable for color (no raw hex)', () => {
    const w = mount(EventStreamRow, {
      props: {
        timestamp: 't',
        category: 'PROCESS',
        subject: 's',
      },
    });
    const html = w.html();
    expect(html).toContain('var(--ok)');
    // Defence-in-depth for invariant #4: assert no hex literal sneaks through.
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}/);
  });
});
