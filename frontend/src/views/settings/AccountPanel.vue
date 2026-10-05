<script setup lang="ts">
/**
 * AccountPanel — Fleet identity + sign-in panel for Settings → Account.
 *
 * fleet-session-truth-01DOGF0A WP04: renders the shared fleet session store
 * (lib/fleetSession.ts) instead of its own fleetSignedIn() +
 * fleetRefreshIdentity() reads. Before this it was one of three surfaces
 * with its own answer: during the 2026-10-04 dogfood it showed the user
 * signed in while the UserMenu popover, reading its own copy, said signed
 * out (F5).
 *
 * Render states (from the snapshot):
 *   1. disabled (HARNESS_FLEET_DISABLED=1): banner only, no sign-in CTA.
 *   2. signed_out / signing_in / unknown: "Sign in to fleet" (or "Waiting
 *      for browser…") + explainer + env badge if not prod.
 *   3. signed_in / degraded: identity card + Refresh / Sign out; degraded
 *      adds the reason, and for not-provisioned the "Finish signup" link.
 *
 * Errors from an action the user just took (sign-in, refresh) are kept
 * locally and humanized; everything about the SESSION comes from the store.
 *
 * OSS-first contract: this component is INVISIBLE to the user until they
 * open Settings → Account. No fleet affordances appear anywhere else in
 * the UI when the user is signed out.
 */
