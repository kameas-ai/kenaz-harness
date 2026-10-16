<script setup lang="ts">
/**
 * ProviderRow — single-row presentation of one configured Provider.
 *
 * Renders name / kind / model + a status pill driven by the most-recent
 * TestProvider outcome. Personal entries expose Edit/Remove; bundle and
 * host entries are read-only and carry a tag naming where they came from.
 *
 * Every row that holds a key also carries a key-source badge ("Your key",
 * "Provided by <org>", "Team") and a read-only scope label, so a user with
 * several keys for one provider can tell whose each one is
 * (provider-credentials-client-01PCCLI01 WP04, §7a H2). A profile bound to
 * a key Fleet delivers is not editable here: the form edits local keys only.
 *
 * `readOnly` additionally hides the Test affordance. Served mode passes it:
 * TestProvider is not ported to the HTTP transport, so the button would
 * reject on click — an affordance that cannot work is worse than none.
 *
 * Events:
 *   - `test`   — caller should run TestProvider against this row.
 *   - `remove` — caller should run RemoveProvider against this row.
 */

import { computed } from 'vue';
import type { CredentialKey, CredentialSource, Provider } from '@/lib/types';
import Button from '@/components/ui/Button.vue';

const props = defineProps<{
  provider: Provider;
  testing?: boolean;
  /** Hide every mutating affordance, including Test. */
  readOnly?: boolean;
  /** The key this profile uses, from LLM_ListCredentialKeys (for its scope name). */
  credentialKey?: CredentialKey;
}>();

const emit = defineEmits<{
  (e: 'test', id: string): void;
  (e: 'edit', provider: Provider): void;
  (e: 'remove', id: string): void;
}>();

const isPersonal = computed(
  () => props.provider.source === 'personal' && !props.readOnly,
);

/** Whose key this row uses. Absent cred means the profile holds no key. */
const keySource = computed<CredentialSource | null>(() => {
  const cred = props.provider.cred;
  if (!cred || (!cred.kind && !cred.credentialId)) return null;
  return cred.source ?? 'local';
});

const isFleetKey = computed(
  () => keySource.value === 'org' || keySource.value === 'team' || keySource.value === 'user_sync',
);

/** Only a key entered on this device can be edited from the form. */
const canEdit = computed(
  () => isPersonal.value && (keySource.value === null || keySource.value === 'local'),
);

const keyBadge = computed(() => {
  switch (keySource.value) {
    case 'org': {
      const org = props.credentialKey?.scopeName;
      return org ? `Provided by ${org}` : 'Provided by your organization';
    }
    case 'team':
      return 'Team';
    case 'local':
    case 'user_sync':
      return 'Your key';
    default:
      return null;
  }
});

const keyScope = computed(() => {
  switch (keySource.value) {
    case 'org':
      return 'Organization';
    case 'team':
      return props.credentialKey?.scopeName ? `Team: ${props.credentialKey.scopeName}` : 'Team';
    case 'local':
    case 'user_sync':
      return 'Personal';
    default:
      return null;
  }
});

/**
 * Tag shown beside the name for providers the user cannot edit here.
 * A host provider must NOT be mislabelled "bundle" — the two have very
 * different remediation paths (re-sign a bundle vs. edit the Kenaz profile).
 */
const sourceTag = computed(() => {
  switch (props.provider.source) {
    case 'host':
      return 'host';
    case 'personal':
      return null;
    default:
      return 'bundle';
  }
});

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
          v-if="sourceTag"
          class="rounded-sm border border-border-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-muted"
          :data-testid="`provider-source-tag-${provider.id}`"
        >
          {{ sourceTag }}
        </span>
        <span
          v-if="keyBadge"
          class="rounded-sm border px-1.5 py-0.5 text-[10px] tracking-wider"
          :class="isFleetKey ? 'border-accent-hairline text-ink' : 'border-border-muted text-ink-muted'"
          :data-testid="`provider-key-badge-${provider.id}`"
        >
          {{ keyBadge }}
        </span>
      </div>
      <div
        v-if="keyScope"
        class="mt-0.5 text-[11px] text-ink-subtle"
        :data-testid="`provider-key-scope-${provider.id}`"
      >
        Scope: {{ keyScope }}
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
          v-if="!readOnly"
          size="sm"
          variant="ghost"
          :disabled="testing"
          @click="emit('test', provider.id)"
        >
          {{ testing ? 'Testing…' : 'Test' }}
        </Button>
        <Button
          v-if="canEdit"
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
