/**
 * AppMenuBroker.spec.ts — tests for the six useEventStream subscriptions in App.vue.
 *
 * Verifies each OS-menu broker event triggers the correct composable action:
 *   menu:about:open        → aboutStore.open()
 *   menu:search:open       → searchPalette.open()
 *   menu:cmd-palette:open  → cmdPalette.open()
 *   menu:theme:set         → theme.set(mode)
 *   menu:route             → router.push(path)
 *   menu:cheat-sheet:toggle → document dispatches 'keydown' with key='?'
 *
 * Strategy: mount App.vue with all heavy children stubbed, inject a fake harness
 * client via global.provide, install a fake Wails runtime, fire events via
 * rt.emit(), then assert side effects via spies.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import App from '@/App.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';

// ---------------------------------------------------------------------------
// Fake Wails runtime (mirrors the pattern in useEventStream.test.ts)
// ---------------------------------------------------------------------------
interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  EventsOff: (topic: string) => void;
  emit: (topic: string, payload?: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn(topic, cb) {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => { s!.delete(cb); };
    },
    EventsOff(topic) { handlers.delete(topic); },
    emit(topic, payload) {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload ?? null);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

// ---------------------------------------------------------------------------
// Stubs for heavy children
// ---------------------------------------------------------------------------
const stubComponent = { template: '<div />' };

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stubComponent },
      { path: '/settings', component: stubComponent },
      { path: '/sessions/new', component: stubComponent },
    ],
  });
}

describe('App.vue — OS menu broker dispatch table', () => {
  let rt: FakeRuntime;
  let router: ReturnType<typeof makeRouter>;
  let client: ReturnType<typeof createFakeHarnessClient>;

  beforeEach(async () => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    router = makeRouter();
    client = createFakeHarnessClient();
  });

  afterEach(() => {
    uninstallRuntime();
  });

  function mountApp() {
    return mount(App, {
      global: {
        plugins: [router],
        provide: {
          // Provide the harness client during setup so useHarnessClient() can inject it.
          [HarnessClientKey as symbol]: client,
        },
        stubs: {
          Shell: stubComponent,
          CommandPalette: stubComponent,
          ErrorBoundary: { template: '<slot />' },
          ToastRoot: stubComponent,
          OnboardingDialog: stubComponent,
          AboutDialog: stubComponent,
        },
      },
    });
  }

  it('menu:about:open opens the about dialog (isOpen becomes true)', async () => {
    // These composables use module-level singleton refs. We observe the state
    // change on the shared ref rather than spying on the returned closure.
    const { useAboutDialogStore } = await import('@/lib/aboutDialogStore');
    const store = useAboutDialogStore();
    // Ensure starting state is false.
    store.close();

    mountApp();
    await flushPromises();

    expect(store.isOpen.value).toBe(false);
    rt.emit('menu:about:open');
    await flushPromises();

    expect(store.isOpen.value).toBe(true);
    // Cleanup: reset so other tests start clean.
    store.close();
  });

  it('menu:search:open opens the search palette (isOpen becomes true)', async () => {
    const { useSearchPalette } = await import('@/lib/useSearchPalette');
    const palette = useSearchPalette();
    palette.close();

    mountApp();
    await flushPromises();

    expect(palette.isOpen.value).toBe(false);
    rt.emit('menu:search:open');
    await flushPromises();

    expect(palette.isOpen.value).toBe(true);
    palette.close();
  });

  it('menu:cmd-palette:open opens the command palette (isOpen becomes true)', async () => {
    const { useCommandPalette } = await import('@/lib/useCommandPalette');
    const palette = useCommandPalette();
    // Close it if open from a previous test.
    if (palette.isOpen.value) palette.toggle();

    mountApp();
    await flushPromises();

    const wasClosed = !palette.isOpen.value;
    rt.emit('menu:cmd-palette:open');
    await flushPromises();

    // After the event, the palette should be open.
    expect(palette.isOpen.value).toBe(true);
    // If it was already open before we emitted, toggle would have closed it —
    // that's acceptable; the test still validates the event fires.
    void wasClosed; // suppress unused-var warning
  });

  it('menu:theme:set with {mode:"dark"} calls theme.set("dark")', async () => {
    // theme.set is a method on the result of useTheme() — but useTheme() is
    // module-scoped-per-component, so we spy on client.settings.saveTheme instead,
    // which is the side-effecting call that confirms theme.set ran.
    const saveThemeSpy = vi.spyOn(client.settings, 'saveTheme').mockResolvedValue(undefined);

    mountApp();
    await flushPromises();

    rt.emit('menu:theme:set', { mode: 'dark' });
    await flushPromises();

    expect(saveThemeSpy).toHaveBeenCalledWith('dark');
    saveThemeSpy.mockRestore();
  });

  it('menu:route with {path:"/settings"} calls router.push("/settings")', async () => {
    mountApp();
    await flushPromises();

    const pushSpy = vi.spyOn(router, 'push');

    rt.emit('menu:route', { path: '/settings' });
    await flushPromises();

    expect(pushSpy).toHaveBeenCalledWith('/settings');
  });

  it('menu:cheat-sheet:toggle dispatches keydown "?" on document', async () => {
    mountApp();
    await flushPromises();

    const dispatched: KeyboardEvent[] = [];
    const dispatchSpy = vi.spyOn(document, 'dispatchEvent').mockImplementation((e) => {
      if (e instanceof KeyboardEvent) dispatched.push(e);
      return true;
    });

    rt.emit('menu:cheat-sheet:toggle');
    await flushPromises();

    const questionMark = dispatched.find((e) => e.key === '?');
    expect(questionMark).toBeDefined();
    expect(questionMark!.type).toBe('keydown');
    dispatchSpy.mockRestore();
  });
});