import { computed, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { refreshFeatureFlags } from '@/lib/featureFlags';
import { isUserNotProvisionedError } from '@/lib/errors';
import { describeFleetReason, useFleetSession } from '@/lib/fleetSession';

const client = useHarnessClient();
const fleet = useFleetSession(client);

// ── state ────────────────────────────────────────────────────────────────

const loading = ref(false);
/** Humanized error from the user's last action in this panel. */
const actionError = ref('');
/** The last action failed with ErrUserNotProvisioned (link target below). */
const actionSignupRequired = ref(false);

// ── computed ──────────────────────────────────────────────────────────────

const state = fleet.state;
const fleetDisabled = computed(() => state.value === 'disabled');
/** signed_in OR degraded — tokens are usable (FR-3). */
const isSignedIn = fleet.signedIn;
const isDegraded = computed(() => state.value === 'degraded');
const isSigningIn = computed(() => state.value === 'signing_in');
const identity = fleet.identity;
const profile = computed(() => fleet.session.value?.profile ?? null);

const badgeColor = computed(() => profile.value?.badgeColor ?? '');
const envName = computed(() => (profile.value?.name ?? '').toUpperCase());
const tierLabel = computed(() => identity.value?.tier ?? '');

/** Not-provisioned, from the session (mount) or from the last action. */
const signupRequired = computed(
  () =>
    actionSignupRequired.value ||
    (isDegraded.value && fleet.session.value?.reason === 'not_provisioned'),
);

/** Session-level message: degraded reason, or why a session ended. */
const sessionMessage = computed(() => {
  const s = fleet.session.value;
  if (!s) return '';
  if (s.state === 'degraded') {
    if (s.reason === 'not_provisioned') {
      return "You signed in with Zitadel, but this account hasn't finished Fleet signup yet.";
    }
    if (s.reason === 'needs_reauth') {
      return (
        'Your sign-in predates a permission fleet now needs (your organisation claim). ' +
        'Everything else keeps working; sign in again to re-enable telemetry export.'
      );
    }
    return `${describeFleetReason(s.reason)} — showing your last known account details.`;
  }
  if (s.state === 'signed_out' && s.reason === 'session_expired') {
    return 'Your session expired. Sign in again.';
  }
  return '';
});

const needsReauth = computed(
  () => isDegraded.value && fleet.session.value?.reason === 'needs_reauth',
);
const degradedHeadline = computed(() => {
  switch (fleet.session.value?.reason) {
    case 'not_provisioned':
      return 'Account setup not finished';
    case 'needs_reauth':
      return 'Update your sign-in to re-enable telemetry export';
    default:
      return 'Not connected to fleet';
  }
});
/** Background lanes currently failing (FR-6). */
const degradedLanes = fleet.degradedLanes;

/** One line under the actions: the action's own error wins. */
const error = computed(() => actionError.value || sessionMessage.value);

// ── actions ───────────────────────────────────────────────────────────────

async function signIn() {
  loading.value = true;
  actionError.value = '';
  actionSignupRequired.value = false;
  // Pre-flight: if the build hasn't populated the env profile's client_id,
  // explain why sign-in is unavailable instead of trying and failing with
  // an opaque message.
  if (profile.value && !profile.value.configured) {
    actionError.value =
      `Sign-in is not available for the "${profile.value.name}" build profile — ` +
      `the identity provider is not configured in this build. ` +
      `This is a build-pipeline gap; contact your administrator.`;
    loading.value = false;
    return;
  }
  try {
    const err = await fleet.signIn();
    if (err) {
      // Branch on the sentinel, not a substring of the raw fleet server
      // response (core/fleet/identity.go wraps ErrUserNotProvisioned with a
      // stable prefix) — this is what lets the template render a real link
      // instead of the raw JSON body.
      actionSignupRequired.value = isUserNotProvisionedError(err);
      const raw = err instanceof Error ? err.message : String(err ?? '');
      actionError.value = humanizeFleetError(raw);
    }
    // Sign-in mutates fleet state long after boot; the capability gates must
    // follow (docs/dead-code-audit-2026-08-16.md finding A4, part 2).
    await refreshFeatureFlags(client);
  } finally {
    loading.value = false;
  }
}

// humanizeFleetError maps the raw Go error string into an actionable
// message for the user. Patterns mirror the sentinel error wording in
// core/fleet/errors.go.
function humanizeFleetError(raw: string): string {
  if (!raw) return 'Sign-in failed. Please try again.';
  if (isUserNotProvisionedError(raw)) {
    return "You signed in with Zitadel, but this account hasn't finished Fleet signup yet.";
  }
  if (raw.includes('env profile not populated')) {
    return (
      'Sign-in is not available: the identity provider is not configured in this build. ' +
      'Contact your administrator to set up fleet integration.'
    );
  }
  if (raw.includes('dashboard SPA fall-through') || raw.includes('returned HTML')) {
    return (
      'Sign-in succeeded, but the fleet server returned an unexpected response. ' +
      'The fleet ingress may not be routing API requests correctly. ' +
      'Contact your fleet administrator; your session tokens are saved locally.'
    );
  }
  if (raw.includes('server unreachable') || raw.includes('no such host')) {
    return (
      'Can\'t reach the fleet server. Check your VPN connection (LLE fleet is ' +
      'VPN-gated) and retry.'
    );
  }
  if (raw.includes('connection refused') || raw.includes('i/o timeout')) {
    return (
      'Fleet server is not responding. The deployment may be offline. Retry ' +
      'in a minute, or check fleet status with your team.'
    );
  }
  if (raw.includes('refresh failed') || raw.includes('re-sign-in required')) {
    return 'Your session expired. Sign in again.';
  }
  if (raw.includes('enroll route not registered')) {
    return (
      'Sign-in succeeded, but the fleet server is not ready for enrollment yet. ' +
      'Contact your administrator. Your access token is saved locally in the meantime.'
    );
  }
  if (raw.includes('state mismatch') || raw.includes('ErrStateMismatch')) {
    return (
      'Sign-in request expired or was redirected unexpectedly. ' +
      'Try signing in again from a fresh tab.'
    );
  }
  if (raw.includes('disabled by env')) {
    return (
      'Fleet integration has been disabled by an administrator setting. ' +
      'Contact your administrator to re-enable fleet features.'
    );
  }
  // Fall back to the raw error — at least it\'s informative for a developer.
  return raw;
}

async function signOut() {
  loading.value = true;
  actionError.value = '';
  try {
    const err = await fleet.signOut();
    if (err) {
      actionError.value = err instanceof Error ? err.message : 'Sign-out failed.';
    }
  } finally {
    // FleetSignOut stops the capability poller and clears tokens *before* it
    // reports a partial keychain failure, so the session is gone whether or
    // not the RPC resolved cleanly — the gates must close either way (the
    // fail-OPEN direction is the one that matters). refreshFeatureFlags
    // never throws.
    await refreshFeatureFlags(client);
    loading.value = false;
  }
}

async function refreshIdentity() {
  loading.value = true;
  actionError.value = '';
  actionSignupRequired.value = false;
  try {
    await client.settings.fleetRefreshIdentity();
  } catch (e: unknown) {
    // Same terminal condition as signIn(): route through humanizeFleetError
    // so a re-enroll that starts 403ing renders the actionable message +
    // link, not raw JSON. The session itself turns degraded via the store.
    actionSignupRequired.value = isUserNotProvisionedError(e);
    const raw = e instanceof Error ? e.message : String(e ?? 'Refresh failed.');
    actionError.value = humanizeFleetError(raw);
  } finally {
    await fleet.refresh();
    // Re-enrolment is where a tier change lands; capabilities follow it.
    await refreshFeatureFlags(client);
    loading.value = false;
  }
}
</script>

<template>
  <!-- ── Disabled state ─────────────────────────────────────────────────── -->
  <div v-if="fleetDisabled" class="account-panel">
    <div class="panel-banner disabled-banner" data-testid="fleet-disabled-banner">
      <p class="banner-title">Fleet features disabled</p>
      <p class="banner-body">
        Fleet integration has been disabled by an administrator setting.
        Contact your administrator to enable fleet features.
      </p>
    </div>
  </div>

  <!-- ── Signed-out state ──────────────────────────────────────────────── -->
  <div v-else-if="!isSignedIn" class="account-panel" data-testid="signed-out-panel">
    <div class="panel-header">
      <h2 class="panel-title">Account</h2>
      <span
        v-if="badgeColor"
        class="env-badge"
        :class="`env-badge--${badgeColor}`"
        data-testid="env-badge"
      >{{ envName }}</span>
    </div>
    <p class="panel-explainer">
      Sign in to access fleet features like shared team context, org-level
      settings, and role-based capabilities.
    </p>
    <div class="panel-actions">
      <button
        class="btn btn-primary"
        :disabled="loading || isSigningIn"
        data-testid="sign-in-btn"
        @click="signIn"
      >
        {{ loading || isSigningIn ? 'Waiting for browser…' : 'Sign in to fleet' }}
      </button>
    </div>
    <p v-if="error" class="error-msg" data-testid="error-msg">
      {{ error }}
      <a
        v-if="signupRequired && profile?.fleetBaseUrl"
        :href="profile.fleetBaseUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="finish-signup-link"
        data-testid="finish-signup-link"
      >Finish signup at {{ profile.fleetBaseUrl }}</a>
    </p>
  </div>

  <!-- ── Signed-in state ───────────────────────────────────────────────── -->
  <div v-else class="account-panel" data-testid="signed-in-panel">
    <div class="panel-header">
      <h2 class="panel-title">Account</h2>
      <span
        v-if="badgeColor"
        class="env-badge"
        :class="`env-badge--${badgeColor}`"
        data-testid="env-badge"
      >{{ envName }}</span>
    </div>

    <p
      v-if="isDegraded"
      class="degraded-banner"
      role="status"
      data-testid="account-degraded"
    >
      {{ degradedHeadline }}
    </p>
    <ul v-if="degradedLanes.length" class="degraded-lanes" data-testid="account-sync-lanes">
      <li v-for="lane in degradedLanes" :key="lane.key" :data-testid="`account-sync-${lane.key}`">
        {{ lane.label }}: not syncing — {{ lane.reason }}
      </li>
    </ul>

    <div class="identity-card" data-testid="identity-card">
      <div v-if="identity?.email" class="identity-row">
        <span class="identity-label">Email</span>
        <span class="identity-value" data-testid="identity-email">
          {{ identity.email }}
        </span>
      </div>
      <div v-if="tierLabel" class="identity-row">
        <span class="identity-label">Tier</span>
        <span class="identity-value tier-badge" :class="`tier-badge--${tierLabel}`" data-testid="tier-badge">
          {{ tierLabel }}
        </span>
      </div>
      <div v-if="identity?.orgName" class="identity-row">
        <span class="identity-label">Organisation</span>
        <span class="identity-value" data-testid="identity-org">
          {{ identity.orgName }}
        </span>
      </div>
      <div v-if="identity?.teamName" class="identity-row">
        <span class="identity-label">Team</span>
        <span class="identity-value" data-testid="identity-team">
          {{ identity.teamName }}
        </span>
      </div>
    </div>

    <div class="panel-actions">
      <button
        v-if="needsReauth"
        class="btn btn-primary"
        :disabled="loading"
        data-testid="reauth-btn"
        @click="signIn"
      >
        {{ loading ? 'Waiting for browser…' : 'Update sign-in' }}
      </button>
      <button
        class="btn btn-secondary"
        :disabled="loading"
        data-testid="refresh-btn"
        @click="refreshIdentity"
      >
        {{ loading ? 'Refreshing…' : isDegraded ? 'Retry' : 'Refresh' }}
      </button>
      <button
        class="btn btn-danger"
        :disabled="loading"
        data-testid="sign-out-btn"
        @click="signOut"
      >
        Sign out
      </button>
    </div>
    <p v-if="error" class="error-msg" data-testid="error-msg">
      {{ error }}
      <a
        v-if="signupRequired && profile?.fleetBaseUrl"
        :href="profile.fleetBaseUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="finish-signup-link"
        data-testid="finish-signup-link"
      >Finish signup at {{ profile.fleetBaseUrl }}</a>
    </p>
  </div>
</template>

<style scoped>
.account-panel {
  padding: 1.5rem;
  max-width: 480px;
}

.panel-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}

