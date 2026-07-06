<script setup lang="ts">
/**
 * SkillsPanel — Settings → Slash Commands → Fleet Skills section.
 *
 * Lists all fleet-installed skills (catalog + org-mandated) with:
 *   - "Org-managed" badge for mandated skills (FR-302).
 *   - "Shadowed" indicator when the effective trigger is occupied by a
 *     higher-priority command (FR-401).
 *   - Inline rename form to set a local trigger alias resolving a shadow
 *     conflict (FR-401). Clearing the alias reverts to the canonical trigger.
 *   - Uninstall button for catalog skills (disabled for org-managed).
 *
 * (fleet-skills-sync-01NDFSEX18 WP04/WP06)
 */
import { ref, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { push as pushToast } from '@/composables/useToastQueue';
import type { SkillItem } from '@/lib/types';

const client = useHarnessClient();

// ── state ─────────────────────────────────────────────────────────────
const skills = ref<SkillItem[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

// Per-item rename state: skillId → new trigger value
const renaming = ref<Record<string, string>>({});
// Per-item busy flag
const busyIds = ref<Set<string>>(new Set());

// ── load ──────────────────────────────────────────────────────────────
async function refresh(): Promise<void> {
  loading.value = true;
  loadError.value = null;
  try {
    skills.value = await client.slashcmd.skillList();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
    skills.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(() => { void refresh(); });

// ── helpers ───────────────────────────────────────────────────────────

function setBusy(id: string, on: boolean): void {
  const s = new Set(busyIds.value);
  if (on) s.add(id);
  else s.delete(id);
  busyIds.value = s;
}

function effectiveTrigger(skill: SkillItem): string {
  return skill.localTrigger || skill.trigger;
}

// ── actions ───────────────────────────────────────────────────────────

async function uninstall(skill: SkillItem): Promise<void> {
  if (skill.orgManaged) return; // blocked by UI — guard anyway
  setBusy(skill.id, true);
  try {
    await client.slashcmd.skillUninstall(skill.id);
    pushToast(`Uninstalled /${effectiveTrigger(skill)}`);
    await refresh();
  } catch (err) {
    pushToast(
      `Uninstall failed: ${err instanceof Error ? err.message : String(err)}`,
      { level: 'error' },
    );
  } finally {
    setBusy(skill.id, false);
  }
}

function startRename(skill: SkillItem): void {
  renaming.value = {
    ...renaming.value,
    [skill.id]: skill.localTrigger ?? '',
  };
}

function cancelRename(skillId: string): void {
  const copy = { ...renaming.value };
  delete copy[skillId];
  renaming.value = copy;
}

async function saveRename(skill: SkillItem): Promise<void> {
  const newTrigger = (renaming.value[skill.id] ?? '').trim();
  setBusy(skill.id, true);
  try {
    await client.slashcmd.skillRenameLocalTrigger(skill.id, newTrigger);
    cancelRename(skill.id);
    pushToast(
      newTrigger
        ? `Renamed /${skill.trigger} → /${newTrigger}`
        : `Cleared local alias for /${skill.trigger}`,
    );
    await refresh();
  } catch (err) {
    pushToast(
      `Rename failed: ${err instanceof Error ? err.message : String(err)}`,
      { level: 'error' },
    );
  } finally {
    setBusy(skill.id, false);
  }
}
</script>

<template>
  <section data-testid="skills-panel">
    <h3 class="mb-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
      Fleet Skills
    </h3>

    <!-- Load error -->
    <div
      v-if="loadError"
      class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="skills-load-error"
    >
      {{ loadError }}
    </div>

    <!-- Loading -->
    <div
      v-if="loading"
      class="font-ui text-[12px] text-ink-muted"
      role="status"
      data-testid="skills-loading"
    >
      Loading skills…
    </div>

    <!-- Empty state -->
    <div
      v-else-if="skills.length === 0"
      class="rounded-md border border-border-muted bg-surface-1 px-4 py-5 text-center"
      data-testid="skills-empty"
    >
      <p class="font-ui text-[12px] text-ink-muted">
        No fleet skills installed. Browse the Marketplace to install skills.
      </p>
    </div>

    <!-- Skills table -->
    <table
      v-else
      class="w-full border-collapse text-left"
      data-testid="skills-table"
    >
      <thead>
        <tr class="border-b border-border-muted text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          <th class="px-3 py-2 font-ui font-medium">Trigger</th>
          <th class="px-3 py-2 font-ui font-medium">Description</th>
          <th class="px-3 py-2 font-ui font-medium">Source</th>
          <th class="px-3 py-2 font-ui font-medium">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="skill in skills"
          :key="skill.id"
          class="border-b border-border-muted"
          :data-testid="`skill-row-${skill.id}`"
        >
          <!-- Trigger column -->
          <td class="px-3 py-2">
            <div class="flex flex-col gap-0.5">
              <code class="font-mono text-[12px] text-ink">/{{ effectiveTrigger(skill) }}</code>
              <!-- Original trigger when local alias is set -->
              <span
                v-if="skill.localTrigger"
                class="font-ui text-[10px] text-ink-subtle"
                :data-testid="`skill-canonical-trigger-${skill.id}`"
              >
                (canonical: /{{ skill.trigger }})
              </span>
              <!-- Shadowed warning (FR-401) -->
              <span
                v-if="skill.shadowed"
                class="mt-0.5 rounded-sm bg-amber-100 px-1.5 py-0.5 font-ui text-[10px] font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
                :data-testid="`skill-shadowed-badge-${skill.id}`"
              >
                Shadowed — /{{ skill.trigger }} is taken by a personal command
              </span>
            </div>
          </td>

          <!-- Description -->
          <td class="px-3 py-2 font-ui text-[12px] text-ink-muted max-w-xs truncate">
            {{ skill.description || '—' }}
          </td>

          <!-- Source / badges -->
          <td class="px-3 py-2">
            <div class="flex flex-wrap gap-1">
              <!-- Org-managed badge (FR-302) -->
              <span
                v-if="skill.orgManaged"
                class="rounded-sm bg-blue-100 px-1.5 py-0.5 font-ui text-[10px] font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400"
                :data-testid="`skill-orgmanaged-badge-${skill.id}`"
              >
                Org-managed
              </span>
              <span
                v-else
                class="rounded-sm bg-surface-2 px-1.5 py-0.5 font-ui text-[10px] text-ink-muted"
              >
                {{ skill.version ? `v${skill.version}` : 'catalog' }}
              </span>
            </div>
          </td>

          <!-- Actions column -->
          <td class="px-3 py-2">
            <!-- Rename form (inline, per-row) -->
            <div
              v-if="skill.id in renaming"
              class="flex items-center gap-1.5"
              :data-testid="`skill-rename-form-${skill.id}`"
            >
              <input
                v-model="renaming[skill.id]"
                type="text"
                :placeholder="skill.trigger"
                class="h-6 w-28 rounded-sm border border-border-muted bg-surface-0 px-1.5 font-mono text-xs text-ink focus:outline-none focus:ring-1 focus:ring-accent"
                :data-testid="`skill-rename-input-${skill.id}`"
              />
              <button
                type="button"
                class="h-6 rounded-sm bg-accent px-2 font-ui text-[10px] font-medium text-white hover:bg-accent/90 disabled:opacity-50"
                :disabled="busyIds.has(skill.id)"
                :data-testid="`skill-rename-save-btn-${skill.id}`"
                @click="saveRename(skill)"
              >
                Save
              </button>
              <button
                type="button"
                class="h-6 rounded-sm px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2"
                :data-testid="`skill-rename-cancel-btn-${skill.id}`"
                @click="cancelRename(skill.id)"
              >
                Cancel
              </button>
            </div>

            <!-- Action buttons (when not renaming) -->
            <div
              v-else
              class="flex items-center gap-1.5"
            >
              <!-- Rename trigger button — shown for all skills; useful to clear a shadow -->
              <button
                type="button"
                class="h-6 rounded-sm border border-border-muted px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2"
                :data-testid="`skill-rename-btn-${skill.id}`"
                @click="startRename(skill)"
              >
                Rename trigger
              </button>
              <!-- Uninstall — disabled for org-managed skills (FR-302) -->
              <button
                v-if="!skill.orgManaged"
                type="button"
                class="h-6 rounded-sm border border-signal-danger/40 px-2 font-ui text-[10px] text-signal-danger hover:bg-signal-danger/10 disabled:opacity-50"
                :disabled="busyIds.has(skill.id)"
                :data-testid="`skill-uninstall-btn-${skill.id}`"
                @click="uninstall(skill)"
              >
                {{ busyIds.has(skill.id) ? 'Removing…' : 'Uninstall' }}
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
