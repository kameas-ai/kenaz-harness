<script setup lang="ts">
/**
 * AccountPanel — Fleet identity + sign-in panel for Settings → Account.
 *
 * Three render states:
 *   1. Disabled (HARNESS_FLEET_DISABLED=1): banner only, no sign-in CTA.
 *   2. Signed-out: single "Sign in to fleet" button + brief explainer +
 *      env badge if not prod.
 *   3. Signed-in: email / tier badge / org_name / team_name / Refresh /
 *      Sign out + env badge if not prod.
 *
 * OSS-first contract: this component is INVISIBLE to the user until they
 * open Settings → Account. No fleet affordances appear anywhere else in
 * the UI when the user is signed out.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { refreshFeatureFlags } from '@/lib/featureFlags';
import type { FleetIdentity, FleetProfileInfo } from '@/lib/types';

const client = useHarnessClient();

// ── state ────────────────────────────────────────────────────────────────

/** null = not yet fetched; false = signed out; Identity = signed in */
const identity = ref<FleetIdentity | null | false>(null);
const profile = ref<FleetProfileInfo | null>(null);
const fleetDisabled = ref(false);
const loading = ref(false);
const error = ref('');

// ── lifecycle ─────────────────────────────────────────────────────────────

onMounted(async () => {
  await init();
});

async function init() {
  loading.value = true;
  error.value = '';
  try {
    // Try to fetch profile first — if fleet is disabled this will throw.
    profile.value = await client.settings.fleetProfile();
    const signedIn = await client.settings.fleetSignedIn();
    if (signedIn) {
      identity.value = await client.settings.fleetRefreshIdentity();
    } else {
      identity.value = false;
    }
  } catch (e: any) {
    const msg: string = e?.message ?? String(e);
    if (msg.includes('disabled by env')) {
      fleetDisabled.value = true;
      identity.value = false;
    } else {
      // Profile not configured or network error — treat as signed out.
      identity.value = false;
    }
  } finally {
    loading.value = false;
  }
}

// ── computed ──────────────────────────────────────────────────────────────

// Truthy only for a real identity object; null (unfetched) and false
// (signed out) are both falsy, so the cast is unnecessary.
const isSignedIn = computed(() => !!identity.value);

const badgeColor = computed(() => profile.value?.badgeColor ?? '');
const envName = computed(() => (profile.value?.name ?? '').toUpperCase());

const tierLabel = computed(() => {
  const id = identity.value;
  if (!id) return '';
  return id.tier ?? '';
});

// ── actions ───────────────────────────────────────────────────────────────

async function signIn() {
  loading.value = true;
  error.value = '';
  // Pre-flight: if the build hasn't populated the env profile's client_id,
  // explain why sign-in is unavailable instead of trying and failing with
  // an opaque message. The fleet client returns ErrProfileNotConfigured in
  // this case, but surfacing it ahead of time keeps the button responsive
  // and the explanation actionable.
  if (profile.value && !profile.value.configured) {
    error.value =
      `Sign-in is not available for the "${profile.value.name}" build profile — ` +
      `the identity provider is not configured in this build. ` +
      `This is a build-pipeline gap; contact your administrator.`;
    loading.value = false;
    return;
  }
  try {
    identity.value = await client.settings.fleetSignIn();
    // Sign-in mutates fleet state long after boot, when main.ts's
    // bootFeatureFlags has already run against a signed-out snapshot. Without
    // this the user stays fully gated — no Sites nav, no Publish-to-team, and
    // Settings → Sync still telling them to sign in — until they restart.
    // (docs/dead-code-audit-2026-08-16.md finding A4, part 2)
    await refreshFeatureFlags(client);
  } catch (e: any) {
    error.value = humanizeFleetError(e?.message ?? String(e ?? ''));
    identity.value = false;
  } finally {
    loading.value = false;
  }
}

// humanizeFleetError maps the raw Go error string into an actionable
// message for the user. Patterns mirror the sentinel error wording in
// core/fleet/errors.go.
function humanizeFleetError(raw: string): string {
  if (!raw) return 'Sign-in failed. Please try again.';
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
  error.value = '';
  try {
    await client.settings.fleetSignOut();
    identity.value = false;
  } catch (e: any) {
    error.value = e?.message ?? 'Sign-out failed.';
  } finally {
    // Outside the try on purpose: FleetSignOut stops the capability poller
    // and clears tokens *before* it reports a partial keychain failure, so the
    // session is gone whether or not the RPC resolved cleanly. Leaving the
    // capability snapshot behind would keep Publish-to-team and the Sites nav
    // on screen for a signed-out user — the fail-OPEN direction, and the one
    // that matters. refreshFeatureFlags never throws.
    await refreshFeatureFlags(client);
    loading.value = false;
  }
}

async function refreshIdentity() {
  loading.value = true;
  error.value = '';
  try {
    identity.value = await client.settings.fleetRefreshIdentity();
    // Re-enrolment is where a tier change lands (roles/tier come back from the
    // enroll endpoint), and tier is what the capability set is derived from.
    await refreshFeatureFlags(client);
  } catch (e: any) {
    error.value = e?.message ?? 'Refresh failed.';
  } finally {
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
        :disabled="loading"
        data-testid="sign-in-btn"
        @click="signIn"
      >
        {{ loading ? 'Opening browser…' : 'Sign in to fleet' }}
      </button>
    </div>
    <p v-if="error" class="error-msg" data-testid="error-msg">{{ error }}</p>
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

    <div class="identity-card" data-testid="identity-card">
      <div v-if="(identity as FleetIdentity).email" class="identity-row">
        <span class="identity-label">Email</span>
        <span class="identity-value" data-testid="identity-email">
          {{ (identity as FleetIdentity).email }}
        </span>
      </div>
      <div v-if="tierLabel" class="identity-row">
        <span class="identity-label">Tier</span>
        <span class="identity-value tier-badge" :class="`tier-badge--${tierLabel}`" data-testid="tier-badge">
          {{ tierLabel }}
        </span>
      </div>
      <div v-if="(identity as FleetIdentity).orgName" class="identity-row">
        <span class="identity-label">Organisation</span>
        <span class="identity-value" data-testid="identity-org">
          {{ (identity as FleetIdentity).orgName }}
        </span>
      </div>
      <div v-if="(identity as FleetIdentity).teamName" class="identity-row">
        <span class="identity-label">Team</span>
        <span class="identity-value" data-testid="identity-team">
          {{ (identity as FleetIdentity).teamName }}
        </span>
      </div>
    </div>

    <div class="panel-actions">
      <button
        class="btn btn-secondary"
        :disabled="loading"
        data-testid="refresh-btn"
        @click="refreshIdentity"
      >
        {{ loading ? 'Refreshing…' : 'Refresh' }}
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
    <p v-if="error" class="error-msg" data-testid="error-msg">{{ error }}</p>
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

.error-msg {
  font-size: 0.8125rem;
  color: var(--danger);
  margin-top: 0.5rem;
}
</style>
