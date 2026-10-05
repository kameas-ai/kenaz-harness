<script setup lang="ts">
/**
 * UserMenu — account status pill + fleet-identity popover.
 *
 * fleet-session-truth-01DOGF0A WP03: this component no longer reads fleet
 * state itself. It used to run its own refresh(): fleetSignedIn() then
 * fleetRefreshIdentity() on a 5-minute poll, and on ANY enroll error it set
 * identity=false — "signed out" — while Settings › Account showed the same
 * user signed in (dogfood F5). A user_not_provisioned error then stopped
 * the poll forever, freezing that lie until remount, and the bogus "Sign in"
 * row plausibly spawned the redundant browser flow that timed out (B3).
 *
 * Now it renders the shared store (lib/fleetSession.ts), which the backend
 * keeps live by pushing fleet:session-changed. No poll here (FR-2):
 *
 *   signed_in   avatar + identity header → Account settings → Sign out
 *   degraded    same, plus "<reason>" + Retry (and "Finish setup" for
 *               not-provisioned). Never "Sign in" while tokens are valid
 *               (FR-3).
 *   signing_in  "Waiting for browser…"
 *   signed_out  Sign in
 *   disabled    render nothing (OSS-first, HARNESS_FLEET_DISABLED=1)
 */
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { refreshFeatureFlags } from '@/lib/featureFlags';
import { describeFleetReason, isSignInCancelled, useFleetSession } from '@/lib/fleetSession';
import { isServedMode } from '@/lib/useServedMode';

const client = useHarnessClient();
const router = useRouter();
const fleet = useFleetSession(client);

const menuOpen = ref(false);
const loading = ref(false);
/** Inline error state for sign-out failure — visible near the sign-out button. */
const signOutError = ref<string | null>(null);

onMounted(() => {
  document.addEventListener('click', onDocumentClick);
});

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentClick);
});

const state = fleet.state;
const fleetDisabled = computed(() => state.value === 'disabled');
/** signed_in OR degraded: the tokens are usable (FR-3). */
const isSignedIn = fleet.signedIn;
const isDegraded = computed(() => state.value === 'degraded');
const isSigningIn = computed(() => state.value === 'signing_in');
/**
 * Offer "Sign in" only when the snapshot SAYS signed out. An unknown state
 * (no snapshot yet / RPC failed) offers nothing rather than a sign-in that
 * may be a lie.
 */
/**
 * Served mode: the host auth broker owns the session — there is no sign-in
 * or sign-out affordance (Settings_FleetSignIn/SignOut are not served).
 * The identity still renders from the served Settings_FleetSession.
 */
const served = isServedMode();
const canSignIn = computed(() => !served && state.value === 'signed_out');

const profile = computed(() => fleet.session.value?.profile ?? null);
const initials = fleet.initials;
const emailDisplay = fleet.identityLabel;
const badgeColor = computed(() => profile.value?.badgeColor ?? '');
const envName = computed(() => (profile.value?.name ?? '').toUpperCase());

const orgDisplay = computed(() => {
  const id = fleet.identity.value;
  if (!id) return '';
  // Already the primary label when email + name are absent (FR-9).
  return id.orgName && id.orgName !== emailDisplay.value ? id.orgName : '';
});
const tierLabel = computed(() => fleet.identity.value?.tier || '');

const degradedReason = computed(() =>
  isDegraded.value ? describeFleetReason(fleet.session.value?.reason) : '',
);
const needsSetup = computed(
  () => isDegraded.value && fleet.session.value?.reason === 'not_provisioned',
);
/**
 * needs_reauth: signed in, but the token predates the org-claim scope —
 * only a fresh sign-in fixes it (refresh keeps the old scopes). One click
 * re-runs the sign-in flow; nothing signs the user out.
 */
const needsReauth = computed(
  () => isDegraded.value && fleet.session.value?.reason === 'needs_reauth',
);
/** Background lanes currently failing (FR-6). */
const degradedLanes = fleet.degradedLanes;
/** The trigger chip: the session OR any sync lane is degraded. */
const showStatusChip = computed(
  () => isSignedIn.value && (isDegraded.value || degradedLanes.value.length > 0),
);
const statusChipTitle = computed(() =>
  [degradedReason.value, ...degradedLanes.value.map((l) => `${l.label}: ${l.reason}`)]
    .filter(Boolean)
    .join(' · '),
);

function openMenu() {
  menuOpen.value = !menuOpen.value;
}

