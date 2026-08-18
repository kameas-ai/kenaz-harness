/**
 * updateClient — frontend shim for the auto-update v0.4.0 RPC surface.
 *
 * Unified surface for three work packages:
 *   - WP03 wires the real Wails bindings (Update_Status returns
 *     update.StatusOutput; Update_StartCheck / StartDownload / Apply /
 *     SkipVersion / ListSkippedVersions / UnskipVersion).
 *   - WP04 (Chrome-style indicator + UpdateMenu) consumes the flat
 *     UpdateStatus directly: availableVersion, downloadState,
 *     downloadProgress, releaseUrl, skippedByUser.
 *   - WP05 (Settings → Updates panel) consumes the same status and
 *     additionally drives listSkippedVersions / unskipVersion plus
 *     install. The Settings panel reads channel + interval + auto-check
 *     out of the Settings record (not UpdateStatus) via SettingsClient.
 *
 * Status shape mirrors the WP02 backend `update.Status` struct:
 *   currentVersion: semver of the running binary
 *   available     : true iff a newer version on the configured channel exists
 *   availableVersion: semver of the offered upgrade (only set when available)
 *   channel       : 'stable' | 'beta' | 'dev' | 'prerelease'
 *   downloadState : staged-download lifecycle
 *   downloadProgress: 0-100 integer percent, only meaningful while
 *     downloading (DC-2 — NOT a 0..1 fraction; a naive
 *     `width: progress * 100 + '%'` renders 10000%)
 *   downloadError : reason the most recent download failed, only set
 *     while downloadState === 'failed'; survives a reload (FR-004/FR-005)
 *   notes         : short markdown blurb pulled from the release manifest
 *   releaseUrl    : GitHub Releases URL for the offered version
 *   skippedByUser : true iff the user already chose Skip on this version
 *   lastCheckedAt : Unix-seconds timestamp of the most recent check
 */

export interface UpdateStatus {
  currentVersion: string;
  available: boolean;
  availableVersion?: string;
  channel: string;
  downloadState: 'idle' | 'downloading' | 'staged' | 'failed';
  /** 0-100 integer percent (DC-2) — never a 0..1 fraction. */
  downloadProgress?: number;
  /** Reason the most recent download failed. Only meaningful while
   *  downloadState === 'failed'; readable from a fresh status() call
   *  after a reload, not only from the one-shot 'update:download-failed'
   *  broker frame (FR-004/FR-005). */
  downloadError?: string;
  notes?: string;
  releaseUrl?: string;
  skippedByUser?: boolean;
  /** Unix-seconds since the last successful check; null/undefined when
   *  the checker has never run on this install. */
  lastCheckedAt?: number;
}

export interface UpdateClient {
  /** Read the current update status (cheap; backend caches the manifest). */
  status(): Promise<UpdateStatus>;
  /** Force an immediate check against the channel manifest. */
  startCheck(): Promise<void>;
  /** Begin (or resume) downloading the staged binary in the background. */
  startDownload(): Promise<void>;
  /**
   * Apply the staged update.
   * - Mac/Linux: replaces the binary and restarts the app.
   * - Windows  : stages the installer for the next launch (no restart).
   */
  apply(): Promise<void>;
  /**
   * Convenience shim used by the Settings → Updates "Install" button.
   * Triggers a download (if not already staged) then applies. The
   * version arg is informational; the backend installs whatever the
   * current channel manifest advertises.
   */
  installLatest(version: string): Promise<void>;
  /** Persist a "skip this version" choice; backend won't re-prompt. */
  skipVersion(version: string): Promise<void>;
  /** Read the user's skipped-versions list. */
  listSkippedVersions(): Promise<string[]>;
  /** Remove a version from the skip-list. No-op if missing. */
  unskipVersion(version: string): Promise<void>;
}

interface BridgeShape {
  Update_Status: () => Promise<UpdateStatus>;
  Update_StartCheck: () => Promise<void>;
  Update_StartDownload: () => Promise<void>;
  Update_Apply: () => Promise<void>;
  Update_SkipVersion: (version: string) => Promise<void>;
  Update_ListSkippedVersions: () => Promise<string[]>;
  Update_UnskipVersion: (version: string) => Promise<void>;
}

// NOTE: not declaring `interface Window` here. `harnessClient.ts` and
// `workflowsClient.ts` already extend the global Window with their own
// (different) `Bindings` shapes; declaring a third would either collide or
// narrow the union. We cast at access time instead.

interface BridgesContainer {
  go?: { rpc?: { Bindings?: unknown } };
}

function bridge(): BridgeShape {
  const w =
    typeof window !== 'undefined'
      ? (window as unknown as BridgesContainer)
      : undefined;
  const b = w?.go?.rpc?.Bindings;
  if (!b) {
    throw new Error(
      'window.go.rpc.Bindings is not available. The harness frontend must run inside Wails.',
    );
  }
  return b as BridgeShape;
}

export function createUpdateClient(): UpdateClient {
  return {
    status: () => bridge().Update_Status(),
    startCheck: () => bridge().Update_StartCheck(),
    startDownload: () => bridge().Update_StartDownload(),
    apply: () => bridge().Update_Apply(),
    installLatest: async (_version: string) => {
      // The Settings panel's "Install" button is a one-shot: kick off
      // the download then apply when ready. The version arg is
      // informational; the backend installs the current manifest's
      // advertised version.
      await bridge().Update_StartDownload();
      await bridge().Update_Apply();
    },
    skipVersion: (version) => bridge().Update_SkipVersion(version),
    listSkippedVersions: () => bridge().Update_ListSkippedVersions(),
    unskipVersion: (version) => bridge().Update_UnskipVersion(version),
  };
}

/**
 * createFakeUpdateClient — stub used by UpdateIndicator / UpdateMenu /
 * UpdatesPanel tests (and dev builds where the live bridge isn't wired
 * yet). `seed` lets a caller inject specific behaviour; everything else
 * returns a safe no-op or in-memory default.
 *
 * Default `status()` returns a "no update available" snapshot so consumers
 * boot into the hidden state.
 */
export function createFakeUpdateClient(
  seed: Partial<UpdateClient> = {},
): UpdateClient {
  const skipped = new Set<string>();
  const defaults: UpdateClient = {
    status: () =>
      Promise.resolve({
        currentVersion: '0.4.0',
        available: false,
        channel: 'stable',
        downloadState: 'idle',
      }),
    startCheck: () => Promise.resolve(),
    startDownload: () => Promise.resolve(),
    apply: () => Promise.resolve(),
    installLatest: () => Promise.resolve(),
    skipVersion: async (v) => {
      if (v) skipped.add(v);
    },
    listSkippedVersions: () => Promise.resolve([...skipped]),
    unskipVersion: async (v) => {
      skipped.delete(v);
    },
  };
  return { ...defaults, ...seed };
}
