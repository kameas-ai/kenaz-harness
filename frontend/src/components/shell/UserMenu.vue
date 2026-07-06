<script setup lang="ts">
/**
 * UserMenu — account status pill + fleet-identity popover.
 *
 * v0.20.0: non-account rows (Search, Command Palette, Theme, Update Available)
 * have moved to the OS native menu bar. This component is now a pure
 * identity/account surface:
 *
 *   Trigger (always rendered unless fleet is fully disabled):
 *     - signed-in:  avatar (initials) + optional env badge
 *     - signed-out: generic user silhouette icon
 *     - No update-dot overlay (that signal now lives in Help → Check for Updates)
 *
 *   Popover (signed-in):
 *     identity header (email, org, tier) → divider → Account settings → Sign out
 *
 *   Popover (signed-out, fleet enabled):
 *     Sign in → calls fleetSignIn() directly (unchanged from v0.19.0)
 *
 *   Fleet disabled (HARNESS_FLEET_DISABLED=1): render nothing.
 *
 * OSS-first contract preserved: fleet rows do not render when disabled.
 */
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { runAsyncAction } from '@/composables/useAsyncAction';
import type { FleetIdentity, FleetProfileInfo } from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();

const identity = ref<FleetIdentity | null | false>(null);
const profile = ref<FleetProfileInfo | null>(null);
const fleetDisabled = ref(false);
const menuOpen = ref(false);
const loading = ref(false);
/** Inline error state for sign-out failure — visible near the sign-out button. */
const signOutError = ref<string | null>(null);

let pollTimer: number | null = null;

onMounted(() => {
  void refresh();
  pollTimer = window.setInterval(() => {
    void refresh();
  }, 15000);
  document.addEventListener('click', onDocumentClick);
});

onBeforeUnmount(() => {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
  document.removeEventListener('click', onDocumentClick);
});

async function refresh() {
  try {
    profile.value = await client.settings.fleetProfile();
    const signedIn = await client.settings.fleetSignedIn();
    if (signedIn) {
      try {
        identity.value = await client.settings.fleetRefreshIdentity();
      } catch {
        identity.value = false;
      }
    } else {
      identity.value = false;
    }
  } catch (e: any) {
    const msg: string = e?.message ?? String(e);
    if (msg.includes('disabled by env')) {
      fleetDisabled.value = true;
      identity.value = false;
    } else {
      identity.value = false;
    }
  }
}

const isSignedIn = computed(() => !!identity.value);

const initials = computed(() => {
  const id = identity.value;
  if (!id) return '';
  const source = id.displayName?.trim() || id.email?.trim() || '';
  const parts = source.split(/[\s@]+/).filter(Boolean);
  if (parts.length === 0) return '';
  const first = parts[0]?.[0] ?? '';
  const second = parts[1]?.[0] ?? '';
  return (first + second).toUpperCase().slice(0, 2);
});

const badgeColor = computed(() => profile.value?.badgeColor ?? '');
const envName = computed(() => (profile.value?.name ?? '').toUpperCase());

const emailDisplay = computed(() => {
  const id = identity.value;
  if (!id) return '';
  return id.email || id.displayName || '';
});
const orgDisplay = computed(() => {
  const id = identity.value;
  if (!id) return '';
  return id.orgName || '';
});
const tierLabel = computed(() => {
  const id = identity.value;
  if (!id) return '';
  return id.tier || '';
});

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
async function handleSignIn() {
  // Click → kick off the PKCE flow directly. Browser opens, user
  // authenticates, then returns. On failure we route to /settings?tab=account
  // where AccountPanel surfaces the error in full.
  close();
  loading.value = true;
  try {
    identity.value = await client.settings.fleetSignIn();
  } catch {
    // Send the user to the full panel so they can see the error and retry.
    void router.push('/settings?tab=account');
  } finally {
    loading.value = false;
  }
}
async function handleSignOut() {
  close();
  loading.value = true;
  signOutError.value = null;
  // (FR-003) Surface sign-out failures instead of silently ignoring them.
  const result = await runAsyncAction({
    action:     () => client.settings.fleetSignOut(),
    errorLabel: 'Sign out',
    toastMessage: (err) => {
      const msg = err instanceof Error ? err.message : String(err);
      return `Sign out failed: ${msg}. You may still be signed in.`;
    },
  });
  if (!result.error) {
    identity.value = false;
  } else {
    signOutError.value = result.err instanceof Error
      ? result.err.message
      : String(result.err);
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
        <!-- Identity header (signed-in only) -->
        <template v-if="isSignedIn">
          <div class="user-menu-header">
            <div class="user-menu-email">{{ emailDisplay }}</div>
            <div v-if="orgDisplay" class="user-menu-sub">{{ orgDisplay }}</div>
            <div v-if="tierLabel" class="user-menu-sub user-menu-tier">{{ tierLabel }}</div>
          </div>
          <div class="user-menu-divider" />
        </template>

        <!-- Fleet account rows -->
        <button
          v-if="!isSignedIn"
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
          v-if="isSignedIn"
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
.user-menu-item:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
