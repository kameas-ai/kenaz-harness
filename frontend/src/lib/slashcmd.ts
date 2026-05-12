/**
 * slashcmd.ts — composable for user-defined slash commands.
 *
 * Exposes useSlashCommands() which loads and caches the user command list
 * for a given projectID. The cache is invalidated on fetch by default.
 * Future: wire to the `slash_user_changed` Wails event once the backend
 * emits it (WP04 follow-up).
 */

import { ref, readonly, type Ref } from 'vue';
import { useHarnessClient } from './harnessClientContext';
import type { UserCommand, UserCommandSummary, SlashRunResult } from './types';

export type { UserCommand, UserCommandSummary, SlashRunResult };

/**
 * useSlashCommands — reactive composable for user-defined slash commands.
 *
 * @param projectID — the active project ID (empty string for global-only).
 */
export function useSlashCommands(projectID: string = '') {
  const client = useHarnessClient();
  const commands = ref<UserCommandSummary[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function refresh() {
    loading.value = true;
    error.value = null;
    try {
      commands.value = await client.slashcmd.list(projectID);
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
    } finally {
      loading.value = false;
    }
  }

  async function get(name: string): Promise<UserCommand | null> {
    try {
      return await client.slashcmd.get(name, projectID);
    } catch {
      return null;
    }
  }

  async function save(cmd: UserCommand): Promise<void> {
    await client.slashcmd.save(cmd);
    await refresh();
  }

  async function remove(name: string): Promise<void> {
    await client.slashcmd.delete(name, projectID);
    await refresh();
  }

  async function run(
    name: string,
    args: Record<string, string>,
    sessionID: string,
    cwd: string,
    selection: string,
  ): Promise<SlashRunResult> {
    return client.slashcmd.run(name, args, sessionID, projectID, cwd, selection);
  }

  return {
    commands: readonly(commands) as Readonly<Ref<readonly UserCommandSummary[]>>,
    loading: readonly(loading),
    error: readonly(error),
    refresh,
    get,
    save,
    remove,
    run,
  };
}
