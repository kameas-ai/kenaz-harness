import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BundlesView from '@/views/bundles/BundlesView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Bundle, TrustAnchor } from '@/lib/types';

function provide(seed: Bundle[] = [], anchorSeed: TrustAnchor[] = []) {
  const detail = new Map<string, Bundle>();
  for (const b of seed) detail.set(b.id, b);
  let store: Bundle[] = [...seed];
  const installs: Array<{ kind: string; path?: string; url?: string }> = [];
  const removes: string[] = [];
  let anchorStore: TrustAnchor[] = [...anchorSeed];
  const anchorInstalls: Array<{ anchorId: string; peerId?: string; keyB64: string }> = [];
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
        const locator = req.path ?? req.url ?? '';
        const installed: Bundle = {
          id: 'installed-' + locator,
          name: 'installed-' + locator,
          version: '0.1.0',
          tier: 'channel (uncached)',
          source: req.kind + ':' + locator,
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
    trustAnchors: {
      list: async () => [...anchorStore],
      install: async (req) => {
        anchorInstalls.push(req);
        const installed: TrustAnchor = {
          anchorId: req.anchorId,
          kind: req.kind ?? 'raw_public_key',
          peerId: req.peerId,
          algorithm: req.algorithm ?? 'ed25519',
          publicKey: { algorithm: req.algorithm ?? 'ed25519', keyB64: req.keyB64, fingerprint: 'fp-' + req.anchorId },
          installedAt: new Date().toISOString(),
          removed: false,
        };
        anchorStore = [...anchorStore, installed];
        return installed;
      },
    },
  });
  return { client, installs, removes, anchorInstalls };
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

  it('renders one row per bundle with verification + artifact-count metadata', async () => {
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

  // UNIT-8 (bundle-download-and-verify-01PMZ909, spec FR-006 / G-2 carried
  // to the frontend layer): verification state MUST come from Bundle.tier
  // (UNIT-4's real recorded VerifyManifestSignatures result), never from
  // Bundle.signature's mere presence. A pre-UNIT-4 lockfile row can carry a
  // non-empty signature locator with Verified defaulting false (AC-008) —
  // it must render "Unsigned" even though `signature` is set. Before this
  // unit BundlesView badged solely on `b.signature`, which would have
  // rendered this exact row as "Signed".
  it('renders a legacy unverified row (signature present, tier not "signed") as Unsigned, not Signed', async () => {
    const seed: Bundle[] = [
      {
        id: 'legacy',
        name: 'legacy',
        version: '0.1.0',
        tier: 'channel',
        signature: 'sigstore://legacy-locator',
        artifactCount: 1,
      },
    ];
    const { client } = provide(seed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const badge = w.find('[data-testid="bundle-verification-legacy"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('Unsigned');
  });

  it('renders a bundle whose tier carries the "(uncached)" suffix as Signed when the tier starts with "signed"', async () => {
    const seed: Bundle[] = [
      {
        id: 'fresh',
        name: 'fresh',
        version: '0.1.0',
        tier: 'signed (uncached)',
        artifactCount: 1,
      },
    ];
    const { client } = provide(seed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.find('[data-testid="bundle-verification-fresh"]').text()).toBe('Signed');
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

  it('install form defaults to local_path and calls Bundle_Install with a path', async () => {
    const { client, installs } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const input = w.find('[data-testid="bundle-install-locator"]');
    await input.setValue('/abs/path/to/alpha');
    await w.find('[data-testid="bundle-install-form"]').trigger('submit');
    await flushPromises();
    expect(installs).toHaveLength(1);
    expect(installs[0]).toEqual({ kind: 'local_path', path: '/abs/path/to/alpha' });
    // refresh repopulated the table
    expect(w.text()).toContain('installed-/abs/path/to/alpha');
  });

  // UNIT-8 step 1: the picker offers exactly the kinds the backend has a
  // registered channels.Registry factory for (local_path, http_mirror —
  // see check-bundle-channel-kinds-sync.sh). Selecting http_mirror must
  // switch the locator input to a URL field and install with `url`, not
  // `path`.
  it('switching the channel picker to http_mirror installs with a url', async () => {
    const { client, installs } = provide();
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    await w.find('[data-testid="bundle-install-kind"]').setValue('http_mirror');
    await w.find('[data-testid="bundle-install-locator"]').setValue('https://mirror.example.com/b');
    await w.find('[data-testid="bundle-install-form"]').trigger('submit');
    await flushPromises();
    expect(installs).toHaveLength(1);
    expect(installs[0]).toEqual({
      kind: 'http_mirror',
      url: 'https://mirror.example.com/b',
    });
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
    await w.find('[data-testid="bundle-install-locator"]').setValue('/nope');
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

  // UNIT-8 step 3: an anchor list + install form over UNIT-3's RPCs.
  it('renders installed trust anchors and installs a new one', async () => {
    const anchorSeed: TrustAnchor[] = [
      {
        anchorId: 'anchor-1',
        kind: 'raw_public_key',
        algorithm: 'ed25519',
        publicKey: { algorithm: 'ed25519', keyB64: 'AAAA', fingerprint: 'sha256:deadbeef' },
        installedAt: new Date().toISOString(),
        removed: false,
      },
    ];
    const { client, anchorInstalls } = provide([], anchorSeed);
    const w = mount(BundlesView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.find('[data-testid="anchor-row-anchor-1"]').exists()).toBe(true);

    await w.find('[data-testid="anchor-install-id"]').setValue('anchor-2');
    await w.find('[data-testid="anchor-install-key"]').setValue('QUJD');
    await w.find('[data-testid="anchor-install-form"]').trigger('submit');
    await flushPromises();
    expect(anchorInstalls).toEqual([{ anchorId: 'anchor-2', peerId: undefined, keyB64: 'QUJD' }]);
    expect(w.find('[data-testid="anchor-row-anchor-2"]').exists()).toBe(true);
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
