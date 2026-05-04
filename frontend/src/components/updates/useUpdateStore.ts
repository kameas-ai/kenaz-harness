/**
 * useUpdateStore — module-scoped reactive store for the auto-update surface
 * (mission auto-update WP04, the user-facing indicator + menu).
 *
 * This is intentionally small:
 *   - one ref<UpdateStatus | null> shared across the whole frontend
 *   - one helper (`ensureBoot`) that calls `client.status()` once per session
 *     and subscribes to the `update:available` broker topic
 *   - localStorage-backed "skip this version" memory so the indicator stays
 *     hidden between launches even before the backend's persisted skip lands
 *
 * Centralising state here avoids duplicate `status()` calls when the indicator
 * mounts in the chrome and the menu opens beneath it (the menu reads the
 * same ref the indicator does).
 *
 * Bindings come from `lib/updateClient.ts`. Tests inject a fake client via
 * `__resetUpdateStoreForTests({ client })`.
 */

import { ref, computed, type Ref } from 'vue';
import type { UpdateClient, UpdateStatus } from '@/lib/updateClient';
import { createUpdateClient } from '@/lib/updateClient';

const SKIP_LS_KEY = 'kaneaz_update_skipped_version';
const FIRST_SEEN_LS_KEY = 'kaneaz_update_first_seen_version';

interface StoreState {
  status: Ref<UpdateStatus | null>;
  /** Version most recently surfaced as a toast in this session, to dedupe. */
  toastedVersion: Ref<string | null>;
  /** Set true after ensureBoot wires the broker subscription. */
  booted: Ref<boolean>;
}

let _client: UpdateClient = createUpdateClient();
const state: StoreState = {
  status: ref<UpdateStatus | null>(null),
  toastedVersion: ref<string | null>(null),
  booted: ref(false),
};

let unsubBroker: (() => void) | null = null;

interface RuntimeShape {
  EventsOn?: (topic: string, cb: (payload: unknown) => void) => () => void;
}

function getRuntime(): RuntimeShape | null {
  if (typeof window === 'undefined') return null;
  const r = (window as unknown as { runtime?: RuntimeShape }).runtime;
  return r ?? null;
}

function readSkippedFromStorage(): string | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage.getItem(SKIP_LS_KEY);
}

function writeSkippedToStorage(version: string): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.setItem(SKIP_LS_KEY, version);
}

function hasSeenVersionThisSession(version: string): boolean {
  if (typeof sessionStorage === 'undefined') return false;
  return sessionStorage.getItem(FIRST_SEEN_LS_KEY) === version;
}

function markVersionSeenThisSession(version: string): void {
  if (typeof sessionStorage === 'undefined') return;
  sessionStorage.setItem(FIRST_SEEN_LS_KEY, version);
}

/** Apply local skip / dedupe rules on top of a freshly-arrived status. */
function reconcile(s: UpdateStatus): UpdateStatus {
  const skipped = readSkippedFromStorage();
  if (
    s.available &&
    s.availableVersion &&
    skipped &&
    skipped === s.availableVersion
  ) {
    return { ...s, skippedByUser: true };
  }
  return s;
}

export interface UpdateStore {
  /** Reactive snapshot of the latest backend status, with local skip merged. */
  status: Ref<UpdateStatus | null>;
  /** True when an update is offered AND the user hasn't skipped that version. */
  visible: Ref<boolean>;
  /**
   * True iff this is the first time we've seen `availableVersion` in this
   * browser session — the toast layer reads this to decide whether to surface
   * the one-shot "vX.Y.Z is available" notification.
   */
  shouldToast: Ref<boolean>;
  /** Mark the current available version as toasted (call after the toast renders). */
  markToasted(version: string): void;
  /** Boot once: pull initial status + wire broker subscription. Idempotent. */
  ensureBoot(): Promise<void>;
  /** Persist & broadcast a "skip this version" decision. */
  skipVersion(version: string): Promise<void>;
  /** Force a re-check (used by the manual "Check now" affordance). */
  refresh(): Promise<void>;
  /** Trigger a background download. */
  download(): Promise<void>;
  /** Apply (Mac/Linux: install + restart; Windows: stage). */
  apply(): Promise<void>;
}

export function useUpdateStore(): UpdateStore {
  const visible = computed(() => {
    const s = state.status.value;
    if (!s) return false;
    if (!s.available) return false;
    if (s.skippedByUser) return false;
    return true;
  });

  const shouldToast = computed(() => {
    const s = state.status.value;
    if (!s || !s.available || !s.availableVersion) return false;
    if (s.skippedByUser) return false;
    if (hasSeenVersionThisSession(s.availableVersion)) return false;
    if (state.toastedVersion.value === s.availableVersion) return false;
    return true;
  });

  function markToasted(version: string): void {
    state.toastedVersion.value = version;
    markVersionSeenThisSession(version);
  }

  async function ensureBoot(): Promise<void> {
    if (state.booted.value) return;
    state.booted.value = true;
    try {
      const s = await _client.status();
      state.status.value = reconcile(s);
    } catch {
      // Bridge may not be ready yet on first paint; the broker subscription
      // below will pick up the next `update:available` push.
    }
    const rt = getRuntime();
    if (rt?.EventsOn) {
      unsubBroker = rt.EventsOn('update:available', (payload) => {
        if (payload && typeof payload === 'object') {
          const next = payload as UpdateStatus;
          state.status.value = reconcile(next);
        }
      });
    }
  }

  async function skipVersion(version: string): Promise<void> {
    writeSkippedToStorage(version);
    try {
      await _client.skipVersion(version);
    } catch {
      // Backend skip is best-effort; the localStorage write keeps the UI
      // honest until the next successful call.
    }
    if (state.status.value) {
      state.status.value = { ...state.status.value, skippedByUser: true };
    }
  }

  async function refresh(): Promise<void> {
    await _client.startCheck();
    const s = await _client.status();
    state.status.value = reconcile(s);
  }

  async function download(): Promise<void> {
    await _client.startDownload();
  }

  async function apply(): Promise<void> {
    await _client.apply();
  }

  return {
    status: state.status,
    visible,
    shouldToast,
    markToasted,
    ensureBoot,
    skipVersion,
    refresh,
    download,
    apply,
  };
}

/**
 * __resetUpdateStoreForTests — wipe module-scoped state and (optionally)
 * inject a fake client. Mirrors the pattern in workflowRunsStore.
 */
export function __resetUpdateStoreForTests(opts?: {
  client?: UpdateClient;
}): void {
  state.status.value = null;
  state.toastedVersion.value = null;
  state.booted.value = false;
  if (unsubBroker) {
    unsubBroker();
    unsubBroker = null;
  }
  if (opts?.client) {
    _client = opts.client;
  } else {
    _client = createUpdateClient();
  }
  if (typeof localStorage !== 'undefined') {
    localStorage.removeItem(SKIP_LS_KEY);
  }
  if (typeof sessionStorage !== 'undefined') {
    sessionStorage.removeItem(FIRST_SEEN_LS_KEY);
  }
}
