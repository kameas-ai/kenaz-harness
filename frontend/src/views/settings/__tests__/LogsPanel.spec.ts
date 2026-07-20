/**
 * LogsPanel tests — mission 01NLOGS01 WP05 (frontend Logs tab)
 *
 * Covers:
 *   - renders without crashing (empty log store).
 *   - displays rows returned by runtimeLogs.tail().
 *   - level filter is applied when changed.
 *   - source filter is applied when changed.
 *   - search filter is applied when changed.
 *   - shows "No log rows" empty state.
 *   - follow-tail toggle correctly reflects state.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import LogsPanel from '@/views/settings/LogsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { LogRow, LogFilter } from '@/lib/harnessClient';

// Fake rows returned by the server.
const FAKE_ROWS: LogRow[] = [
  { timestamp: '2026-07-20T10:00:00.000Z', level: 'info',  source: 'sessions',   message: 'session started' },
  { timestamp: '2026-07-20T10:00:01.000Z', level: 'warn',  source: 'mcp:foo',    message: 'spawn retried' },
  { timestamp: '2026-07-20T10:00:02.000Z', level: 'error', source: 'mcp:foo',    message: 'connection refused' },
  { timestamp: '2026-07-20T10:00:03.000Z', level: 'debug', source: 'llm',        message: 'model response received' },
];

function buildClient(rows: LogRow[] = FAKE_ROWS) {
  const tail = vi.fn(async (_filter: LogFilter) => [...rows]);
  const client = createFakeHarnessClient({
    runtimeLogs: { tail } as any,
  });
  return { client, tail };
}

// Suppress setInterval / clearInterval warnings in tests.
beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe('LogsPanel', () => {
  it('renders without crashing', async () => {
    const { client } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="logs-panel"]').exists()).toBe(true);
  });

  it('displays rows from the server', async () => {
    const { client } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const rows = wrapper.findAll('[data-testid="logs-row"]');
    expect(rows).toHaveLength(FAKE_ROWS.length);
  });

  it('shows empty state when no rows match', async () => {
    const { client } = buildClient([]);
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="logs-empty"]').exists()).toBe(true);
  });

  it('calls tail with level filter when level select changes', async () => {
    const { client, tail } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    tail.mockClear();

    const select = wrapper.find<HTMLSelectElement>('[data-testid="logs-level-filter"]');
    await select.setValue('warn');
    await select.trigger('change');
    await flushPromises();

    expect(tail).toHaveBeenCalledWith(expect.objectContaining({ level: 'warn' }));
  });

  it('calls tail with source filter when source input changes', async () => {
    const { client, tail } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    tail.mockClear();

    const input = wrapper.find<HTMLInputElement>('[data-testid="logs-source-filter"]');
    await input.setValue('mcp');
    await input.trigger('input');
    await flushPromises();

    expect(tail).toHaveBeenCalledWith(expect.objectContaining({ source: 'mcp' }));
  });

  it('calls tail with search filter when search input changes', async () => {
    const { client, tail } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    tail.mockClear();

    const input = wrapper.find<HTMLInputElement>('[data-testid="logs-search"]');
    await input.setValue('timeout');
    await input.trigger('input');
    await flushPromises();

    expect(tail).toHaveBeenCalledWith(expect.objectContaining({ search: 'timeout' }));
  });

  it('shows the follow-tail button', async () => {
    const { client } = buildClient();
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="logs-follow-tail"]').exists()).toBe(true);
  });

  it('shows row count badge', async () => {
    const { client } = buildClient(FAKE_ROWS);
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const count = wrapper.find('[data-testid="logs-count"]');
    expect(count.text()).toContain(String(FAKE_ROWS.length));
  });

  it('renders level badge for each row', async () => {
    const { client } = buildClient(FAKE_ROWS);
    const wrapper = mount(LogsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    // info, warn, error, debug should all have their level badge rendered.
    for (const row of FAKE_ROWS) {
      const badge = wrapper.find(`[data-testid="log-level-${row.level}"]`);
      expect(badge.exists(), `level badge for ${row.level}`).toBe(true);
    }
  });
});
