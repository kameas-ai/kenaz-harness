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

const isSignedIn = computed(() => !!identity.value && identity.value !== false);

const badgeColor = computed(() => profile.value?.badgeColor ?? '');
const envName = computed(() => (profile.value?.name ?? '').toUpperCase());

const tierLabel = computed(() => {
  const id = identity.value;
  if (!id || id === false) return '';
  return id.tier ?? '';
});

// ── actions ───────────────────────────────────────────────────────────────

async function signIn() {
  loading.value = true;
  error.value = '';
  try {
    identity.value = await client.settings.fleetSignIn();
  } catch (e: any) {
    error.value = e?.message ?? 'Sign-in failed. Please try again.';
    identity.value = false;
  } finally {
    loading.value = false;
  }
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
    loading.value = false;
  }
}

async function refreshIdentity() {
  loading.value = true;
  error.value = '';
  try {
    identity.value = await client.settings.fleetRefreshIdentity();
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
      <p class="banner-title">Fleet disabled by environment</p>
      <p class="banner-body">
        The HARNESS_FLEET_DISABLED environment variable is set. Fleet features
        are unavailable. Unset this variable and restart the harness to enable
        fleet integration.
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
