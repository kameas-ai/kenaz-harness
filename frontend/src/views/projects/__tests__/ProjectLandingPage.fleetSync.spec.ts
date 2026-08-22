/**
 * ProjectLandingPage.fleetSync.spec.ts — fleet-context-sync-01NDFSEX15 WP08
 * (+ fleet-enforcement-truth-01PMZ505 WP13, remote purge)
 *
 * Specs for the fleet project sync section:
 *   1. sync toggle calls ProjectSync_Toggle and updates state
 *   2. artifact-class opt-in panel is hidden when sync is disabled
 *   3. artifact-class toggle calls ProjectSync_SetArtifactClass
 *   4-6 (WP13). AC-025: the purge control is reachable through the rendered
 *      UI, its confirm copy names both effects (remote delete + sync
 *      disabled), and cancelling does not call the RPC.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ProjectLandingPage from '@/views/projects/ProjectLandingPage.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetProjectSyncStatus } from '@/lib/types';

// ── vue-router stub ────────────────────────────────────────────────────────
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ params: { id: 'proj-test' } }),
}));

// ── fixtures ───────────────────────────────────────────────────────────────

function makeProjectSyncStatus(enabled: boolean, classes: Record<string, boolean> = {}): FleetProjectSyncStatus {
  return {
    projectID: 'proj-test',
    enabled,
    lastSeq: 0,
    lastSyncAt: '',
    artifactClasses: { classes },
  };
}

function buildClient(overrides: {
  toggleFn?: ReturnType<typeof vi.fn>;
  setClassFn?: ReturnType<typeof vi.fn>;
  deleteRemoteFn?: ReturnType<typeof vi.fn>;
} = {}) {
  const toggleFn = overrides.toggleFn ?? vi.fn(async (_id: string, enable: boolean) =>
    makeProjectSyncStatus(enable));
  const setClassFn = overrides.setClassFn ?? vi.fn(async (_id: string, opts: { classes: Record<string, boolean> }) =>
    makeProjectSyncStatus(true, opts.classes));
  const deleteRemoteFn = overrides.deleteRemoteFn ?? vi.fn(async (_id: string) => undefined);

  const client = createFakeHarnessClient({
    ProjectSync_Toggle: toggleFn,
    ProjectSync_SetArtifactClass: setClassFn,
    ProjectSync_DeleteRemote: deleteRemoteFn,
    // Also stub basic project loading
    projects: {
      get: async () => ({ id: 'proj-test', name: 'Test Project', description: '' } as never),
      list: async () => [],
      create: async () => ({ id: 'proj-test', name: 'Test Project', description: '' } as never),
      rename: async () => {},
      updateDescription: async () => {},
      remove: async () => {},
      addSession: async () => {},
      removeSession: async () => {},
      listSessions: async () => [],
      getAutonomy: async () => ({ level: null, overrides: {} }),
      setAutonomy: async () => {},
    },
  });
  return { client, toggleFn, setClassFn, deleteRemoteFn };
}

function mountPage(client = buildClient().client) {
  return mount(ProjectLandingPage, {
    attachTo: document.body,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

function q(testId: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testId}"]`);
}

describe('ProjectLandingPage — fleet sync section', () => {
  afterEach(() => {
    document.body.innerHTML = '';
    vi.clearAllMocks();
  });

  it('1. sync toggle calls ProjectSync_Toggle and shows enabled state', async () => {
    const { client, toggleFn } = buildClient();
    mountPage(client);
    await flushPromises();

    // Sync section should exist
    expect(q('project-sync-section')).not.toBeNull();

    // Toggle starts disabled
    const toggleBtn = q('project-sync-toggle') as HTMLButtonElement;
    expect(toggleBtn).not.toBeNull();
    expect(toggleBtn.getAttribute('aria-pressed')).toBe('false');

    // Click to enable
    toggleBtn.click();
    await flushPromises();

    expect(toggleFn).toHaveBeenCalledWith('proj-test', true);
    // After toggle, button should reflect enabled state
    const updatedBtn = q('project-sync-toggle') as HTMLButtonElement;
    expect(updatedBtn.getAttribute('aria-pressed')).toBe('true');
  });

  it('2. artifact-class panel is hidden when sync is disabled', async () => {
    const { client } = buildClient();
    mountPage(client);
    await flushPromises();

    // Sync starts disabled
    expect(q('project-artifact-class-panel')).toBeNull();

    // Enable sync
    (q('project-sync-toggle') as HTMLButtonElement).click();
    await flushPromises();

    // Now panel should be visible
    expect(q('project-artifact-class-panel')).not.toBeNull();

    // All 4 artifact class rows should exist
    for (const cls of ['notes', 'code', 'documents', 'binaries']) {
      expect(q(`artifact-class-row-${cls}`)).not.toBeNull();
    }
  });

  it('3. artifact-class toggle calls ProjectSync_SetArtifactClass', async () => {
    const toggleFn = vi.fn(async (_id: string, enable: boolean) =>
      makeProjectSyncStatus(enable));
    const setClassFn = vi.fn(async (_id: string, opts: { classes: Record<string, boolean> }) =>
      makeProjectSyncStatus(true, opts.classes));
    const { client } = buildClient({ toggleFn, setClassFn });
    mountPage(client);
    await flushPromises();

    // Enable sync first
    (q('project-sync-toggle') as HTMLButtonElement).click();
    await flushPromises();

    // Toggle 'notes' artifact class
    const notesCheckbox = q('artifact-class-toggle-notes') as HTMLInputElement;
    expect(notesCheckbox).not.toBeNull();
    notesCheckbox.checked = true;
    notesCheckbox.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();

    expect(setClassFn).toHaveBeenCalledWith('proj-test', expect.objectContaining({
      classes: expect.objectContaining({ notes: true }),
    }));
  });

  // ── Remote purge (fleet-enforcement-truth-01PMZ505 WP13) ──────────────────

  it('4. renders a Purge remote control in the sync section', async () => {
    const { client } = buildClient();
    mountPage(client);
    await flushPromises();

    expect(q('project-purge-remote-btn')).not.toBeNull();
  });

  it('5. AC-025: drives the action through the confirm dialog to ProjectSync_DeleteRemote, and the confirm copy names both effects', async () => {
    const { client, deleteRemoteFn } = buildClient();
    mountPage(client);
    await flushPromises();

    const purgeBtn = q('project-purge-remote-btn') as HTMLButtonElement;
    expect(purgeBtn).not.toBeNull();
    purgeBtn.click();
    await flushPromises();

    // The confirm dialog must actually render — asserting the RPC fired
    // without ever rendering the confirmation proves nothing about a
    // control nobody could find, which is the whole finding.
    const dialogMessage = q('confirm-dialog-message');
    expect(dialogMessage).not.toBeNull();
    const messageText = (dialogMessage!.textContent ?? '').toLowerCase();
    expect(messageText).toContain('delete');
    expect(messageText).toContain('sync');
    expect(messageText).toMatch(/off|disable/);

    expect(deleteRemoteFn).not.toHaveBeenCalled();

    const confirmBtn = q('confirm-dialog-confirm') as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    confirmBtn.click();
    await flushPromises();

    expect(deleteRemoteFn).toHaveBeenCalledWith('proj-test');
  });

  it('6. cancelling the confirm dialog does not call ProjectSync_DeleteRemote', async () => {
    const { client, deleteRemoteFn } = buildClient();
    mountPage(client);
    await flushPromises();

    (q('project-purge-remote-btn') as HTMLButtonElement).click();
    await flushPromises();

    const cancelBtn = q('confirm-dialog-cancel') as HTMLButtonElement;
    expect(cancelBtn).not.toBeNull();
    cancelBtn.click();
    await flushPromises();

    expect(deleteRemoteFn).not.toHaveBeenCalled();
  });
});
