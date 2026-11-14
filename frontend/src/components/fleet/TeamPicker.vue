<script setup lang="ts">
/**
 * TeamPicker — choose one of the caller's Fleet teams (kenaz-fleet spec 018
 * H-1). Options are in tree order (parent before children), indented by
 * level, with the default team marked as the org root. The value is the
 * team id, never the name: two teams may share a name.
 */
import type { FleetTeamView } from '@/lib/types';
import { teamDepth, teamOptionLabel } from '@/lib/fleetTeams';

defineProps<{
  teams: readonly FleetTeamView[];
  disabled?: boolean;
  /** data-testid of the select; lets two dialogs tell their pickers apart. */
  testid?: string;
}>();

const model = defineModel<string>({ required: true });
</script>

<template>
  <select
    v-model="model"
    class="font-ui text-[12px] text-ink bg-surface-1 border border-border-muted rounded-md px-2 py-1 w-full"
    aria-label="Team"
    :disabled="disabled"
    :data-testid="testid ?? 'fleet-team-picker'"
  >
    <option
      v-for="team in teams"
      :key="team.id"
      :value="team.id"
      :data-team-id="team.id"
      :data-depth="teamDepth(team)"
    >
      {{ teamOptionLabel(team) }}
    </option>
  </select>
</template>
