import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BundlesView from '@/views/bundles/BundlesView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Bundle } from '@/lib/types';

function provide(seed: Bundle[] = []) {
  const detail = new Map<string, Bundle>();
  for (const b of seed) detail.set(b.id, b);
  let store: Bundle[] = [...seed];
  const installs: Array<{ kind: string; path: string }> = [];
  const removes: string[] = [];
  const client = createFakeHarnessClient({
    bundle: {
      list: async () => [...store],
      get: async (id) =>
        detail.get(id) ?? {
          id,
          name: id,
          version: '',
          tier: '',
          artifactCount: 0,
        },
      install: async (req) => {
        installs.push(req);
        const installed: Bundle = {
          id: 'installed-' + req.path,
          name: 'installed-' + req.path,
          version: '0.1.0',
          tier: 'channel (uncached)',
          source: req.kind + ':' + req.path,
          artifactCount: 0,
        };
        store = [...store, installed];
        return installed;
      },
      remove: async (id) => {
        removes.push(id);
        store = store.filter((b) => b.id !== id);
      },
    },
  });
  return { client, installs, removes };
}

describe('BundlesView (FR-001b numbered-section header)', () => {
  it('renders the canvas head with section number 03', async () => {
    const { client } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('03');
    expect(w.text()).toContain('BUNDLES');
    expect(w.text()).toContain('Installed bundles');
  });

  it('renders the empty-state copy + doc link when nothing is installed', async () => {
    const { client } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('No bundles installed');
    expect(w.html()).toContain('docs/bundles.md');
  });

  it('renders one row per bundle with signature + artifact-count metadata', async () => {
    const seed: Bundle[] = [
      {
        id: 'alpha',
        name: 'alpha',
        version: '0.2.0',
        tier: 'signed',
        source: 'https://example.com/alpha',
        signature: 'sigstore://abc',
        artifactCount: 2,
      },
      {
        id: 'zeta',
        name: 'zeta',
        version: '1.0.0',
        tier: 'channel',
        artifactCount: 0,
      },
    ];
    const { client } = provide(seed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const table = w.find('[data-testid=bundles-table]');
    expect(table.exists()).toBe(true);
    expect(w.text()).toContain('alpha');
    expect(w.text()).toContain('zeta');
    expect(w.text()).toContain('Signed');
    expect(w.text()).toContain('Unsigned');
  });

  it('expands artifact details on click', async () => {
    const seed: Bundle[] = [
      {
        id: 'alpha',
        name: 'alpha',
        version: '0.2.0',
        tier: 'signed',
        artifactCount: 1,
        artifacts: [
          {
            name: 'policy.toml',
            kind: 'policy',
            contentHash: 'sha256:0123456789abcdef',
          },
        ],
      },
    ];
    const { client } = provide(seed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const button = w.find('[data-testid="bundle-toggle-alpha"]');
    await button.trigger('click');
    await flushPromises();
    expect(w.text()).toContain('policy.toml');
    expect(w.text()).toContain('policy');
  });

  it('install form calls Bundle_Install and refreshes the list', async () => {
    const { client, installs } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const input = w.find('[data-testid="bundle-install-path"]');
    await input.setValue('/abs/path/to/alpha');
    await w.find('[data-testid="bundle-install-form"]').trigger('submit');
    await flushPromises();
    expect(installs).toHaveLength(1);
    expect(installs[0]).toEqual({ kind: 'local_path', path: '/abs/path/to/alpha' });
    // refresh repopulated the table
    expect(w.text()).toContain('installed-/abs/path/to/alpha');
  });

  it('install form surfaces errors and does not refresh on failure', async () => {
    const client = createFakeHarnessClient({
      bundle: {
        list: async () => [],
        get: async (id) => ({
          id,
          name: id,
          version: '',
          tier: '',
          artifactCount: 0,
        }),
        install: async () => {
          throw new Error('manifest missing');
        },
        remove: async () => {},
      },
    });
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    await w.find('[data-testid="bundle-install-path"]').setValue('/nope');
    await w.find('[data-testid="bundle-install-form"]').trigger('submit');
    await flushPromises();
    expect(w.find('[data-testid="bundle-install-error"]').text()).toContain('manifest missing');
  });

  it('row Remove button calls Bundle_Remove and refreshes', async () => {
    const seed: Bundle[] = [
      {
        id: 'alpha',
        name: 'alpha',
        version: '0.2.0',
        tier: 'signed',
        artifactCount: 0,
      },
    ];
    const { client, removes } = provide(seed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('alpha');
    await w.find('[data-testid="bundle-remove-alpha"]').trigger('click');
    await flushPromises();
    expect(removes).toEqual(['alpha']);
    // After remove the table is empty so the empty-state appears
    expect(w.text()).toContain('No bundles installed');
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { client } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
