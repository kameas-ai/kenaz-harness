/**
 * updateClient — minimal direct-bridge accessor for the v0.4.0 auto-update
 * surface (mission auto-update, WP04).
 *
 * The full RPC view (Update_Status / Update_StartCheck / Update_StartDownload /
 * Update_Apply / Update_SkipVersion) is being landed in WP03 by another agent.
 * Until that PR merges + `wails generate module` regenerates the bindings,
 * the WP04 indicator + menu talk to the bridge through this tiny shim — same
 * shape as `workflowsClient.ts`, which served the same purpose for the
 * workflows surface in v0.3.0-beta.
 *
 * The hand-stubbed bindings live in `frontend/wailsjs/go/rpc/Bindings.{d.ts,js}`
 * (see the WP04 hand-add block). Real bindings will overwrite them on next
 * generation.
 *
 * Status shape mirrors the WP02 backend `update.Status` struct:
 *   currentVersion: semver of the running binary
 *   available     : true iff a newer version on the configured channel exists
 *   availableVersion: semver of the offered upgrade (only set when available)
 *   channel       : 'stable' | 'beta' | 'dev'
 *   downloadState : staged-download lifecycle
 *   downloadProgress: 0..1, only meaningful while downloading
 *   notes         : short markdown blurb pulled from the release manifest
 *   releaseUrl    : GitHub Releases URL for the offered version
 *   skippedByUser : true iff the user already chose Skip on this version
 */

export interface UpdateStatus {
  currentVersion: string;
  available: boolean;
  availableVersion?: string;
  channel: string;
  downloadState: 'idle' | 'downloading' | 'staged' | 'failed';
  downloadProgress?: number;
  notes?: string;
  releaseUrl?: string;
  skippedByUser?: boolean;
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
  /** Persist a "skip this version" choice; backend won't re-prompt. */
  skipVersion(version: string): Promise<void>;
}

interface BridgeShape {
  Update_Status: () => Promise<UpdateStatus>;
  Update_StartCheck: () => Promise<void>;
  Update_StartDownload: () => Promise<void>;
  Update_Apply: () => Promise<void>;
  Update_SkipVersion: (version: string) => Promise<void>;
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
    skipVersion: (version) => bridge().Update_SkipVersion(version),
  };
}

/**
 * createFakeUpdateClient — stub used by UpdateIndicator / UpdateMenu tests
 * (and dev builds where the live bridge isn't wired yet). `seed` lets a
 * caller inject specific behaviour; everything else returns a safe no-op.
 *
 * Default `status()` returns a "no update available" snapshot so consumers
 * boot into the hidden state.
 */
export function createFakeUpdateClient(
  seed: Partial<UpdateClient> = {},
): UpdateClient {
  return {
    status:
      seed.status ??
      (() =>
        Promise.resolve({
          currentVersion: '0.4.0',
          available: false,
          channel: 'stable',
          downloadState: 'idle',
        })),
    startCheck: seed.startCheck ?? (() => Promise.resolve()),
    startDownload: seed.startDownload ?? (() => Promise.resolve()),
    apply: seed.apply ?? (() => Promise.resolve()),
    skipVersion: seed.skipVersion ?? (() => Promise.resolve()),
  };
}
