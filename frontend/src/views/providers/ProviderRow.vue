<script setup lang="ts">
/**
 * ProviderRow — single-row presentation of one configured Provider.
 *
 * Renders name / kind / model + a status pill driven by the most-recent
 * TestProvider outcome. Bundle entries are rendered with a "bundle" tag
 * and no remove affordance; personal entries expose a Remove button.
 *
 * Events:
 *   - `test`   — caller should run TestProvider against this row.
 *   - `remove` — caller should run RemoveProvider against this row.
 */

import { computed } from 'vue';
import type { Provider } from '@/lib/types';
import Button from '@/components/ui/Button.vue';

const props = defineProps<{
  provider: Provider;
  testing?: boolean;
}>();

const emit = defineEmits<{
  (e: 'test', id: string): void;
  (e: 'edit', provider: Provider): void;
  (e: 'remove', id: string): void;
}>();

const isPersonal = computed(() => props.provider.source === 'personal');

const statusLabel = computed(() =>
  props.provider.validated ? 'VALIDATED' : 'UNVALIDATED',
);

const statusClass = computed(() =>
  props.provider.validated ? 'text-signal-ok' : 'text-ink-dim',
);
</script>

<template>
  <tr
    class="border-b border-border-muted text-sm font-ui text-ink"
    :data-testid="`provider-row-${provider.id}`"
  >
    <td class="px-4 py-2.5">
      <div class="flex items-center gap-2">
        <span class="font-medium">{{ provider.name || provider.id }}</span>
        <span
          v-if="!isPersonal"
          class="rounded-sm border border-border-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-muted"
        >
          bundle
        </span>
      </div>
    </td>
    <td class="px-4 py-2.5 font-mono text-xs text-ink-muted">
      {{ provider.tier || '—' }}
    </td>
    <td class="px-4 py-2.5 font-mono text-xs text-ink-muted">
      {{ provider.model }}
    </td>
    <td class="px-4 py-2.5">
      <span
        class="inline-flex items-center gap-1.5 text-[11px] uppercase tracking-[0.18em] font-ui"
        :class="statusClass"
        role="status"
        :aria-label="`Status: ${statusLabel.toLowerCase()}`"
      >
        <span
          class="inline-block h-1.5 w-1.5 rounded-sm"
          :class="provider.validated ? 'bg-signal-ok' : 'bg-ink-dim'"
          aria-hidden="true"
        />
        {{ statusLabel }}
      </span>
    </td>
    <td class="px-4 py-2.5">
      <div class="flex items-center justify-end gap-2">
        <Button
          size="sm"
          variant="ghost"
          :disabled="testing"
          @click="emit('test', provider.id)"
        >
          {{ testing ? 'Testing…' : 'Test' }}
        </Button>
        <Button
          v-if="isPersonal"
          size="sm"
          variant="ghost"
          :data-testid="`edit-provider-${provider.id}`"
          @click="emit('edit', provider)"
        >
          Edit
        </Button>
        <Button
          v-if="isPersonal"
          size="sm"
          variant="ghost"
          @click="emit('remove', provider.id)"
        >
          Remove
        </Button>
      </div>
    </td>
  </tr>
</template>
