<script setup lang="ts">
/**
 * PermissionsView — top-level page hosting the four family permission
 * panels (Bash, Filesystem, Credentials, Tools) under a tab strip.
 *
 * The route is `/permissions/:family?` so deep links can target a
 * specific family (e.g. `/permissions/fs`). When :family is missing or
 * unknown the view defaults to "bash".
 */

import { computed, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import PermissionDialsPanel from '@/components/settings/PermissionDialsPanel.vue';
import BashPermissionsPanel from '@/views/settings/BashPermissionsPanel.vue';
import FilesystemPermissionsPanel from '@/views/settings/FilesystemPermissionsPanel.vue';
import CredentialPermissionsPanel from '@/views/settings/CredentialPermissionsPanel.vue';
import ToolPermissionsPanel from '@/views/settings/ToolPermissionsPanel.vue';

type Family = 'bash' | 'fs' | 'credential' | 'tool';

const TABS: ReadonlyArray<{ id: Family; label: string; icon: string }> = [
  { id: 'bash', label: 'Bash', icon: '⚡' },
  { id: 'fs', label: 'Filesystem', icon: '📁' },
  { id: 'credential', label: 'Credentials', icon: '🔑' },
  { id: 'tool', label: 'Tools', icon: '🔧' },
];

const route = useRoute();
const router = useRouter();

function isFamily(s: string): s is Family {
  return s === 'bash' || s === 'fs' || s === 'credential' || s === 'tool';
}

const activeFamily = computed<Family>(() => {
  const raw = route.params.family;
  const v = typeof raw === 'string' ? raw : '';
  return isFamily(v) ? v : 'bash';
});

function selectTab(f: Family) {
  if (f === activeFamily.value) return;
  void router.replace(`/permissions/${f}`);
}

// Normalise a missing or unknown :family slug to the canonical /bash
// path so the URL always reflects the rendered tab. Guard the redirect
// to ONLY fire while we're still on the permissions route — without
// this guard, navigating away (e.g. to /settings) would clear
// route.params.family and the watch would shove the user right back
// into /permissions/bash.
watch(
  () => [route.name, route.params.family] as const,
  ([name, raw]) => {
    if (name !== 'permissions') return;
    const v = typeof raw === 'string' ? raw : '';
    if (!isFamily(v)) {
      void router.replace('/permissions/bash');
    }
  },
  { immediate: true },
);
</script>

<template>
  <div data-testid="permissions-view">
    <CanvasHead
      number="06"
      section="SETTINGS"
      title="Permissions"
      subtitle="Saved “Allow always” grants for bash, filesystem, credentials, and tools. Revoke a row to re-prompt the next time the harness sees that resource."
    />
    <SettingsTabs />

    <div class="px-6 pt-4 pb-2 border-b border-border-muted">
      <PermissionDialsPanel />
    </div>

    <div class="px-6 pt-4">
      <div
        class="flex items-center gap-1 border-b border-border-muted"
        role="tablist"
        aria-label="Permission families"
        data-testid="permissions-tablist"
      >
        <button
          v-for="tab in TABS"
          :key="tab.id"
          type="button"
          role="tab"
          :aria-selected="activeFamily === tab.id"
          :class="[
            'px-3 py-2 -mb-px font-ui text-[11px] uppercase tracking-[0.18em] border-b-2 transition-colors',
            activeFamily === tab.id
              ? 'border-accent text-accent'
              : 'border-transparent text-ink-muted hover:text-ink',
          ]"
          :data-testid="`permissions-tab-${tab.id}`"
          @click="selectTab(tab.id)"
        >
          <span aria-hidden="true">{{ tab.icon }}</span>
          {{ tab.label }}
        </button>
      </div>
      <div class="mt-4" role="tabpanel">
        <BashPermissionsPanel v-if="activeFamily === 'bash'" />
        <FilesystemPermissionsPanel v-else-if="activeFamily === 'fs'" />
        <CredentialPermissionsPanel v-else-if="activeFamily === 'credential'" />
        <ToolPermissionsPanel v-else-if="activeFamily === 'tool'" />
      </div>
    </div>
  </div>
</template>
