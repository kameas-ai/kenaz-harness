<script setup lang="ts">
import { computed } from 'vue';
import Titlebar from './Titlebar.vue';
import Toolbar from './Toolbar.vue';
import LeftRail from './LeftRail.vue';
import LegendBar from './LegendBar.vue';
import ConnectionLostBanner from '@/components/ui/ConnectionLostBanner.vue';
import { useConnectionState } from '@/lib/useConnectionState';

/**
 * Shell — the persistent app-level layout (plan §2.1).
 *
 * 3-region grid: Titlebar / [LeftRail | main]. The main column hosts
 * Toolbar + <router-view> wrapped in <KeepAlive> for per-session UI
 * state (FR-002) + LegendBar.
 *
 * The first-paint state machine (plan §4.1, FR-017) renders a quiet
 * "starting…" surface while connecting. ConnectionLostBanner appears
 * only when the bridge is lost (FR-013) — not as a toast wall.
 */
const connection = useConnectionState();
const isStarting = computed(() => connection.value === 'connecting');
const isLost = computed(() => connection.value === 'lost');
</script>

<template>
  <div class="shell-grid bg-surface-0 text-ink font-ui">
    <header class="shell-titlebar border-b border-border-muted bg-surface-1">
      <Titlebar />
    </header>

    <aside
      class="shell-rail border-r border-border-muted bg-surface-1"
      aria-label="Primary navigation"
    >
      <LeftRail />
    </aside>

    <main class="shell-main bg-surface-0">
      <div class="shell-toolbar border-b border-border-muted bg-surface-1">
        <Toolbar />
      </div>

      <div class="shell-canvas" role="region" aria-label="Active surface">
        <ConnectionLostBanner v-if="isLost" />
        <div
          v-if="isStarting"
          class="grid place-items-center h-full text-ink-subtle font-mono text-sm"
        >
          starting…
        </div>
        <router-view v-else v-slot="{ Component }">
          <KeepAlive>
            <component :is="Component" />
          </KeepAlive>
        </router-view>
      </div>

      <div class="shell-legend border-t border-border-muted bg-surface-1">
        <LegendBar />
      </div>
    </main>
  </div>
</template>
