/**
 * fleetSession.ts — THE fleet-session store (fleet-session-truth-01DOGF0A).
 *
 * Dogfood 2026-10-04: seven surfaces gave four different answers about one
 * fleet session. UserMenu said signed OUT (it collapsed any enroll error to
 * "signed out"), Settings › Account said signed IN, its header said "Sign in
 * to access…", the rail gates said whatever boot AppInfo said, and Contexts
 * said team sync was off. Each surface ran its own reads on its own clock.
 *
 * This module is the only fleet-session state the frontend has:
 *
 *   - ONE backend snapshot (`Settings_FleetSession`, FR-1), read once on
 *     first use and again after each action or window focus;
 *   - kept live by ONE subscription to `fleet:session-changed` (FR-2), the
 *     full snapshot pushed on every backend transition;
 *   - every fleet surface — UserMenu, Account panel + section head,
 *     featureFlags' `signedIn` / `capability`, LeftRail, ContextsView,
 *     CedarEditor — reads it. None of them polls.
 *
 * `degraded` (tokens usable, last identity refresh failed) counts as signed
 * in: the popover never offers "Sign in" while tokens are valid (FR-3).
 */

import { computed, effectScope, ref, type EffectScope } from 'vue';
import { useEventStream } from './useEventStream';
import { ORG_PAUSED_REASON } from './orgPausedCopy';
import type {
  FleetIdentity,
  FleetSessionState,
  FleetSessionView,
} from './types';

/** Broker topic the backend pushes snapshots on (core/fleet TopicFleetSessionChanged). */
export const FLEET_SESSION_CHANGED = 'fleet:session-changed';

/**
 * FOCUS_RETRY_MS — after automatic retries have stopped (not provisioned,
 * FR-4), regaining window focus re-attempts the identity refresh once this
 * long has passed since the last attempt. The user likely went to the fleet
 * SPA to finish signup; coming back is the moment to look again.
 */
export const FOCUS_RETRY_MS = 10 * 60 * 1000;

/** The slice of the harness client the store needs (structural, test-friendly). */
export interface FleetSessionSource {
  settings: {
    fleetSession(): Promise<FleetSessionView>;
    fleetRefreshIdentity(): Promise<unknown>;
    fleetSignIn(): Promise<unknown>;
    fleetSignOut(): Promise<void>;
    fleetSignInCancel?(): Promise<void>;
  };
}

const _session = ref<FleetSessionView | null>(null);
let _client: FleetSessionSource | null = null;
let _scope: EffectScope | null = null;
let _readSeq = 0;
let _focusHandler: (() => void) | null = null;

/**
 * True while the store holds only the boot-time AppInfo seed (below), not a
 * real backend snapshot. A real snapshot always replaces a seed; a seed
 * never replaces a real snapshot.
 */
let _seeded = false;

/** Install a snapshot (the event handler; tests seed through it too). */
export function applyFleetSession(v: FleetSessionView | null): void {
  _seeded = false;
  _session.value = v;
}

/**
 * seedFleetSessionFromAppInfo installs the capability map boot AppInfo
 * already carries, so the gates can open on the same tick the app boots —
 * before the Settings_FleetSession read lands. It is the SAME backend data
 * (AppInfo's map is the capability poller's set, masked to empty while
 * default-deny), not a second source: the first real snapshot replaces it,
 * and it never overwrites one. `null` clears a seed (fail closed).
 * Called only by lib/featureFlags.ts's initFeatureFlags.
 */
export function seedFleetSessionFromAppInfo(caps: Record<string, boolean> | null | undefined): void {
  if (_session.value && !_seeded) return;
  if (!caps) {
    _seeded = false;
    _session.value = null;
    return;
  }
  const lane = { status: 'unknown', consecutiveFailures: 0 };
  _seeded = true;
  _session.value = {
    state: Object.keys(caps).length > 0 ? 'signed_in' : 'signed_out',
    autoRetry: true,
    tokensUsable: Object.keys(caps).length > 0,
    claims: { hasSubject: false, hasOrgClaim: false },
    capabilities: { tier: '', enabled: { ...caps }, fetchedAt: '', source: 'appinfo' },
    sync: { contextSync: { ...lane }, unitPoll: { ...lane }, telemetry: { ...lane }, catalogRevocation: { ...lane } },
    updatedAt: '',
  };
}

