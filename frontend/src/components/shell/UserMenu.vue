<script setup lang="ts">
/**
 * UserMenu — the unified top-right dropdown.
 *
 * Replaces the four separate top-bar buttons (search, command palette,
 * theme toggle, update indicator) plus the fleet identity slot with a
 * single dropdown trigger. The trigger renders:
 *
 *   - signed-in           → avatar (initials) + optional env badge
 *   - signed-out          → generic menu icon
 *   - fleet disabled      → generic menu icon (no fleet items in menu)
 *
 * An update-available dot overlays the trigger when the auto-update
 * store has a pending update (so users still notice updates without
 * opening the menu).
 *
 * Menu rows (in order):
 *   1. Identity header (signed-in only)
 *   2. Search…              (calls useSearchPalette().open())
 *   3. Command palette…     (calls useCommandPalette().open())
 *   4. Theme: <current>     (calls useTheme().cycle())
 *   5. Update available     (visible only when store reports one)
 *   6. Account settings     (routes to /settings?tab=account; fleet only)
 *   7. Sign in              (signed-out, fleet only)
 *   8. Sign out             (signed-in, fleet only)
 *
 * The OSS-first contract is preserved: fleet rows (1, 6, 7, 8) do not
 * render when HARNESS_FLEET_DISABLED=1; only the non-fleet rows remain.
 */
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useTheme } from '@/lib/useTheme';
import { useSearchPalette } from '@/lib/useSearchPalette';
import { useCommandPalette } from '@/lib/useCommandPalette';
import { useUpdateStore } from '@/components/updates/useUpdateStore';
import type { FleetIdentity, FleetProfileInfo } from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();
const theme = useTheme();
const searchPalette = useSearchPalette();
const cmdPalette = useCommandPalette();
const updateStore = useUpdateStore();

const identity = ref<FleetIdentity | null | false>(null);
const profile = ref<FleetProfileInfo | null>(null);
const fleetDisabled = ref(false);
const menuOpen = ref(false);
const loading = ref(false);

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

const isSignedIn = computed(() => !!identity.value && identity.value !== false);
const fleetEnabled = computed(() => !fleetDisabled.value);

const initials = computed(() => {
  const id = identity.value;
  if (!id || id === false) return '';
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
  if (!id || id === false) return '';
  return id.email || id.displayName || '';
});
const orgDisplay = computed(() => {
  const id = identity.value;
  if (!id || id === false) return '';
  return id.orgName || '';
});
const tierLabel = computed(() => {
  const id = identity.value;
  if (!id || id === false) return '';
  return id.tier || '';
});

const updateAvailable = computed(() => {
  const s = updateStore.status.value;
  return !!(s && s.availableVersion);
});

const themeLabel = computed(() => {
  const t = theme.theme.value;
  if (t === 'dark') return 'Theme: Dark';
  if (t === 'light') return 'Theme: Light';
  return 'Theme: System';
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

function runSearch() {
  close();
  searchPalette.open();
}
function runCommandPalette() {
  close();
  cmdPalette.open();
}
function cycleTheme() {
  // Don't close — let the user cycle multiple times if they're picking.
  theme.cycle();
}
function openUpdate() {
  close();
  // The UpdateMenu reads from the same store; opening it surfaces the
  // detailed action panel. For now route to settings → updates, which
  // hosts the same controls; the legacy in-titlebar UpdateIndicator
  // popover is gone with this consolidation.
  void router.push('/settings?tab=updates');
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
  try {
    await client.settings.fleetSignOut();
    identity.value = false;
  } catch {
    // Ignored — next poll refreshes state.
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="relative" data-user-menu data-testid="user-menu">
    <button
      type="button"
      class="relative flex items-center gap-1.5 rounded-sm px-1.5 py-1 hover:bg-surface-2 transition-fast ease-kenaz"
      :aria-label="isSignedIn ? `Menu (${emailDisplay})` : 'Menu'"
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
      <!-- Update-available indicator overlay -->
      <span
        v-if="updateAvailable"
        class="update-dot"
        aria-label="Update available"
        title="Update available"
      />
    </button>

    <div
      v-if="menuOpen"
      role="menu"
      class="user-menu-popover"
      data-testid="user-menu-popover"
    >
      <!-- Identity header (fleet + signed-in only) -->
      <template v-if="fleetEnabled && isSignedIn">
        <div class="user-menu-header">
          <div class="user-menu-email">{{ emailDisplay }}</div>
          <div v-if="orgDisplay" class="user-menu-sub">{{ orgDisplay }}</div>
          <div v-if="tierLabel" class="user-menu-sub user-menu-tier">{{ tierLabel }}</div>
        </div>
        <div class="user-menu-divider" />
      </template>

      <button
        type="button"
        role="menuitem"
        class="user-menu-item"
        data-testid="menu-search"
        @click="runSearch"
      >
        <span>Search…</span>
        <span class="user-menu-shortcut">⌘F</span>
      </button>
      <button
        type="button"
        role="menuitem"
        class="user-menu-item"
        data-testid="menu-command-palette"
        @click="runCommandPalette"
      >
        <span>Command palette…</span>
        <span class="user-menu-shortcut">⌘K</span>
      </button>
      <button
        type="button"
        role="menuitem"
        class="user-menu-item"
        data-testid="menu-theme"
        @click="cycleTheme"
      >
        <span>{{ themeLabel }}</span>
      </button>

      <template v-if="updateAvailable">
        <div class="user-menu-divider" />
        <button
          type="button"
          role="menuitem"
          class="user-menu-item user-menu-item--accent"
          data-testid="menu-update"
          @click="openUpdate"
        >
          <span>Update available</span>
          <span class="update-dot update-dot--inline" aria-hidden="true" />
        </button>
      </template>

      <!-- Fleet account rows -->
      <template v-if="fleetEnabled">
        <div class="user-menu-divider" />
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
      </template>
    </div>
  </div>
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

.update-dot {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--ok);
  box-shadow: 0 0 0 2px var(--surface-0);
}
.update-dot--inline {
  position: static;
  box-shadow: none;
  display: inline-block;
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
.user-menu-item--accent {
  color: var(--accent);
}
.user-menu-item--danger {
  color: var(--danger);
}
.user-menu-item:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.user-menu-shortcut {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  color: var(--ink-muted);
  letter-spacing: 0.05em;
}
</style>
