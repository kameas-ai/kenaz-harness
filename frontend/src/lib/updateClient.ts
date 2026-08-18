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
   *
   * Mechanism (self-update-repair-01PMUP01 DC-1): starts the download,
   * then POLLS Update_Status until downloadState leaves 'downloading' —
   * it does NOT race a bare `await StartDownload(); await Apply();`
   * (StartDownload is fire-and-forget; Apply would see hasStaged=false
   * on every call, 100% of the time — the race is lost by construction,
   * not by timing). Resolves only after Apply() succeeds against a
   * confirmed 'staged' status; rejects with the failure reason on
   * 'failed', or after a 30-minute ceiling with a message naming the
   * last observed state (never resolves silently on timeout).
   *
   * The version arg is informational; the backend installs whatever the
   * current channel manifest advertises.
   *
   * `onStatus` is invoked with every snapshot the poll loop reads. It is
   * what keeps DC-1 honest for the RENDERED surface, not just for the
   * terminal outcome: the panel's progress bar advances from these
   * snapshots at the poll cadence with zero broker events present, so
   * the three `update:download-*` subscriptions really are an
   * accelerator (10/s instead of 2/s) rather than the bar's only source
   * of movement. It does NOT open a second poll loop — installLatest
   * remains the one waiter (DC-8); the panel just sees what that waiter
   * already read.
   */
  installLatest(
    version: string,
    onStatus?: (status: UpdateStatus) => void,
  ): Promise<void>;
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

/** DC-3: Status is a cheap in-memory RLock read (no network, no disk) —
 *  a 500ms poll is free; do not add caching or debouncing. */
const DEFAULT_POLL_INTERVAL_MS = 500;
/** spec §4.1 Bounds: a generous ceiling so a WEDGED pump cannot spin
 *  installLatest's promise forever. On expiry we reject naming the
 *  observed state rather than resolving silently.
 *
 *  This is a NO-PROGRESS window, not a total-runtime cap. The spec said
 *  "30 min"; read as total runtime it kills a download that is merely
 *  slow — a 150MB asset on a ~110KB/s link (hotel wifi, tethering) is
 *  healthy, progressing, and dead at the ceiling. Worse, there is no
 *  resume path (spec §3 non-goals), so the retry restarts from zero and
 *  hits the same wall: that user can never update, which is the exact
 *  outcome this mission exists to prevent. Wedged is what the bound is
 *  for, and wedged means the observed status stops changing. */
const DEFAULT_POLL_TIMEOUT_MS = 30 * 60 * 1000;

/** Test seam for the installLatest poll cadence/ceiling — production
 *  callers never pass these; vitest overrides them so AC-2/the
 *  30-minute-bound test don't need real wall-clock time. */
export interface UpdateClientOptions {
  pollIntervalMs?: number;
  pollTimeoutMs?: number;
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function createUpdateClient(
  opts: UpdateClientOptions = {},
): UpdateClient {
  const pollIntervalMs = opts.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS;
  const pollTimeoutMs = opts.pollTimeoutMs ?? DEFAULT_POLL_TIMEOUT_MS;

  return {
    status: () => bridge().Update_Status(),
    startCheck: () => bridge().Update_StartCheck(),
    startDownload: () => bridge().Update_StartDownload(),
    apply: () => bridge().Update_Apply(),
    installLatest: async (
      _version: string,
      onStatus?: (status: UpdateStatus) => void,
    ) => {
      // DC-1: poll Update_Status to a terminal state; the broker events
      // (WP03) are an accelerator for the repaint rate only — this
      // function must resolve/reject correctly with every subscription
      // detached. No broker event is read here, deliberately (WP02 must
      // not gate any transition on an event arriving).
      //
      // StartDownload is fire-and-forget: it clears hasStaged and
      // returns immediately after spawning the download pump. A bare
      // `await StartDownload(); await Apply();` therefore raced Apply
      // against a download that had not started, and lost 100% of the
      // time (self-update-repair-01PMUP01 §1.1). Errors from
      // StartDownload — including ErrDownloadInFlight when a previous
      // download is still running — propagate unchanged; we do not
      // catch and keep polling.
      await bridge().Update_StartDownload();

      // The deadline is refreshed every time the observed status CHANGES
      // (state, percent, or error). A progressing download therefore
      // never expires; a pump that stops moving for pollTimeoutMs does.
      let deadline = Date.now() + pollTimeoutMs;
      let lastSeen = '';
      for (;;) {
        const s = await bridge().Update_Status();
        const seen = `${s.downloadState}|${s.downloadProgress ?? ''}|${s.downloadError ?? ''}`;
        if (seen !== lastSeen) {
          lastSeen = seen;
          deadline = Date.now() + pollTimeoutMs;
        }
        // Hand every polled snapshot to the caller BEFORE acting on it,
        // so the panel repaints 'downloading'/'staged'/'failed' (and the
        // percent) from the poll alone. Deleting every useEventStream
        // subscription must change frame rate, never what the user sees
        // (spec §4.1's stated invariant).
        onStatus?.(s);
        if (s.downloadState === 'staged') {
          await bridge().Update_Apply();
          return;
        }
        if (s.downloadState === 'failed') {
          throw new Error(
            s.downloadError || 'Update download failed with no reason given.',
          );
        }
        if (Date.now() >= deadline) {
          throw new Error(
            `Update install stalled: no progress for ${Math.round(pollTimeoutMs / 60000)} minutes ` +
              `(last observed downloadState: "${s.downloadState}").`,
          );
        }
        await delay(pollIntervalMs);
      }
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
