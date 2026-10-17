/**
 * MarkdownBlock.teardown.spec.ts — async upgrades must never mount a
 * child app (or reject) after the component — or the whole test
 * environment — has gone away.
 *
 * Regression for the CI flake seen on PR 439 (run 39075582687): `upgradeCodeBlocks` awaited the
 * CodeBlock.vue dynamic import, the spec never unmounted its wrapper, the
 * environment was torn down, and the late `app.mount(host)` threw
 * `ReferenceError: window is not defined` as an unhandled rejection.
 *
 * The CodeBlock import is gated on a deferred promise so each test can
 * resolve it at an exact point (after unmount / after teardown).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';

const h = vi.hoisted(() => {
  const state = {
    release: (() => {}) as () => void,
    gate: Promise.resolve() as Promise<void>,
    childMounts: 0,
    fail: false,
  };
  return state;
});

function mockGatedCodeBlock() {
  vi.doMock('@/components/rendering/CodeBlock.vue', async () => {
    await h.gate;
    if (h.fail) throw new Error('CodeBlock chunk failed to load');
    const { defineComponent, h: render, onMounted } = await import('vue');
    return {
      default: defineComponent({
        name: 'CodeBlockStub',
        setup() {
          onMounted(() => {
            h.childMounts++;
          });
          return () => render('pre', { class: 'code-stub' });
        },
      }),
    };
  });
}

vi.mock('@/lib/harnessClient', () => ({
  createHarnessClient: () => ({
    fs: { exists: vi.fn().mockResolvedValue(true) },
    tools: { openInEditor: vi.fn().mockResolvedValue(undefined) },
  }),
}));

const CODE = '```typescript\nconst x = 1;\n```\n';

let unhandled: unknown[] = [];
const onUnhandled = (reason: unknown) => {
  unhandled.push(reason);
};

async function mountGated() {
  // Fresh module graph so the gated CodeBlock factory runs again.
  vi.resetModules();
  mockGatedCodeBlock();
  const { default: MarkdownBlock } = await import(
    '@/components/rendering/MarkdownBlock.vue'
  );
  const vueErrors: unknown[] = [];
  const w = mount(MarkdownBlock, {
    props: { text: CODE, extensions: 'all' },
    global: {
      config: {
        errorHandler: (err: unknown) => {
          vueErrors.push(err);
        },
      },
    },
  });
  // Let onMounted → nextTick → runUpgrades reach the gated import.
  await flushPromises();
  // The upgrade is parked on the gated import: nothing mounted yet.
  expect(h.childMounts).toBe(0);
  return { w, vueErrors };
}

async function settle() {
  await vi.dynamicImportSettled();
  await flushPromises();
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
}

describe('MarkdownBlock async upgrades vs teardown', () => {
  let wrapper: VueWrapper | null = null;

  beforeEach(() => {
    h.childMounts = 0;
    h.fail = false;
    h.gate = new Promise<void>((resolve) => {
      h.release = resolve;
    });
    unhandled = [];
    process.on('unhandledRejection', onUnhandled);
  });

  afterEach(async () => {
    vi.unstubAllGlobals();
    h.release();
    await settle();
    wrapper?.unmount();
    wrapper = null;
    process.off('unhandledRejection', onUnhandled);
  });

  it('control: resolving the import while mounted mounts the CodeBlock', async () => {
    const { w, vueErrors } = await mountGated();
    wrapper = w;
    expect(h.childMounts).toBe(0);
    h.release();
    await settle();
    expect(h.childMounts).toBe(1);
    expect(vueErrors).toEqual([]);
    expect(unhandled).toEqual([]);
  });

  it('unmount mid-upgrade: no child mount, no rejection', async () => {
    const { w, vueErrors } = await mountGated();
    w.unmount();
    h.release();
    await settle();
    expect(h.childMounts).toBe(0);
    expect(vueErrors).toEqual([]);
    expect(unhandled).toEqual([]);
  });

  it('environment torn down mid-upgrade (never unmounted): no child mount, no rejection', async () => {
    const { w, vueErrors } = await mountGated();
    wrapper = w;
    // Simulate vitest tearing the DOM environment down underneath a
    // still-mounted component: `window` is gone when the import resolves.
    vi.stubGlobal('window', undefined);
    h.release();
    await settle();
    vi.unstubAllGlobals();
    expect(h.childMounts).toBe(0);
    expect(vueErrors).toEqual([]);
    expect(unhandled).toEqual([]);
  });

  it('a failure while still mounted reaches app.config.errorHandler (harness.log / Sentry), not an unhandled rejection', async () => {
    const { w, vueErrors } = await mountGated();
    wrapper = w;
    h.fail = true;
    h.release();
    await settle();
    expect(h.childMounts).toBe(0);
    // Exactly one error reaches the app's handler (vitest wraps the failing
    // mock factory's error in its own message, so don't match the text).
    expect(vueErrors).toHaveLength(1);
    expect(unhandled).toEqual([]);
  });
});