function ensureSubscribed(): void {
  if (_scope) return;
  _scope = effectScope(true);
  _scope.run(() => {
    // The single subscriber of fleet:session-changed.
    // Literal (not FLEET_SESSION_CHANGED) so check-served-mode-topic-forwarding
    // can see this consumer.
    useEventStream<FleetSessionView>('fleet:session-changed', (v) => {
      // A pushed snapshot is newer than any read still in flight.
      _readSeq++;
      applyFleetSession(v);
    });
  });
  if (typeof window !== 'undefined') {
    _focusHandler = () => void onWindowFocus();
    window.addEventListener('focus', _focusHandler);
  }
}

/**
 * refreshFleetSession re-reads the snapshot. Never throws. A transport error
 * keeps the previous snapshot rather than inventing "signed out"; with no
 * previous snapshot the store stays null ("unknown"), which every reader
 * treats as not signed in AND not offering a sign-in it cannot back.
 */
export async function refreshFleetSession(client?: FleetSessionSource): Promise<void> {
  if (client) _client = client;
  const c = _client;
  if (!c) return;
  // Last-request-wins: a read issued before an action (sign-in) must not
  // land after the post-action read and roll the store back.
  const mySeq = ++_readSeq;
  try {
    const v = await c.settings.fleetSession();
    if (mySeq === _readSeq) applyFleetSession(v);
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    if (mySeq === _readSeq && msg.includes('disabled by env')) {
      applyFleetSession(disabledSnapshot());
    }
    // Otherwise keep what we had: an RPC hiccup is not a session change.
  }
}

async function onWindowFocus(): Promise<void> {
  const s = _session.value;
  if (
    s &&
    s.state === 'degraded' &&
    !s.autoRetry &&
    (!s.lastAttemptAt || Date.now() - Date.parse(s.lastAttemptAt) >= FOCUS_RETRY_MS)
  ) {
    await retryFleetSession();
    return;
  }
  await refreshFleetSession();
}

/** Explicit retry (FR-4 recovery path): one identity refresh, then re-read. */
export async function retryFleetSession(): Promise<void> {
  const c = _client;
  if (!c) return;
  try {
    await c.settings.fleetRefreshIdentity();
  } catch {
    // The failure is recorded backend-side and arrives in the snapshot.
  }
  await refreshFleetSession();
}

/**
 * signInFleet runs the sign-in flow. Resolves to the error (or null) so the
 * caller can route to the full Account panel on failure; the session state
 * itself always comes from the snapshot.
 */
export async function signInFleet(): Promise<unknown | null> {
  const c = _client;
  if (!c) return new Error('fleet session store has no client');
  let err: unknown | null = null;
  try {
    await c.settings.fleetSignIn();
  } catch (e) {
    err = e;
  }
  await refreshFleetSession();
  return err;
}

/**
 * cancelSignInFleet cancels an in-flight sign-in (FR-5: "Waiting for
 * browser… Cancel"). Best-effort; the pending signIn resolves with the
 * cancellation and the snapshot returns to signed_out.
 */
export async function cancelSignInFleet(): Promise<void> {
  const c = _client;
  if (!c?.settings.fleetSignInCancel) return;
  try {
    await c.settings.fleetSignInCancel();
  } catch {
    // Nothing in flight / bridge gone: the snapshot is the truth either way.
  }
  await refreshFleetSession();
}

/** True for the error a cancelled sign-in flow resolves with. */
export function isSignInCancelled(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? '');
  return msg.includes('context canceled');
}

/** signOutFleet signs out; resolves to the error (or null). */
export async function signOutFleet(): Promise<unknown | null> {
  const c = _client;
  if (!c) return new Error('fleet session store has no client');
  let err: unknown | null = null;
  try {
    await c.settings.fleetSignOut();
  } catch (e) {
    err = e;
  }
  await refreshFleetSession();
  return err;
}

