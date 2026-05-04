/**
 * updateClient — frontend shim for the auto-update v0.4.0 RPC surface.
 *
 * WP04 introduces this module so the UpdateMenu / UpdatesPanel can speak
 * to the updater without going through the typed HarnessClient (the
 * update RPC lives outside the SettingsAPI / view-scoped surface). WP05
 * extends it with the skip-list operations the Settings panel needs:
 * `listSkippedVersions` (collapsible list source) and `unskipVersion`
 * (the per-row "Unskip" link).
 *
 * Until WP03 wires the real backend, every call here resolves through a
 * mock implementation: `status()` returns a deterministic snapshot,
 * `startCheck()` is a no-op, `installLatest()` is a no-op, and the
 * skip-list operations round-trip through Settings_GetSkippedUpdateVersions
 * / Settings_RemoveSkippedUpdateVersion when those bindings exist (added
 * in this WP), falling back to an in-memory list otherwise.
 *
 * The shim is intentionally narrow — no event streams, no progress
 * payloads — so WP03 can drop the mock and wire the real RPC without
 * touching every call site.
 */

export interface UpdateRelease {
  /** Canonical version string, e.g. "v0.3.4". */
  version: string;
  /** GitHub release URL the "Release notes" link points at. */
  notesUrl: string;
  /** Human-readable publish date. */
  publishedAt: string;
}

export interface UpdateStatus {
  /** Currently-installed version (mirrors AppInfo.build, but stripped
   *  of any "+commit" suffix the build pipeline adds). */
  currentVersion: string;
  /** Whether the auto-check scheduler is enabled. Mirror of
   *  Settings.AutoCheckUpdates(). */
  autoCheckEnabled: boolean;
  /** Last time the checker ran, as an ISO-8601 string, or null when
   *  the checker has never run on this install. */
  lastCheckedAt: string | null;
  /** Active release channel — either "stable" or "prerelease". */
  channel: 'stable' | 'prerelease';
  /** The latest release the checker found, or null when nothing newer
   *  than `currentVersion` is available on the configured channel. */
  available: UpdateRelease | null;
  /** Whether a check is currently in flight (drives the "Check for
   *  updates now" button's disabled state). */
  checking: boolean;
}

export interface UpdateClient {
  /** Read the full update status snapshot. */
  status(): Promise<UpdateStatus>;
  /** Trigger an immediate check. The backend updates `lastCheckedAt`
   *  and `available` fields; status() reflects the new values once it
   *  resolves. */
  startCheck(): Promise<void>;
  /** Install a known release. The backend kicks off the platform-native
   *  installer flow (signed dmg / msi / AppImage); the harness exits
   *  shortly after so the installer can replace the running binary. */
  installLatest(version: string): Promise<void>;

  // ── Skip-list operations (WP05) ──────────────────────────────────────

  /** Read the user's skipped-versions list. */
  listSkippedVersions(): Promise<string[]>;
  /** Add a version to the skip-list. Idempotent. */
  skipVersion(version: string): Promise<void>;
  /** Remove a version from the skip-list. No-op if missing. */
  unskipVersion(version: string): Promise<void>;
}

/* eslint-disable  @typescript-eslint/no-explicit-any */
type WailsBindings = Record<string, (...args: any[]) => Promise<any>>;
/* eslint-enable  @typescript-eslint/no-explicit-any */

function bindings(): WailsBindings | null {
  // The wails runtime injects window.go.* lazily. Returning null lets
  // the mock pathway take over for vitest + storybook.
  const w = (typeof window !== 'undefined' ? window : undefined) as
    | (Window & { go?: { main?: { App?: WailsBindings } } })
    | undefined;
  const b = w?.go?.main?.App;
  return b ?? null;
}

/**
 * createUpdateClient — production factory. Reads through the wails
 * bindings when available (WP03 wires `Update_Status`, `Update_StartCheck`,
 * `Update_InstallLatest`, `Settings_GetSkippedUpdateVersions`,
 * `Settings_AppendSkippedUpdateVersion`, `Settings_RemoveSkippedUpdateVersion`);
 * falls back to a deterministic mock so the panel mounts cleanly in
 * dev / tests where the wails runtime is absent.
 */
export function createUpdateClient(): UpdateClient {
  return {
    status: async () => {
      const b = bindings();
      if (b && typeof b.Update_Status === 'function') {
        return (await b.Update_Status()) as UpdateStatus;
      }
      // Mock — see fakeUpdateStatus() for the canonical shape.
      return fakeUpdateStatus();
    },
    startCheck: async () => {
      const b = bindings();
      if (b && typeof b.Update_StartCheck === 'function') {
        await b.Update_StartCheck();
      }
      // No-op fallback; WP03 wires the real check.
    },
    installLatest: async (version: string) => {
      const b = bindings();
      if (b && typeof b.Update_InstallLatest === 'function') {
        await b.Update_InstallLatest(version);
      }
    },
    listSkippedVersions: async () => {
      const b = bindings();
      if (b && typeof b.Settings_GetSkippedUpdateVersions === 'function') {
        return (await b.Settings_GetSkippedUpdateVersions()) as string[];
      }
      return [...mockSkipped];
    },
    skipVersion: async (version: string) => {
      const b = bindings();
      if (b && typeof b.Settings_AppendSkippedUpdateVersion === 'function') {
        await b.Settings_AppendSkippedUpdateVersion(version);
        return;
      }
      if (version && !mockSkipped.includes(version)) {
        mockSkipped.push(version);
      }
    },
    unskipVersion: async (version: string) => {
      const b = bindings();
      if (b && typeof b.Settings_RemoveSkippedUpdateVersion === 'function') {
        await b.Settings_RemoveSkippedUpdateVersion(version);
        return;
      }
      const idx = mockSkipped.indexOf(version);
      if (idx >= 0) mockSkipped.splice(idx, 1);
    },
  };
}

/* ── Mock fixtures (WP04 default; WP03 will replace) ─────────────────── */

const mockSkipped: string[] = [];

function fakeUpdateStatus(): UpdateStatus {
  return {
    currentVersion: 'v0.3.3',
    autoCheckEnabled: true,
    lastCheckedAt: null,
    channel: 'stable',
    available: null,
    checking: false,
  };
}

/**
 * createFakeUpdateClient — test factory. Tests pass `seed` to override
 * specific methods; everything else routes through deterministic mocks.
 */
export function createFakeUpdateClient(
  seed: Partial<UpdateClient> = {},
): UpdateClient {
  const skipped = new Set<string>();
  const defaults: UpdateClient = {
    status: async () => fakeUpdateStatus(),
    startCheck: async () => {},
    installLatest: async () => {},
    listSkippedVersions: async () => [...skipped],
    skipVersion: async (v) => {
      if (v) skipped.add(v);
    },
    unskipVersion: async (v) => {
      skipped.delete(v);
    },
  };
  return { ...defaults, ...seed };
}
