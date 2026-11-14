/**
 * fleetTeams — the caller's Fleet teams as a tree (kenaz-fleet spec 018 H-1).
 *
 * The backend (core/fleet OrderTeamTree, via Settings_FleetTeams) reads
 * GET /api/v1/me `teams[]` and returns it already laid out: the default
 * team first as the org root, parent before children, each entry carrying
 * `tree_depth`, the indent level it is drawn at. Cycles and parents the
 * caller is not a member of are resolved there. This module only draws.
 *
 * Rules (spec 018):
 *   - Teams are keyed by `id`. Name and slug are labels; two teams may share
 *     a name, and a rename never changes the id.
 *   - Team names are shown as Fleet serves them. The client's own words sit
 *     before the name ("Org root: "), never inside or instead of it.
 *   - A team is never put on any telemetry event.
 */
import { ref, type Ref } from 'vue';
import type { HarnessClient } from './harnessClient';
import type { FleetTeamView } from './types';

/** The client's own marker for the default team, shown before its served name. */
export const ORG_ROOT_PREFIX = 'Org root: ';

/** Non-breaking spaces per indent level in a native <option> (no CSS there). */
const OPTION_INDENT = '    ';

/** Clamp a served indent level to something drawable. */
export function teamDepth(team: Pick<FleetTeamView, 'tree_depth'>): number {
  const d = Number(team.tree_depth);
  return Number.isFinite(d) && d > 0 ? Math.min(Math.floor(d), 16) : 0;
}

/** Text of a team in a native select: indent, the root marker, the served name. */
export function teamOptionLabel(team: FleetTeamView): string {
  return OPTION_INDENT.repeat(teamDepth(team)) + (team.is_default ? ORG_ROOT_PREFIX : '') + team.name;
}

/**
 * pickInitialTeam — the team a share dialog starts on: `preferredId` (the
 * enrolled identity's team, which is where a team share went before the
 * picker existed) when it is one of the caller's teams, else the root, else
 * the first team, else ''. Matching is by id only.
 */
export function pickInitialTeam(teams: readonly FleetTeamView[], preferredId?: string): string {
  if (preferredId && teams.some((t) => t.id === preferredId)) return preferredId;
  const root = teams.find((t) => t.is_default);
  if (root) return root.id;
  return teams[0]?.id ?? '';
}

export interface FleetTeamsHandle {
  teams: Ref<FleetTeamView[]>;
  /** True once a load has finished, whatever its outcome. */
  loaded: Ref<boolean>;
  load(): Promise<void>;
  /** Empty the list and drop any load still in flight (e.g. on sign-out). */
  reset(): void;
}

/**
 * useFleetTeams — load the caller's team tree. A failure (signed out, an
 * older harness binding, served mode, network) leaves an empty list: every
 * caller then falls back to what it showed before the tree existed.
 */
export function useFleetTeams(client: HarnessClient): FleetTeamsHandle {
  const teams = ref<FleetTeamView[]>([]);
  const loaded = ref(false);
  let seq = 0;
  async function load() {
    const mine = ++seq;
    let next: FleetTeamView[] = [];
    try {
      const got = await client.settings.fleetTeams();
      next = Array.isArray(got) ? got.filter((t) => t && typeof t.id === 'string' && t.id !== '') : [];
    } catch {
      next = [];
    }
    if (mine !== seq) return; // a newer load superseded this one
    teams.value = next;
    loaded.value = true;
  }
  function reset() {
    seq++;
    teams.value = [];
    loaded.value = false;
  }
  return { teams, loaded, load, reset };
}
