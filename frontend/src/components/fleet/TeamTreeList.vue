<script setup lang="ts">
/**
 * TeamTreeList — the caller's Fleet teams drawn as a tree (kenaz-fleet spec
 * 018 H-1). Rows arrive already ordered parent-before-children with their
 * indent level (lib/fleetTeams.ts); this component only draws them.
 *
 * Keyed by team id. The default team is marked as the org root; every name
 * is shown as Fleet serves it.
 */
import type { FleetTeamView } from '@/lib/types';
import { teamDepth } from '@/lib/fleetTeams';

defineProps<{
  teams: readonly FleetTeamView[];
}>();

/** Indent per tree level, in px. */
const INDENT_PX = 14;
</script>

<template>
  <ul class="team-tree" role="tree" data-testid="fleet-team-tree">
    <li
      v-for="team in teams"
      :key="team.id"
      class="team-tree__row"
      role="treeitem"
      :aria-level="teamDepth(team) + 1"
      :data-team-id="team.id"
      :data-depth="teamDepth(team)"
      :style="{ paddingLeft: `${teamDepth(team) * INDENT_PX}px` }"
      data-testid="fleet-team-tree-row"
    >
      <span class="team-tree__name">{{ team.name }}</span>
      <span v-if="team.is_default" class="team-tree__tag" data-testid="fleet-team-root">Org root</span>
      <span v-if="team.role === 'lead'" class="team-tree__tag" data-testid="fleet-team-lead">Lead</span>
    </li>
  </ul>
</template>

<style scoped>
.team-tree {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.team-tree__row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.team-tree__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.team-tree__tag {
  flex: none;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  opacity: 0.7;
}
</style>
