<script setup lang="ts">
/**
 * SkillDetail — the skill detail plugin of the "Add capability" surface
 * (install-framework-01DOGF0B WP05). A fleet catalog skill installs through
 * Capability_Install → the install framework: its payload is fetched once,
 * checked by the single signature verifier, and live-registered with the
 * slash registry (no restart). Installed state is the slash registry's, so
 * the badge here and in the Marketplace agree by construction (P-1/P-3).
 *
 * Removal is the surface's generic Remove (Capability_Uninstall); an
 * org-required skill is read-only there.
 */
import { ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { CapabilityItem } from '@/lib/types';
import { SOURCE_LABELS } from './labels';

const props = defineProps<{ item: CapabilityItem }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const client = useHarnessClient();
const installing = ref(false);
const error = ref<string | null>(null);

async function install() {
  installing.value = true;
  error.value = null;
  try {
    await client.capabilities.install('skill', props.item.id, props.item.version ?? '');
    emit('changed');
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    installing.value = false;
  }
}
</script>

<template>
  <section class="space-y-3" :data-testid="`skill-detail-${item.id}`">
    <header>
      <h3 class="font-ui text-[13px] font-semibold text-ink">/{{ item.name }}</h3>
      <p class="font-ui text-[11px] text-ink-muted">
        Skill · {{ SOURCE_LABELS[item.source] ?? item.source }}<span v-if="item.version"> · {{ item.version }}</span>
      </p>
      <p v-if="item.description" class="mt-2 font-ui text-[12px] text-ink-muted">{{ item.description }}</p>
    </header>

    <p class="font-ui text-[12px]" data-testid="skill-detail-state">
      <span v-if="item.state.installed" class="text-signal-ok">
        Installed{{ item.state.version ? ` (${item.state.version})` : '' }} — available as a slash command now.
      </span>
      <span v-else-if="item.state.detail" class="text-signal-warn">{{ item.state.detail }}</span>
      <span v-else class="text-ink-muted">Not installed.</span>
      <span v-if="item.state.detail === 'disabled'" class="text-ink-muted"> Disabled in Settings › Skills.</span>
    </p>
    <p v-if="item.state.update_available" class="font-ui text-[12px] text-accent" data-testid="skill-detail-update">
      A newer version ({{ item.version }}) is in the catalog — use Update in the list.
    </p>

    <p class="font-ui text-[11px] text-ink-muted" data-testid="skill-detail-unverified">
      Fleet catalog installs are not signature-verified yet — content comes from your org's catalog but is
      not cryptographically checked before install.
    </p>

    <div v-if="error" class="font-ui text-[12px] text-signal-danger" role="alert" data-testid="skill-detail-error">
      {{ error }}
    </div>
    <button
      v-if="!item.state.installed && !item.read_only"
      type="button"
      class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
      :disabled="installing"
      :data-testid="`skill-detail-install-${item.id}`"
      @click="install"
    >
      {{ installing ? 'Installing…' : 'Install' }}
    </button>
  </section>
</template>