function onDocumentClick(e: MouseEvent) {
  if (!menuOpen.value) return;
  const root = (e.target as HTMLElement | null)?.closest('[data-user-menu]');
  if (!root) menuOpen.value = false;
}

function close() {
  menuOpen.value = false;
}

function goToAccount() {
  close();
  void router.push('/settings?tab=account');
}

async function handleRetry() {
  loading.value = true;
  try {
    await fleet.retry();
  } finally {
    loading.value = false;
  }
}

async function handleSignIn() {
  // Click → kick off the PKCE flow directly. Browser opens, user
  // authenticates, then returns. On failure we route to /settings?tab=account
  // where AccountPanel surfaces the error in full.
  close();
  loading.value = true;
  try {
    const err = await fleet.signIn();
    // The capability gates (LeftRail Marketplace/Sites, Publish-to-team)
    // must follow a menu sign-in exactly as they follow an Account-panel
    // one (P-4: they used to stay stale until restart).
    await refreshFeatureFlags(client);
    // A user cancel is not an error to explain; anything else is.
    if (err && !isSignInCancelled(err)) void router.push('/settings?tab=account');
  } finally {
    loading.value = false;
  }
}

async function handleSignOut() {
  close();
  loading.value = true;
  signOutError.value = null;
  // (FR-003) Surface sign-out failures instead of silently ignoring them.
  const err = await fleet.signOut();
  await refreshFeatureFlags(client);
  if (err) {
    const msg = err instanceof Error ? err.message : String(err);
    signOutError.value = `Sign out failed: ${msg}. You may still be signed in.`;
    // Re-open the menu so the user sees the error inline.
    menuOpen.value = true;
  }
  loading.value = false;
}
</script>

<template>
  <!-- Fleet disabled: render nothing (OSS-first, spec FR-020) -->
  <template v-if="!fleetDisabled">
    <div class="relative" data-user-menu data-testid="user-menu">
      <button
        type="button"
        class="relative flex items-center gap-1.5 rounded-sm px-1.5 py-1 hover:bg-surface-2 transition-fast ease-kenaz"
        :aria-label="isSignedIn ? `Account (${emailDisplay})` : 'Account'"
        :aria-expanded="menuOpen"
        aria-haspopup="menu"
        data-testid="user-menu-trigger"
        @click.stop="openMenu"
      >
        <!-- Trigger glyph: avatar when signed-in, generic icon otherwise. -->
        <span
          v-if="isSignedIn"
          class="grid h-5 w-5 place-items-center rounded-full bg-accent-dim font-ui text-[10px] font-semibold text-accent"
          aria-hidden="true"
          data-testid="user-menu-avatar"
        >{{ initials }}</span>
        <span
          v-else
          class="grid h-5 w-5 place-items-center rounded-full bg-surface-3 text-ink-muted"
          aria-hidden="true"
        >
          <!-- User silhouette, matches kenaz-fleet dashboard's cil-user icon. -->
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="currentColor"
            aria-hidden="true"
          >
            <path d="M12 12c2.21 0 4-1.79 4-4s-1.79-4-4-4-4 1.79-4 4 1.79 4 4 4zm0 2c-2.67 0-8 1.34-8 4v2h16v-2c0-2.66-5.33-4-8-4z" />
          </svg>
        </span>
        <!-- Degraded: a status dot on the trigger (FR-3 / FR-6). -->
        <span
          v-if="showStatusChip"
          class="fleet-status-dot"
          :title="statusChipTitle"
          data-testid="user-menu-status-chip"
        />
        <span
          v-if="badgeColor && isSignedIn"
          class="env-badge"
          :class="`env-badge--${badgeColor}`"
          :title="`Environment: ${envName.toLowerCase()}`"
        >{{ envName }}</span>
      </button>

      <div
        v-if="menuOpen"
        role="menu"
        class="user-menu-popover"
        data-testid="user-menu-popover"
      >
        <!-- Identity header (signed-in or degraded) -->
        <template v-if="isSignedIn">
          <div class="user-menu-header" data-testid="user-menu-identity">
            <div class="user-menu-email">{{ emailDisplay }}</div>
            <div v-if="orgDisplay" class="user-menu-sub">{{ orgDisplay }}</div>
            <div v-if="tierLabel" class="user-menu-sub user-menu-tier">{{ tierLabel }}</div>
          </div>
          <div
            v-if="isDegraded"
            class="user-menu-degraded"
            role="status"
            data-testid="user-menu-degraded"
          >
            <span>{{ degradedReason }}</span>
            <a
              v-if="needsSetup && profile?.fleetBaseUrl"
              :href="profile.fleetBaseUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="user-menu-link"
              data-testid="menu-finish-setup"
            >Finish setup</a>
            <button
              v-if="needsReauth && !served"
              type="button"
              class="user-menu-link"
              :disabled="loading"
              data-testid="menu-reauth"
              @click="handleSignIn"
            >
              {{ loading ? 'Opening browser…' : 'Update sign-in' }}
            </button>
            <button
              v-else-if="!needsReauth"
              type="button"
              class="user-menu-link"
              :disabled="loading"
              data-testid="menu-retry"
              @click="handleRetry"
            >
              {{ loading ? 'Retrying…' : 'Retry' }}
            </button>
          </div>
          <div
            v-for="lane in degradedLanes"
            :key="lane.key"
            class="user-menu-degraded"
            role="status"
            :data-testid="`user-menu-sync-${lane.key}`"
          >
            <span>{{ lane.label }}: not syncing — {{ lane.reason }}</span>
          </div>
          <div class="user-menu-divider" />
        </template>

        <div
          v-if="isSigningIn"
          class="user-menu-degraded"
          role="status"
          data-testid="menu-signing-in"
        >
          <span>Waiting for browser…</span>
          <button
            v-if="!served"
            type="button"
            class="user-menu-link"
            data-testid="menu-sign-in-cancel"
            @click="fleet.cancelSignIn()"
          >
            Cancel
          </button>
        </div>

        <!-- Fleet account rows -->
        <button
          v-if="canSignIn"
          type="button"
          role="menuitem"
          class="user-menu-item"
          data-testid="menu-sign-in"
          :disabled="loading"
          @click="handleSignIn"
        >
          {{ loading ? 'Opening browser…' : 'Sign in' }}
        </button>
        <button
          v-if="isSignedIn"
          type="button"
          role="menuitem"
          class="user-menu-item"
          data-testid="menu-account"
          @click="goToAccount"
        >
          Account settings
        </button>
        <button
          v-if="isSignedIn && !served"
          type="button"
          role="menuitem"
          class="user-menu-item user-menu-item--danger"
          data-testid="menu-sign-out"
          :disabled="loading"
          @click="handleSignOut"
        >
          {{ loading ? 'Signing out…' : 'Sign out' }}
        </button>
        <!-- Sign-out failure inline error (FR-003) -->
        <p
          v-if="signOutError"
          class="px-2 pb-1 font-ui text-[10px] text-signal-danger"
          role="alert"
          data-testid="sign-out-error"
        >
          {{ signOutError }}
        </p>
      </div>
    </div>
  </template>