.panel-title {
  font-size: 1.125rem;
  font-weight: 600;
  margin: 0;
}

.panel-explainer {
  font-size: 0.875rem;
  color: var(--ink-muted);
  margin-bottom: 1rem;
}

.panel-banner {
  border-radius: 0.5rem;
  padding: 1rem;
}

.disabled-banner {
  background: var(--surface-2);
  border: 1px solid var(--warn);
}

.banner-title {
  font-weight: 600;
  margin: 0 0 0.5rem;
}

.banner-body {
  font-size: 0.875rem;
  color: var(--ink-muted);
  margin: 0;
}

.env-badge {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 0.1rem 0.45rem;
  border-radius: 0.25rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.env-badge--yellow {
  background: var(--warn);
  color: var(--surface-0);
}

.env-badge--blue {
  background: var(--info);
  color: var(--surface-0);
}

.env-badge--red {
  background: var(--danger);
  color: var(--surface-0);
}

.identity-card {
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: 0.5rem;
  padding: 1rem;
  margin-bottom: 1rem;
}

.identity-row {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.identity-row:last-child {
  margin-bottom: 0;
}

.identity-label {
  font-size: 0.75rem;
  color: var(--ink-muted);
  min-width: 90px;
}

.identity-value {
  font-size: 0.875rem;
}

.tier-badge {
  font-size: 0.7rem;
  font-weight: 600;
  padding: 0.1rem 0.4rem;
  border-radius: 0.2rem;
  text-transform: capitalize;
  background: var(--accent-dim);
  color: var(--accent);
}

.panel-actions {
  display: flex;
  gap: 0.5rem;
}

.btn {
  padding: 0.5rem 1rem;
  border: none;
  border-radius: 0.375rem;
  cursor: pointer;
  font-size: 0.875rem;
  font-weight: 500;
  transition: opacity 0.15s;
}

.btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.btn-primary {
  background: var(--accent);
  color: var(--surface-0);
}

.btn-secondary {
  background: var(--surface-3);
  color: var(--ink);
}

.btn-danger {
  background: var(--danger);
  color: var(--surface-0);
}

.degraded-banner {
  font-size: 0.8125rem;
  color: var(--warn);
  margin: 0 0 0.75rem;
}

.degraded-lanes {
  font-size: 0.8125rem;
  color: var(--warn);
  margin: 0 0 0.75rem;
  padding-left: 1rem;
}

.error-msg {
  font-size: 0.8125rem;
  color: var(--danger);
  margin-top: 0.5rem;
}

.finish-signup-link {
  display: block;
  margin-top: 0.35rem;
  color: var(--accent);
  text-decoration: underline;
}
</style>
