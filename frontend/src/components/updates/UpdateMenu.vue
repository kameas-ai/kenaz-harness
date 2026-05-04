<script setup lang="ts">
/**
 * UpdateMenu — compact popover anchored to the UpdateIndicator dot.
 *
 * Critical UX guardrails (mission auto-update WP04):
 *   - NOT a full modal. No scrim, no focus trap that hijacks the canvas.
 *   - Auto-closes on outside click + Escape.
 *   - Primary action is "Install & Restart" on every supported platform.
 *     v0.4.0 added a Windows helper updater (kenaz-updater.exe) that
 *     waits-then-relaunches outside the harness process, so Windows
 *     now matches the Mac/Linux UX.
 *   - "What's new" deep-links to GitHub Releases via Wails BrowserOpenURL
 *     (NEVER `window.open` — Wails strips that to nothing in production).
 *   - "Settings →" emits `navigate-settings` for the parent to route; the
 *     Update Settings panel is task #41 and may not exist yet, so we don't
 *     hardcode a route here.
 */

import {
  computed,
  getCurrentInstance,
  onBeforeUnmount,
  onMounted,
  ref,
  useTemplateRef,
  watch,
} from 'vue';
import { useRouter } from 'vue-router';
import { detectPlatform } from '@/lib/shortcuts/platform';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { useUpdateStore } from './useUpdateStore';

// Resolve the router only when the app actually installed one. In isolated
// unit tests we mount the menu without vue-router, and calling useRouter()
// in that context produces a noisy injection warning. Probing the current
// instance's app-level provides map keeps the menu silent in tests while
// still routing to /settings inside the live shell.
function resolveRouter(): ReturnType<typeof useRouter> | null {
  const inst = getCurrentInstance();
  const provides = inst?.appContext?.config?.globalProperties as
    | { $router?: unknown }
    | undefined;
  if (!provides?.$router) return null;
  return useRouter();
}
const routerOrNull = resolveRouter();

const props = defineProps<{
  /** Element the popover anchors to — used to compute the float position. */
  anchor?: HTMLElement | null;
}>();

const emit = defineEmits<{
  close: [];
  /** Fired when the user clicks "Settings →"; parent decides routing. */
  'navigate-settings': [];
}>();

const store = useUpdateStore();
const client = useHarnessClient();
const popoverRef = useTemplateRef<HTMLDivElement>('popoverRef');
const busy = ref(false);
const errMsg = ref<string | null>(null);

const platform = computed(() => detectPlatform());

const status = computed(() => store.status.value);
const version = computed(() => status.value?.availableVersion ?? '');
const notes = computed(() => status.value?.notes ?? '');
const releaseUrl = computed(() => status.value?.releaseUrl ?? '');

const downloadState = computed(() => status.value?.downloadState ?? 'idle');
const isDownloading = computed(() => downloadState.value === 'downloading');
const isStaged = computed(() => downloadState.value === 'staged');
const isFailed = computed(() => downloadState.value === 'failed');

const downloadPct = computed(() => {
  const p = status.value?.downloadProgress;
  if (typeof p !== 'number') return 0;
  return Math.max(0, Math.min(100, Math.round(p * 100)));
});

const primaryLabel = computed(() => {
  if (busy.value) return 'Working…';
  if (isFailed.value) return 'Retry';
  // Every platform now performs a true restart: Mac/Linux do it inline,
  // Windows hands off to the bundled kenaz-updater.exe helper which
  // waits-then-relaunches the new binary. The deferred-pending-marker
  // path still exists as a fallback if the helper isn't bundled, but
  // that path is invisible to the user — the CTA stays consistent.
  // platform is read here only so future per-OS divergence (e.g. an
  // elevated-install confirmation modal on Windows) has a hook.
  void platform.value;
  return 'Install & Restart';
});

async function onPrimary() {
  if (busy.value) return;
  busy.value = true;
  errMsg.value = null;
  try {
    if (!isStaged.value) {
      // Kick the download if it hasn't started yet — the backend coalesces
      // duplicate calls so this is safe even if a background download is
      // already in flight.
      await store.download();
    }
    await store.apply();
    emit('close');
  } catch (e) {
    errMsg.value = (e as Error)?.message ?? 'Update failed.';
  } finally {
    busy.value = false;
  }
}

async function onSkip() {
  if (!version.value) return;
  await store.skipVersion(version.value);
  emit('close');
}

function onLater() {
  emit('close');
}

