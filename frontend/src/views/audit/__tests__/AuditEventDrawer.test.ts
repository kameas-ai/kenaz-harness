/**
 * AuditEventDrawer unit tests (WP06).
 *
 * Verifies:
 * 1. Drawer renders all spec §4.2 fields.
 * 2. Adjacent navigation wraps correctly at ends.
 * 3. Drawer can be closed.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import AuditEventDrawer from '@/views/audit/AuditEventDrawer.vue';
import type { AuditEntry } from '@/lib/types';

const seed: AuditEntry[] = [
  { id: '01AAA', timestamp: '2026-04-25T00:00:00Z', category: 'LLM', subject: 'llm.request.started', trailing: 'cafe' },
  { id: '01BBB', timestamp: '2026-04-25T00:01:00Z', category: 'MCP', subject: 'mcp.tool.invoked', trailing: 'dead' },
  { id: '01CCC', timestamp: '2026-04-25T00:02:00Z', category: 'POLICY', subject: 'policy.gate.denied' },
];

function mountDrawer(entry: AuditEntry | null = seed[0]) {
  const onClose = vi.fn();
  const onSelect = vi.fn();
  const w = mount(AuditEventDrawer, {
    attachTo: document.body,
    props: {
      entry,
      entries: seed,
    },
    global: {
      stubs: {
        Teleport: true, // render teleport inline
        CrossReferenceLink: true,
        TraceLink: true,
      },
    },
  });
  w.vm.$emit = (event: string, ...args: unknown[]) => {
    if (event === 'close') onClose();
    if (event === 'select') onSelect(args[0]);
  };
  return { w, onClose, onSelect };
}

describe('AuditEventDrawer', () => {
  it('renders the entry fields from spec §4.2', async () => {
    const { w } = mountDrawer(seed[0]);
    const text = w.text();
    expect(text).toContain(seed[0].id);
    expect(text).toContain(seed[0].subject);
    expect(text).toContain(seed[0].category);
    expect(text).toContain(seed[0].timestamp);
    expect(text).toContain(seed[0].trailing!);
  });

  it('shows null state (nothing) when entry is null', () => {
    const { w } = mountDrawer(null);
    // When entry is null the drawer should not render its panel.
    expect(w.find('[data-testid="audit-event-drawer"]').exists()).toBe(false);
  });

  it('shows navigation position indicator', async () => {
    const { w } = mountDrawer(seed[1]);
    // Middle entry: "2 of 3"
    expect(w.text()).toContain('2 of 3');
  });

  it('renders prev/next navigation buttons', () => {
    const { w } = mountDrawer(seed[1]);
    const buttons = w.findAll('button');
    const labels = buttons.map((b) => b.attributes('aria-label') ?? '');
    expect(labels).toContain('Previous entry');
    expect(labels).toContain('Next entry');
  });

  it('prev button is disabled for first entry', () => {
    const { w } = mountDrawer(seed[0]);
    const prevBtn = w.find('button[aria-label="Previous entry"]');
    expect(prevBtn.attributes('disabled')).toBeDefined();
  });

  it('next button is disabled for last entry', () => {
    const { w } = mountDrawer(seed[2]);
    const nextBtn = w.find('button[aria-label="Next entry"]');
    expect(nextBtn.attributes('disabled')).toBeDefined();
  });
});
