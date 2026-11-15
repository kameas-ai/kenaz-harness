<script setup lang="ts">
/**
 * PolicyDefaultNotice — launch spec L09 R7: on first run, say which policy
 * default is active and where to change it. On an install that predates
 * the starter policy (R6) it offers the starter policy instead; nothing
 * changes until the person chooses.
 *
 * Desktop only: served (workbench) mode has no host policy default.
 * Mounted once in Shell.vue next to the other banners.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { isServedMode } from '@/lib/useServedMode';
import type { PolicyDefaultView } from '@/lib/types';
import { LEGACY_SUMMARY, POLICY_DEFAULT_WHERE, STARTER_SUMMARY } from './policyDefaultCopy';

const client = useHarnessClient();
const view = ref<PolicyDefaultView | null>(null);
const busy = ref(false);
const error = ref('');

onMounted(async () => {
  if (isServedMode() || !client?.settings?.getPolicyDefault) return;
  try {
    const v = await client.settings.getPolicyDefault();
    if (!v.noticeSeen && (v.mode === 'starter' || v.mode === 'legacy')) {
      view.value = v;
    }
  } catch {
    // No notice rather than a wrong one.
  }
});

async function dismiss() {
  view.value = null;
  try {
    await client?.settings?.dismissPolicyDefaultNotice?.();
  } catch {
    // Shown again next launch; harmless.
  }
}

async function choose(mode: 'starter' | 'legacy') {
  if (!client?.settings?.setPolicyDefault) return;
  busy.value = true;
  error.value = '';
  try {
    await client.settings.setPolicyDefault(mode);
    view.value = null;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div
    v-if="view"
    class="px-4 py-2 bg-surface-2 border-b border-border-muted flex items-start justify-between gap-3"
    role="status"
    aria-live="polite"
    data-testid="policy-default-notice"
  >
    <div class="flex flex-col gap-1 min-w-0">
      <template v-if="view.upgraded">
        <span class="font-ui text-sm font-medium text-ink" data-testid="policy-default-notice-title">
          A starter policy is available
        </span>
        <span class="font-ui text-xs text-ink-muted">
          Now: {{ LEGACY_SUMMARY }} With the starter policy: {{ STARTER_SUMMARY }}
          Nothing changes until you choose. You can switch later in
          <span class="font-mono">{{ POLICY_DEFAULT_WHERE }}</span>.
        </span>
      </template>
      <template v-else-if="view.mode === 'starter'">
        <span class="font-ui text-sm font-medium text-ink" data-testid="policy-default-notice-title">
          The starter policy is on
        </span>
        <span class="font-ui text-xs text-ink-muted">
          {{ STARTER_SUMMARY }} Change it in
          <span class="font-mono">{{ POLICY_DEFAULT_WHERE }}</span>.
        </span>
      </template>
      <template v-else>
        <span class="font-ui text-sm font-medium text-ink" data-testid="policy-default-notice-title">
          Every file access asks first
        </span>
        <span class="font-ui text-xs text-ink-muted">
          {{ LEGACY_SUMMARY }} Change it in
          <span class="font-mono">{{ POLICY_DEFAULT_WHERE }}</span>.
        </span>
      </template>
      <span v-if="error" class="font-ui text-xs text-signal-danger" role="alert">{{ error }}</span>
    </div>
    <div class="flex items-center gap-2 flex-shrink-0">
      <template v-if="view.upgraded">
        <button
          type="button"
          class="font-ui text-xs text-ink px-2 py-1 rounded-sm border border-border-muted hover:bg-surface-1"
          :disabled="busy"
          data-testid="policy-default-notice-accept"
          @click="choose('starter')"
        >
          Use the starter policy
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm"
          :disabled="busy"
          data-testid="policy-default-notice-keep"
          @click="choose('legacy')"
        >
          Keep current behaviour
        </button>
      </template>
      <button
        v-else
        type="button"
        class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm"
        data-testid="policy-default-notice-dismiss"
        @click="dismiss"
      >
        Got it
      </button>
    </div>
  </div>
</template>
