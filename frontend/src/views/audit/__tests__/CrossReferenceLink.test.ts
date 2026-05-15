/**
 * CrossReferenceLink unit tests (WP06).
 *
 * Verifies:
 * 1. Known kinds render as buttons (navigable).
 * 2. Unknown kinds render as spans (no navigation).
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import CrossReferenceLink from '@/components/audit/CrossReferenceLink.vue';

describe('CrossReferenceLink', () => {
  it('renders a button for known session_id kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'session_id', id: '01HFXY8B5VJ6T6T7AXJF9JT9F1' },
      global: { stubs: { RouterLink: true } },
    });
    expect(w.find('button').exists()).toBe(true);
    expect(w.text()).toContain('session');
  });

  it('renders a span for unknown kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'unknown_thing_id', id: 'abc123' },
    });
    expect(w.find('span').exists()).toBe(true);
    expect(w.find('button').exists()).toBe(false);
  });

  it('renders a button for artifact_id kind', () => {
    const w = mount(CrossReferenceLink, {
      props: { kind: 'artifact_id', id: 'art-456' },
      global: { stubs: { RouterLink: true } },
    });
    expect(w.find('button').exists()).toBe(true);
  });
});