function onSettings() {
  emit('navigate-settings');
  emit('close');
  // Convenience: also push to /settings if a router is installed. The
  // dedicated Update Settings panel is task #41 and isn't routed yet, so we
  // land on the existing Settings root for now.
  if (routerOrNull) {
    void routerOrNull.push('/settings');
  }
}

function onWhatsNew() {
  if (!releaseUrl.value) return;
  try {
    client.openExternalURL(releaseUrl.value);
  } catch {
    // openExternalURL is unavailable outside Wails; absorb so tests don't
    // fail when the runtime is stubbed.
  }
}

function onClickOutside(e: MouseEvent) {
  const pop = popoverRef.value;
  const anchor = props.anchor;
  const target = e.target as Node | null;
  if (!target) return;
  if (pop && pop.contains(target)) return;
  if (anchor && anchor.contains(target)) return;
  emit('close');
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation();
    emit('close');
  }
}

onMounted(() => {
  if (typeof window !== 'undefined') {
    window.addEventListener('mousedown', onClickOutside, true);
    window.addEventListener('keydown', onKeydown);
  }
});

onBeforeUnmount(() => {
  if (typeof window !== 'undefined') {
    window.removeEventListener('mousedown', onClickOutside, true);
    window.removeEventListener('keydown', onKeydown);
  }
});

// If the indicator state changes such that no update is offered any more
// (e.g. user just skipped), close ourselves.
watch(
  () => store.visible.value,
  (v) => {
    if (!v) emit('close');
  },
);
</script>

<template>
  <div
    ref="popoverRef"
    role="dialog"
    aria-label="Update menu"
    class="absolute right-0 top-full z-50 mt-2 w-72 rounded-md border border-border bg-surface-1 p-3 shadow-lg"
    data-testid="update-menu-popover"
  >
    <div class="flex items-baseline justify-between gap-2">
      <div
        class="font-ui text-sm font-semibold text-ink"
        data-testid="update-menu-headline"
      >
        Kenaz {{ version }} available
      </div>
      <a
        v-if="releaseUrl"
        href="#"
        class="font-ui text-[11px] text-ink-link text-accent hover:underline"
        data-testid="update-menu-whats-new"
        @click.prevent="onWhatsNew"
      >
        What's new
      </a>
    </div>

    <p
      v-if="notes"
      class="mt-2 font-ui text-xs leading-relaxed text-ink-muted"
      data-testid="update-menu-notes"
    >
      {{ notes }}
    </p>

    <div
      v-if="isDownloading"
      class="mt-3"
      data-testid="update-menu-progress"
      aria-label="Download progress"
    >
      <div class="h-1 w-full overflow-hidden rounded-sm bg-surface-3">
        <div
          class="h-full bg-accent transition-fast ease-kenaz"
          :style="{ width: `${downloadPct}%` }"
          data-testid="update-menu-progress-fill"
        />
      </div>
      <div class="mt-1 font-mono text-[11px] text-ink-subtle">
        Downloading… {{ downloadPct }}%
      </div>
    </div>

    <div
      v-else-if="isStaged"
      class="mt-3 font-ui text-[11px] text-signal-ok"
      data-testid="update-menu-staged"
    >
      Update downloaded — ready to apply.
    </div>

    <div
      v-else-if="isFailed"
      class="mt-3 font-ui text-[11px] text-signal-danger"
      data-testid="update-menu-failed"
    >
      Last attempt failed. Retry to try again.
    </div>

    <div
      v-if="errMsg"
      class="mt-2 font-ui text-[11px] text-signal-danger"
      data-testid="update-menu-error"
    >
      {{ errMsg }}
    </div>

    <div class="mt-3 flex items-center justify-end gap-2">
      <button
        type="button"
        class="font-ui text-xs text-ink-muted hover:text-ink"
        data-testid="update-menu-skip"
        @click="onSkip"
      >
        Skip this version
      </button>
      <button
        type="button"
        class="font-ui text-xs text-ink-muted hover:text-ink"
        data-testid="update-menu-later"
        @click="onLater"
      >
        Later
      </button>
      <button
        type="button"
        class="rounded-sm border border-accent bg-accent px-2.5 py-1 font-ui text-xs font-medium text-surface-0 hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60 transition-fast ease-kenaz"
        :disabled="busy"
        data-testid="update-menu-primary"
        @click="onPrimary"
      >
        {{ primaryLabel }}
      </button>
    </div>

    <div class="mt-2 border-t border-border-muted pt-2 text-right">
      <button
        type="button"
        class="font-ui text-[11px] text-ink-subtle hover:text-ink"
        data-testid="update-menu-settings"
        @click="onSettings"
      >
        Settings →
      </button>
    </div>
  </div>
</template>