function disabledSnapshot(): FleetSessionView {
  const lane = { status: 'unknown', consecutiveFailures: 0 };
  return {
    state: 'disabled',
    autoRetry: false,
    tokensUsable: false,
    claims: { hasSubject: false, hasOrgClaim: false },
    capabilities: { tier: '', enabled: {}, fetchedAt: '', source: 'default-deny' },
    sync: { contextSync: { ...lane }, unitPoll: { ...lane }, telemetry: { ...lane }, catalogRevocation: { ...lane } },
    updatedAt: '',
  };
}

// ── derived views (module-level so non-component code can read them) ────────

/** The raw snapshot (null until the first read lands). */
export const fleetSession = computed<FleetSessionView | null>(() => _session.value);

/** Session state; 'unknown' until the first snapshot. */
export const fleetSessionState = computed<FleetSessionState | 'unknown'>(
  () => _session.value?.state ?? 'unknown',
);

/**
 * True while the session's tokens are usable: signed_in OR degraded. A
 * degraded session is still signed in — it must never show "Sign in" (FR-3).
 */
export const fleetSignedIn = computed<boolean>(() => {
  const s = _session.value;
  const st = s?.state;
  if (st === 'signed_in' || st === 'degraded') return true;
  // A re-auth (Update sign-in) runs the flow with the old tokens still
  // valid: the session has not ended, so no gate closes for the up-to-10
  // minutes the browser flow may take (review F5).
  return st === 'signing_in' && s?.tokensUsable === true;
});

export const fleetIdentity = computed<FleetIdentity | null>(
  () => (fleetSignedIn.value ? (_session.value?.identity ?? null) : null),
);

/**
 * Primary label for the identity header. Never empty while signed in
 * (FR-9 / P-9: enroll returned email:"" and the popover rendered a blank
 * line): email → display name → org name → "Signed in".
 */
export const fleetIdentityLabel = computed<string>(() => {
  if (!fleetSignedIn.value) return '';
  const id = _session.value?.identity;
  return (
    id?.email?.trim() ||
    id?.displayName?.trim() ||
    id?.orgName?.trim() ||
    'Signed in'
  );
});

/**
 * Avatar initials. Never blank while signed in: name/email initials, else
 * the org name's initial (FR-9), else "·".
 */
export const fleetInitials = computed<string>(() => {
  if (!fleetSignedIn.value) return '';
  const id = _session.value?.identity;
  const source = id?.displayName?.trim() || id?.email?.trim() || '';
  const parts = source.split(/[\s@]+/).filter(Boolean);
  if (parts.length > 0) {
    return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase().slice(0, 2);
  }
  const org = id?.orgName?.trim();
  if (org) return org[0]!.toUpperCase();
  return '·';
});

/**
 * fleetSessionCapability(key) — true only when signed in AND the snapshot's
 * capability set enables `key`. The one capability truth FR-8 asks for.
 */
export function fleetSessionCapability(key: string): boolean {
  if (!fleetSignedIn.value) return false;
  const caps = _session.value?.capabilities;
  if (!caps || caps.source === 'default-deny') return false;
  return caps.enabled?.[key] === true;
}

/**
 * describeFleetReason turns a snapshot reason code into short UI copy.
 * The raw message stays available for detail rows.
 */
export function describeFleetReason(reason: string | undefined): string {
  switch (reason) {
    case 'network':
      return "Can't reach fleet";
    case 'not_provisioned':
      return 'Fleet account setup not finished';
    case 'server_error':
      return 'Fleet returned an error';
    case 'not_configured':
      return 'Fleet is not configured in this build';
    case 'session_expired':
      return 'Your fleet session expired';
    case 'sign_in_cancelled':
      return 'Sign-in cancelled';
    case 'sign_in_failed':
      return 'Sign-in failed';
    case 'needs_reauth':
      return 'Update your sign-in — telemetry export is off';
    case 'node_removed':
      return 'This device was removed by an org admin';
    default:
      return reason ? `Fleet: ${reason}` : '';
  }
}