</template>

<style scoped>
.env-badge {
  font-size: 0.6rem;
  font-weight: 700;
  padding: 0.05rem 0.3rem;
  border-radius: 0.2rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
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

.user-menu-popover {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  min-width: 240px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: 0 6px 18px var(--modal-shadow);
  z-index: 60;
  padding: 0.25rem;
}

.user-menu-header {
  padding: 0.5rem 0.6rem;
}
.user-menu-email {
  font-family: var(--font-ui);
  font-size: 0.75rem;
  color: var(--ink);
  font-weight: 500;
  word-break: break-all;
}
.user-menu-sub {
  font-family: var(--font-ui);
  font-size: 0.7rem;
  color: var(--ink-muted);
  margin-top: 0.15rem;
}
.user-menu-tier {
  color: var(--accent);
  text-transform: capitalize;
}
.user-menu-divider {
  height: 1px;
  background: var(--border);
  margin: 0.25rem 0;
}
.user-menu-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  width: 100%;
  text-align: left;
  padding: 0.4rem 0.6rem;
  font-family: var(--font-ui);
  font-size: 0.75rem;
  color: var(--ink);
  background: transparent;
  border: none;
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.user-menu-item:hover {
  background: var(--surface-3);
}
.user-menu-item--danger {
  color: var(--danger);
}
.fleet-status-dot {
  position: absolute;
  top: 2px;
  left: 18px;
  width: 7px;
  height: 7px;
  border-radius: 9999px;
  background: var(--warn);
  border: 1px solid var(--surface-1);
}
.user-menu-degraded {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.6rem 0.4rem;
  font-family: var(--font-ui);
  font-size: 0.7rem;
  color: var(--warn);
}
.user-menu-link {
  background: transparent;
  border: none;
  padding: 0;
  font: inherit;
  color: var(--accent);
  text-decoration: underline;
  cursor: pointer;
}
.user-menu-item:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
