/**
 * Team tree (kenaz-fleet spec 018 H-1): drawing order, indentation, the
 * default team as org root, and keying by team id.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TeamTreeList from '@/components/fleet/TeamTreeList.vue';
import TeamPicker from '@/components/fleet/TeamPicker.vue';
import {
  ORG_ROOT_PREFIX,
  pickInitialTeam,
  teamDepth,
  teamOptionLabel,
  useFleetTeams,
} from '@/lib/fleetTeams';
import type { HarnessClient } from '@/lib/harnessClient';
import type { FleetTeamView } from '@/lib/types';

// In tree order, as Settings_FleetTeams returns it. "Platform" appears twice
// under different parents: only the id tells the two apart.
const TREE: FleetTeamView[] = [
  { id: 'root-id', name: 'Everyone', is_default: true, role: 'member', tree_depth: 0 },
  { id: 'eng-id', name: 'Engineering', is_default: false, role: 'lead', parent_team_id: 'root-id', tree_depth: 1 },
  { id: 'plat-a', name: 'Platform', is_default: false, role: 'member', parent_team_id: 'eng-id', tree_depth: 2 },
  { id: 'db-id', name: 'Databases', is_default: false, role: 'member', parent_team_id: 'plat-a', tree_depth: 3 },
  { id: 'plat-b', name: 'Platform', is_default: false, role: 'member', parent_team_id: 'root-id', tree_depth: 1 },
];

// An older Fleet: {id, name} only, decoded flat.
const FLAT: FleetTeamView[] = [
  { id: 'a', name: 'Alpha', is_default: false, tree_depth: 0 },
  { id: 'b', name: 'Beta', is_default: false, tree_depth: 0 },
];

describe('TeamTreeList', () => {
  it('draws parent before children in served order, indented by depth', () => {
    const w = mount(TeamTreeList, { props: { teams: TREE } });
    const rows = w.findAll('[data-testid=fleet-team-tree-row]');
    expect(rows.map((r) => r.attributes('data-team-id'))).toEqual(['root-id', 'eng-id', 'plat-a', 'db-id', 'plat-b']);
    expect(rows.map((r) => r.attributes('data-depth'))).toEqual(['0', '1', '2', '3', '1']);
    expect(rows.map((r) => r.attributes('aria-level'))).toEqual(['1', '2', '3', '4', '2']);
    const pad = rows.map((r) => (r.element as HTMLElement).style.paddingLeft);
    expect(pad).toEqual(['0px', '14px', '28px', '42px', '14px']);
  });

  it('marks only the default team as the org root and shows names as served', () => {
    const w = mount(TeamTreeList, { props: { teams: TREE } });
    const rows = w.findAll('[data-testid=fleet-team-tree-row]');
    expect(rows[0].find('[data-testid=fleet-team-root]').exists()).toBe(true);
    expect(w.findAll('[data-testid=fleet-team-root]')).toHaveLength(1);
    expect(rows[0].find('.team-tree__name').text()).toBe('Everyone');
    expect(rows[1].find('[data-testid=fleet-team-lead]').exists()).toBe(true);
  });

  it('keeps two same-named teams distinct by id', () => {
    const w = mount(TeamTreeList, { props: { teams: TREE } });
    const platforms = w
      .findAll('[data-testid=fleet-team-tree-row]')
      .filter((r) => r.find('.team-tree__name').text() === 'Platform');
    expect(platforms.map((r) => r.attributes('data-team-id'))).toEqual(['plat-a', 'plat-b']);
  });

  it('draws an older flat list flat, with no root', () => {
    const w = mount(TeamTreeList, { props: { teams: FLAT } });
    const rows = w.findAll('[data-testid=fleet-team-tree-row]');
    expect(rows.map((r) => r.attributes('data-depth'))).toEqual(['0', '0']);
    expect(w.find('[data-testid=fleet-team-root]').exists()).toBe(false);
  });
});

describe('TeamPicker', () => {
  it('lists teams in tree order, values are ids, labels indented with the root marked', () => {
    const w = mount(TeamPicker, { props: { teams: TREE, modelValue: 'root-id' } });
    const opts = w.findAll('option');
    expect(opts.map((o) => o.attributes('value'))).toEqual(['root-id', 'eng-id', 'plat-a', 'db-id', 'plat-b']);
    expect(opts[0].text()).toBe(`${ORG_ROOT_PREFIX}Everyone`);
    // Indent is non-breaking spaces, four per level.
    expect(opts[2].element.textContent?.trim().length).toBeGreaterThan(0);
    expect(teamOptionLabel(TREE[2])).toBe(' '.repeat(8) + 'Platform');
    expect(teamOptionLabel(TREE[3])).toBe(' '.repeat(12) + 'Databases');
  });

  it('emits the picked team id', async () => {
    const w = mount(TeamPicker, { props: { teams: TREE, modelValue: 'root-id' } });
    await w.find('select').setValue('plat-b');
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['plat-b']);
  });
});

describe('fleetTeams helpers', () => {
  it('teamDepth clamps junk to a drawable level', () => {
    expect(teamDepth({ tree_depth: 2 })).toBe(2);
    expect(teamDepth({ tree_depth: -1 })).toBe(0);
    expect(teamDepth({ tree_depth: Number.NaN })).toBe(0);
    expect(teamDepth({ tree_depth: 999 })).toBe(16);
  });

  it('pickInitialTeam prefers the enrolled team by id, then the root, then the first', () => {
    expect(pickInitialTeam(TREE, 'plat-b')).toBe('plat-b');
    expect(pickInitialTeam(TREE, 'not-mine')).toBe('root-id');
    expect(pickInitialTeam(TREE)).toBe('root-id');
    expect(pickInitialTeam(FLAT, 'Beta')).toBe('a'); // a name is never an id
    expect(pickInitialTeam([], 'x')).toBe('');
  });

  it('useFleetTeams loads the tree, drops id-less rows, and empties on failure', async () => {
    const fleetTeams = vi
      .fn<() => Promise<FleetTeamView[]>>()
      .mockResolvedValueOnce([...TREE, { id: '', name: 'ghost', is_default: false, tree_depth: 0 }])
      .mockRejectedValueOnce(new Error('signed out'));
    const client = { settings: { fleetTeams } } as unknown as HarnessClient;
    const h = useFleetTeams(client);
    await h.load();
    expect(h.teams.value.map((t) => t.id)).toEqual(TREE.map((t) => t.id));
    expect(h.loaded.value).toBe(true);
    await h.load();
    expect(h.teams.value).toEqual([]);
  });

  it('useFleetTeams keeps the newest load when two race', async () => {
    let resolveFirst: (v: FleetTeamView[]) => void = () => {};
    const fleetTeams = vi
      .fn<() => Promise<FleetTeamView[]>>()
      .mockImplementationOnce(() => new Promise((r) => (resolveFirst = r)))
      .mockResolvedValueOnce(FLAT);
    const client = { settings: { fleetTeams } } as unknown as HarnessClient;
    const h = useFleetTeams(client);
    const first = h.load();
    await h.load();
    resolveFirst(TREE);
    await first;
    await flushPromises();
    expect(h.teams.value.map((t) => t.id)).toEqual(['a', 'b']);
  });

  it('useFleetTeams.reset drops a load still in flight', async () => {
    let resolve: (v: FleetTeamView[]) => void = () => {};
    const fleetTeams = vi.fn<() => Promise<FleetTeamView[]>>(() => new Promise((r) => (resolve = r)));
    const client = { settings: { fleetTeams } } as unknown as HarnessClient;
    const h = useFleetTeams(client);
    const pending = h.load();
    h.reset();
    resolve(TREE);
    await pending;
    expect(h.teams.value).toEqual([]);
    expect(h.loaded.value).toBe(false);
  });
});