/**
 * formatRoles renders FleetIdentity.roles for the identity header and the
 * Account panel (FR-9 / dogfood F8b — the payload carried roles and no
 * surface showed them): "org_owner" → "Org owner", joined with ", ".
 */
export function formatRoles(roles: string[] | undefined | null): string {
  if (!roles || roles.length === 0) return '';
  return roles
    .map((r) => r.trim())
    .filter(Boolean)
    .map((r) => {
      const words = r.replace(/[_-]+/g, ' ').trim();
      return words.charAt(0).toUpperCase() + words.slice(1);
    })
    .join(', ');
}

/**
 * fleetOrgPaused — a Kameas-staff "pause paid features" hold is on the org
 * (kenaz-fleet PR 206). Not a tier answer and not a sign-out: surfaces render
 * OrgPausedBanner instead of their tier-gated / upsell copy.
 */
export const fleetOrgPaused = computed<boolean>(() => _session.value?.paused === true);

/** The paused category ('' when not paused). */
export const fleetPausedCategory = computed<string>(() =>
  fleetOrgPaused.value ? (_session.value?.pausedCategory ?? 'other') : '',
);

/** Short copy for a sync lane's reason code (FR-6). */
export function describeSyncReason(reason: string | undefined): string {
  switch (reason) {
    case ORG_PAUSED_REASON:
      return "paused by your organization's account status";
    case 'remote_context_missing':
      return 'remote context missing on fleet';
    case 'fleet_api_not_routed':
      return 'a proxy in front of fleet answered with an error page';
    case 'fleet_endpoint_unsupported':
      return "this fleet server doesn't support session sync — events stay local";
    case 'not_authorized':
      return 'not authorized by fleet';
    case 'server_error':
      return 'fleet returned an error';
    case 'network':
      return "can't reach fleet";
    case 'session_expired':
      return 'fleet session expired';
    case 'no_resource_owner_claim':
      return 'your token has no org claim — sign in again';
    case 'activate_failed':
      return 'export could not start';
    case 'api_host_unresolved':
      return "can't resolve the fleet API host";
    // Catalog revocation sweep (skill-library-01SKLIB01 WP03).
    case 'not_entitled':
      return 'your plan does not include the org catalog';
    case 'signed_out':
      return 'signed out';
    case 'list_failed':
      return "couldn't read the org catalog — nothing was removed";
    case 'enumerate_failed':
      return "couldn't read what is installed — nothing was removed";
    case 'uninstall_failed':
      return "a revoked item couldn't be removed";
    default:
      return reason ?? '';
  }
}

export interface DegradedLane {
  key: 'contextSync' | 'unitPoll' | 'telemetry' | 'catalogRevocation';
  label: string;
  reason: string;
  consecutiveFailures: number;
  lastSuccessAt?: string;
}

/**
 * fleetDegradedLanes — the background lanes currently failing (FR-6). Each
 * is shown, with its reason and last success, instead of only being logged.
 */
export const fleetDegradedLanes = computed<DegradedLane[]>(() => {
  const sync = _session.value?.sync;
  if (!sync || !fleetSignedIn.value) return [];
  const paused = fleetOrgPaused.value;
  const out: DegradedLane[] = [];
  const add = (key: DegradedLane['key'], label: string) => {
    const lane = sync[key];
    // While the org is paused the banner speaks for every lane the hold
    // stopped: no per-lane failure, and never the tier copy for an
    // entitlement the pause (not the plan) withheld.
    if (paused && (lane?.reason === ORG_PAUSED_REASON || lane?.reason === 'not_entitled')) return;
    // An Off lane is normally deliberate (consent, entitlement) and hidden —
    // except when the backend latched the fleet route as unsupported: sync
    // was asked for and is not happening, so it is shown like a failure.
    if (lane?.status === 'degraded' || (lane?.status === 'off' && lane.reason === SYNC_UNSUPPORTED_REASON)) {
      out.push({
        key,
        label,
        reason: describeSyncReason(lane.reason),
        consecutiveFailures: lane.consecutiveFailures,
        lastSuccessAt: lane.lastSuccessAt,
      });
    }
  };
  add('contextSync', 'Context sync');
  add('unitPoll', 'Shared units');
  add('telemetry', 'Telemetry');
  add('catalogRevocation', 'Revoked-item check');
  return out;
});

