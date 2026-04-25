<script setup lang="ts">
/**
 * SettingsView — app preferences (NN/SECTION pattern).
 *
 * Theme selector (system / light / dark — currently dark only per
 * Kenaz, structured for v1.x), lastRoute toggle, read-only data-dir
 * + harness build info from AppInfo.
 *
 * Persistence routes through the SettingsStore (Settings_Get +
 * Settings_Set RPC); writes are debounced 250ms via lib/settings.ts
 * to coalesce rapid toggles into a single disk write.
 */
import { onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import type { AppInfo, Settings, Theme } from '@/lib/types';

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
});
const appInfo = ref<AppInfo | null>(null);
const restoreOnLaunch = ref(true);

const themes: ReadonlyArray<{ value: Theme; label: string; note?: string }> = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark', note: 'v1 default' },
  { value: 'light', label: 'Light', note: 'v1.x' },
];

async function refresh() {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
}

function setTheme(t: Theme) {
  settings.value = { ...settings.value, theme: t };
  void client.settings.saveTheme(t).catch(() => {});
}

function toggleRestore() {
  restoreOnLaunch.value = !restoreOnLaunch.value;
  // When restore is off the chassis still records lastRoute for audit
  // but App.vue's restoreLastRoute respects this preference. We
  // persist the selection alongside the rest of the settings.
  debouncedSave(client, {
    ...settings.value,
    lastRoute: restoreOnLaunch.value ? settings.value.lastRoute : '/sessions',
  });
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="06"
      section="SETTINGS"
      title="App preferences"
      subtitle="Theme, route restoration, and data-dir info. Settings persist to a single JSON file under your user config dir."
    />

    <div class="px-6 py-4 grid gap-6 max-w-3xl" data-testid="settings-form">
      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Theme
        </h2>
        <div class="mt-2 inline-flex rounded-sm border border-border" role="radiogroup">
          <button
            v-for="t in themes"
            :key="t.value"
            type="button"
            role="radio"
            :aria-checked="settings.theme === t.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="settings.theme === t.value
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            @click="setTheme(t.value)"
          >
            {{ t.label }}
            <span v-if="t.note" class="ml-1 text-[10px] text-ink-subtle">({{ t.note }})</span>
          </button>
        </div>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Route restoration
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="restoreOnLaunch"
            data-testid="restore-toggle"
            @change="toggleRestore"
          />
          Restore the last visited route on launch
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          Last route: <span class="font-mono">{{ settings.lastRoute }}</span>
        </p>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Storage
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Schema version</dt>
          <dd class="font-mono text-ink">{{ settings.schemaVersion }}</dd>
          <dt class="text-ink-muted">Window size</dt>
          <dd class="font-mono text-ink">
            {{ settings.windowSize.width }} × {{ settings.windowSize.height }}
          </dd>
        </dl>
      </section>

      <section v-if="appInfo">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Harness build
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Build</dt>
          <dd class="font-mono text-ink">{{ appInfo.build }}</dd>
          <dt class="text-ink-muted">Commit</dt>
          <dd class="font-mono text-ink">{{ appInfo.commit }}</dd>
          <dt class="text-ink-muted">Built</dt>
          <dd class="font-mono text-ink">{{ appInfo.buildTime || '—' }}</dd>
          <dt class="text-ink-muted">Go</dt>
          <dd class="font-mono text-ink">{{ appInfo.goVersion || '—' }}</dd>
          <dt class="text-ink-muted">Platform</dt>
          <dd class="font-mono text-ink">{{ appInfo.platform || '—' }}</dd>
        </dl>
      </section>
    </div>
  </div>
</template>
