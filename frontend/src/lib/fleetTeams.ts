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
import type { ContextShareStateView, FleetTeamView } from './types';

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

/**
 * pickableTeams — the teams a share can go to. Fleet's push accepts direct
 * memberships only, so an `inherited` team (spec 018 D-2, not served yet)
 * is shown in the Account tree but never offered as a share target.
 */
export function pickableTeams(teams: readonly FleetTeamView[]): FleetTeamView[] {
  return teams.filter((t) => t.membership !== 'inherited');
}

/**
 * initialShareTeam — where a share dialog starts. An entry already shared
 * with a team starts on that team (a re-share must not move it), else the
 * enrolled team / root / first team (pickInitialTeam).
 */
export function initialShareTeam(
  teams: readonly FleetTeamView[],
  priorTeamId: string | undefined,
  preferredId?: string,
): string {
  if (priorTeamId && teams.some((t) => t.id === priorTeamId)) return priorTeamId;
  return pickInitialTeam(teams, preferredId);
}

/**
 * folderInitialTeam — where the folder dialog starts: the one team every
 * previously team-shared entry in the batch is on, when there is exactly
 * one and it is pickable; otherwise as for a single entry with no history.
 */
export function folderInitialTeam(
  teams: readonly FleetTeamView[],
  priors: readonly ContextShareStateView[],
  preferredId?: string,
): string {
  const ids = new Set(priors.filter((p) => p.layer === 'team' && p.team_id).map((p) => p.team_id as string));
  if (ids.size === 1) return initialShareTeam(teams, [...ids][0], preferredId);
  return pickInitialTeam(teams, preferredId);
}

function teamName(teams: readonly FleetTeamView[], id: string): string | null {
  return teams.find((t) => t.id === id)?.name ?? null;
}

interface ShareChoice {
  layer: 'team' | 'org';
  teamId: string;
}

/** Audience the chosen target reaches, for the warnings below. */
function widensToOrg(teams: readonly FleetTeamView[], choice: ShareChoice): boolean {
  return choice.layer === 'org' || teams.some((t) => t.id === choice.teamId && t.is_default);
}

/**
 * shareMoveWarning — a plain warning when sharing an entry would move it
 * away from the team it is shared with now (Fleet overwrites the team on
 * re-share). null when there is nothing to warn about. Team names are
 * shown as Fleet serves them.
 */
export function shareMoveWarning(
  teams: readonly FleetTeamView[],
  prior: ContextShareStateView | null | undefined,
  choice: ShareChoice,
): string | null {
  if (!prior || prior.layer !== 'team' || !prior.team_id) return null;
  if (choice.layer === 'team' && (choice.teamId === prior.team_id || choice.teamId === '')) return null;
  const from = teamName(teams, prior.team_id);
  const lead = from ? `This file is shared with ${from}.` : 'This file is shared with a team.';
  if (choice.layer === 'org') {
    return `${lead} Publishing it org-wide makes it visible to your whole organisation.`;
  }
  const to = teamName(teams, choice.teamId) ?? 'that team';
  if (widensToOrg(teams, choice)) {
    return `${lead} Sharing it with ${to} makes it visible to your whole organisation.`;
  }
  return `${lead} Sharing it with ${to} moves it there instead.`;
}

/**
 * folderShareMoveWarning — the batch form: how many of the entries being
 * shared would move off the team they are shared with now.
 */
export function folderShareMoveWarning(
  teams: readonly FleetTeamView[],
  priors: readonly ContextShareStateView[],
  choice: ShareChoice,
): string | null {
  const moving = priors.filter(
    (p) =>
      p.layer === 'team' &&
      !!p.team_id &&
      (choice.layer === 'org' || (choice.teamId !== '' && choice.teamId !== p.team_id)),
  ).length;
  if (moving === 0) return null;
  const lead =
    moving === 1
      ? '1 of these files is shared with a different team.'
      : `${moving} of these files are shared with a different team.`;
  if (choice.layer === 'org') {
    return `${lead} Publishing org-wide makes ${moving === 1 ? 'it' : 'them'} visible to your whole organisation.`;
  }
  const to = teamName(teams, choice.teamId) ?? 'that team';
  if (widensToOrg(teams, choice)) {
    return `${lead} Sharing with ${to} makes ${moving === 1 ? 'it' : 'them'} visible to your whole organisation.`;
  }
  return `${lead} Sharing with ${to} moves ${moving === 1 ? 'it' : 'them'} there instead.`;
}

/** Shown when, with no team list, a re-share keeps an entry on its recorded team. */
export const STAYS_WITH_TEAM_NOTE = "It stays with the team it's shared with now.";

/**
 * staysWithTeamNote — with no team list (older Fleet, the read failed) there
 * is no picker, and a "team" re-share sends each entry's recorded team. Say
 * so plainly, so the member is not left guessing where it goes. null when a
 * picker is shown, the layer is org, or no entry has a recorded team.
 */
export function staysWithTeamNote(
  pickable: readonly FleetTeamView[],
  priors: readonly (ContextShareStateView | null | undefined)[],
  layer: 'team' | 'org',
): string | null {
  if (pickable.length > 0 || layer !== 'team') return null;
  return priors.some((p) => p?.layer === 'team' && !!p.team_id) ? STAYS_WITH_TEAM_NOTE : null;
}

/**
 * readShareStates — where this device last shared each of nodeIDs. Any
 * failure reads as "no record": the dialogs then start as for a new entry.
 */
export async function readShareStates(
  client: HarnessClient,
  nodeIDs: string[],
): Promise<ContextShareStateView[]> {
  try {
    const got = await client.contexts.shareStates(nodeIDs);
    return Array.isArray(got) ? got.filter((st) => st && typeof st.node_id === 'string') : [];
  } catch {
    return [];
  }
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