/** Lane reason the backend publishes when fleet has no session-sync route. */
export const SYNC_UNSUPPORTED_REASON = 'fleet_endpoint_unsupported';

/**
 * fleetSessionSyncUnsupported — true once the backend has latched the fleet
 * session-sync route as absent (core/fleet/unsupported_endpoint.go): the
 * context-sync lane is Off with reason fleet_endpoint_unsupported. Process-
 * wide (not per session) and cleared on sign-in. Sync toggles must not claim
 * "Synced to fleet" while it holds — events stay local.
 */
export const fleetSessionSyncUnsupported = computed<boolean>(() => {
  const lane = _session.value?.sync?.contextSync;
  return lane?.status === 'off' && lane.reason === SYNC_UNSUPPORTED_REASON;
});

/**
 * fleetSessionSyncFailure — the context-sync breaker state for one chat
 * session, or null when it is syncing fine (or not synced at all). Drives
 * the session header's "Not syncing — <reason>" badge (FR-6, dogfood F7).
 */
export function fleetSessionSyncFailure(sessionId: string | null | undefined) {
  if (!sessionId) return null;
  const sessions = _session.value?.sync?.contextSync?.sessions ?? [];
  return sessions.find((x) => x.sessionId === sessionId) ?? null;
}

/**
 * accountSectionSubtitle derives Settings › Account's section-head subtitle
 * from a snapshot (FR-10). Returns '' for signed-out / unknown so the caller
 * keeps its "Sign in to access…" explainer — the only state it is true in.
 */
export function accountSectionSubtitle(s: FleetSessionView | null): string {
  if (!s) return '';
  const id = s.identity;
  const label =
    id?.email?.trim() || id?.displayName?.trim() || id?.orgName?.trim() || '';
  const org = id?.orgName?.trim() && id.orgName.trim() !== label ? id.orgName.trim() : '';
  const who = label ? `Signed in as ${label}${org ? ` · ${org}` : ''}` : 'Signed in';
  switch (s.state) {
    case 'signed_in':
      return who;
    case 'degraded':
      return `${who} · ${describeFleetReason(s.reason)}`;
    case 'signing_in':
      return 'Waiting for the browser sign-in to finish…';
    case 'disabled':
      return 'Fleet features are disabled in this build.';
    default:
      return '';
  }
}

/**
 * useFleetSession — the component entry point. Pass the harness client; the
 * first caller triggers the initial read and the single event subscription.
 */
export function useFleetSession(client?: FleetSessionSource) {
  ensureSubscribed();
  // One initial read per client; later consumers share it, and the event
  // subscription keeps it current after that.
  if (client && client !== _client) {
    void refreshFleetSession(client);
  }
  return {
    session: fleetSession,
    state: fleetSessionState,
    signedIn: fleetSignedIn,
    identity: fleetIdentity,
    identityLabel: fleetIdentityLabel,
    initials: fleetInitials,
    capability: fleetSessionCapability,
    degradedLanes: fleetDegradedLanes,
    orgPaused: fleetOrgPaused,
    pausedCategory: fleetPausedCategory,
    refresh: refreshFleetSession,
    retry: retryFleetSession,
    signIn: signInFleet,
    signOut: signOutFleet,
    cancelSignIn: cancelSignInFleet,
  };
}

/** Test-only: drop all module state (snapshot, client, subscription). */
export function _resetFleetSessionForTest(): void {
  _scope?.stop();
  _scope = null;
  if (_focusHandler && typeof window !== 'undefined') {
    window.removeEventListener('focus', _focusHandler);
  }
  _focusHandler = null;
  _client = null;
  _readSeq = 0;
  _seeded = false;
  _session.value = null;
}
