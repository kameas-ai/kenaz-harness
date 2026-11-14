<script setup lang="ts">
/**
 * FleetDeviceNamePanel — Settings → Account → "Device name" (Fleet ER-43),
 * next to the device info in AccountPanel.
 *
 * The name your organization's Fleet admins see for this device on the
 * Hosts page (the enroll `hostname` field). Read and written through
 * Settings_Get/SetFleetDeviceName:
 *   - "Share this device's name with your organization" (default on). Off
 *     sends no name, and the name Fleet has is cleared.
 *   - Which name: a neutral "<OS> device <4 hex>" (default, relay parity
 *     with legal D58), this computer's own name, or a custom name.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { FleetDeviceNameSettings } from '@/lib/types';

const client = useHarnessClient();

type Mode = 'default' | 'computer' | 'custom';

const loaded = ref(false);
const saving = ref(false);
const errorMsg = ref('');
const share = ref(true);
const mode = ref<Mode>('default');
const customInput = ref('');
const computerName = ref('');
const defaultName = ref('');
const effectiveName = ref('');
const maxLength = ref(64);

function apply(dn: FleetDeviceNameSettings) {
  share.value = dn.share;
  customInput.value = dn.customName;
  mode.value = dn.customName ? 'custom' : dn.useComputerName ? 'computer' : 'default';
  computerName.value = dn.computerName;
  defaultName.value = dn.defaultName;
  effectiveName.value = dn.effectiveName;
  maxLength.value = dn.maxLength > 0 ? dn.maxLength : 64;
}

onMounted(async () => {
  try {
    apply(await client.settings.getFleetDeviceName());
    loaded.value = true;
  } catch (err) {
    errorMsg.value = String(err);
  }
});

const defaultLabel = computed(() => defaultName.value || 'chosen when you sign in');

/** Truncate to max code points (Fleet counts runes, not UTF-16 units). */
function capRunes(v: string, max: number): string {
  const cps = Array.from(v);
  return cps.length > max ? cps.slice(0, max).join('') : v;
}

function onCustomInput(e: Event) {
  const el = e.target as HTMLInputElement;
  const capped = capRunes(el.value, maxLength.value);
  if (capped !== el.value) el.value = capped;
  customInput.value = capped;
}

/**
 * Persist the panel. `source` says what changed:
 *   - 'share': always saved — turning sharing off must never be swallowed.
 *   - 'mode': picking "A name I choose" with nothing typed yet is not a
 *     choice to save (the stored name stays until a name is typed).
 *   - 'custom': an emptied custom field falls back to the neutral name.
 */
async function save(source: 'share' | 'mode' | 'custom') {
  const custom = capRunes(customInput.value.trim(), maxLength.value);
  if (source === 'mode' && mode.value === 'custom' && custom === '') return;
  saving.value = true;
  errorMsg.value = '';
  try {
    await client.settings.setFleetDeviceName({
      share: share.value,
      useComputerName: mode.value === 'computer',
      customName: mode.value === 'custom' ? custom : '',
      computerName: '',
      defaultName: '',
      effectiveName: '',
      maxLength: 0,
    });
    apply(await client.settings.getFleetDeviceName());
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="space-y-4 p-4" data-testid="fleet-device-name-panel">
    <fieldset class="space-y-3" :disabled="!loaded || saving">
      <label class="flex items-start gap-2 cursor-pointer">
        <input
          v-model="share"
          type="checkbox"
          class="mt-0.5"
          data-testid="fleet-device-name-share"
          @change="save('share')"
        />
        <span>
          <span class="text-sm text-ink font-medium">Share this device's name with your organization</span>
          <span class="block text-xs text-ink-muted">
            Your organization's Fleet admins see this name for this device, so they can tell your
            devices apart. When this is off, no name is sent and the one Fleet has is removed.
          </span>
        </span>
      </label>

      <div v-if="share" class="space-y-2 pl-6" role="radiogroup" aria-label="Name to share">
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            v-model="mode"
            type="radio"
            value="default"
            data-testid="fleet-device-name-mode-default"
            @change="save('mode')"
          />
          <span class="text-sm text-ink">
            A neutral name
            <span class="text-ink-muted" data-testid="fleet-device-name-default">({{ defaultLabel }})</span>
          </span>
        </label>
        <label class="flex items-center gap-2" :class="computerName ? 'cursor-pointer' : 'opacity-60'">
          <input
            v-model="mode"
            type="radio"
            value="computer"
            :disabled="!computerName"
            data-testid="fleet-device-name-mode-computer"
            @change="save('mode')"
          />
          <span class="text-sm text-ink">
            This computer's name
            <span v-if="computerName" class="text-ink-muted" data-testid="fleet-device-name-computer">({{ computerName }})</span>
            <span v-else class="text-ink-muted">(not available)</span>
          </span>
        </label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input
            v-model="mode"
            type="radio"
            value="custom"
            data-testid="fleet-device-name-mode-custom"
            @change="save('mode')"
          />
          <span class="text-sm text-ink">A name I choose</span>
        </label>
        <input
          v-if="mode === 'custom'"
          :value="customInput"
          type="text"
          placeholder="e.g. Work laptop"
          class="ml-6 w-64 rounded border border-hairline bg-surface-1 px-2 py-1 text-sm"
          data-testid="fleet-device-name-custom"
          @input="onCustomInput"
          @change="save('custom')"
        />
        <p v-if="mode === 'custom'" class="ml-6 text-xs text-ink-muted">
          Up to {{ maxLength }} characters. Leave it empty to use the neutral name.
        </p>
      </div>

      <p class="text-xs text-ink-muted" data-testid="fleet-device-name-effective">
        <template v-if="share && effectiveName">
          Shown to your organization as <span class="text-ink font-medium">{{ effectiveName }}</span>.
        </template>
        <template v-else-if="share">A neutral name is shared once you sign in.</template>
        <template v-else>No device name is shared.</template>
      </p>
    </fieldset>

    <p v-if="errorMsg" class="text-xs text-signal-danger" data-testid="fleet-device-name-error">{{ errorMsg }}</p>
  </section>
</template>
