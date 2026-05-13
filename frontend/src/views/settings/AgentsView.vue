<script setup lang="ts">
/**
 * AgentsView — Settings → Agents panel.
 *
 * Lists all sub-agent profiles (bundled + user-authored). Bundled
 * profiles are read-only and marked with a "Built-in" chip. The user
 * can:
 *   - Inspect any profile (click row to open AgentProfileEditor).
 *   - Duplicate a bundled profile to create a user-owned copy.
 *   - Edit/delete user-authored profiles.
 *
 * branch-subagent-interactive-01KZNP3B WP05.
 */
import { ref, onMounted, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import AgentProfileEditor from '@/views/settings/AgentProfileEditor.vue';
import type { AgentProfile, AgentProfileSummary } from '@/lib/types';

const client = useHarnessClient();

const profiles = ref<AgentProfileSummary[]>([]);
const loading = ref(false);
const lastError = ref<string | null>(null);

// Editor state
const editorOpen = ref(false);
const editingProfile = ref<AgentProfile | null>(null);
const editorReadOnly = ref(false);

async function refresh() {
  loading.value = true;
  lastError.value = null;
  try {
    profiles.value = await client.agents.listProfiles();
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void refresh();
});

async function openProfile(summary: AgentProfileSummary) {
  try {
    const full = await client.agents.loadProfile(summary.id);
    editingProfile.value = full;
    editorReadOnly.value = summary.bundled;
    editorOpen.value = true;
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

async function duplicateProfile(summary: AgentProfileSummary) {
  try {
    const full = await client.agents.loadProfile(summary.id);
    const copy: AgentProfile = {
      ...full,
      id: full.id + '-copy',
      name: full.name + ' (copy)',
      bundled: false,
    };
    editingProfile.value = copy;
    editorReadOnly.value = false;
    editorOpen.value = true;
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

function openNew() {
  editingProfile.value = {
    id: '',
    name: '',
    description: '',
    autonomyTier: 'default',
    mergePolicy: 'auto',
    allowedTools: [],
    deniedTools: [],
    bundled: false,
  };
  editorReadOnly.value = false;
  editorOpen.value = true;
}

async function handleSave(profile: AgentProfile) {
  try {
    await client.agents.saveProfile(profile);
    editorOpen.value = false;
    editingProfile.value = null;
    await refresh();
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

async function handleDelete(id: string) {
  if (!confirm(`Delete profile "${id}"? This cannot be undone.`)) return;
  try {
    await client.agents.deleteProfile(id);
    await refresh();
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
  }
}

function handleEditorClose() {
  editorOpen.value = false;
  editingProfile.value = null;
}

const bundledProfiles = computed(() => profiles.value.filter((p) => p.bundled));
const userProfiles = computed(() => profiles.value.filter((p) => !p.bundled));

function mergePolicyLabel(mp: string) {
  switch (mp) {
    case 'auto':
      return 'Auto';
    case 'confirm':
      return 'Confirm';
    case 'manual':
      return 'Manual';
    default:
      return mp;
  }
}
</script>

<template>
  <div class="flex flex-col gap-4 p-4" data-testid="agents-view">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-base font-semibold text-ink">Sub-agent Profiles</h2>
      <button
        type="button"
        class="font-ui text-sm text-ink-link hover:underline"
        data-testid="agents-new-btn"
        @click="openNew"
      >
        + New Profile
      </button>
    </div>

    <p class="font-ui text-sm text-ink-muted">
      Profiles configure the model, tools, and budget for sub-agents spawned via
      <code class="rounded bg-surface-2 px-1 text-xs">__subagent_dispatch</code>.
      Built-in profiles are read-only — duplicate to customise.
    </p>

    <div v-if="loading" class="font-ui text-sm text-ink-subtle" data-testid="agents-loading">
      Loading…
    </div>

    <div
      v-else-if="lastError"
      class="rounded border border-status-error/30 bg-status-error/5 p-3 font-ui text-sm text-status-error"
      role="alert"
      data-testid="agents-error"
    >
      {{ lastError }}
    </div>

    <template v-else>
      <!-- Bundled profiles section -->
      <section v-if="bundledProfiles.length > 0" aria-label="Built-in profiles">
        <h3 class="mb-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Built-in
        </h3>
        <ul class="flex flex-col gap-2" data-testid="bundled-profile-list">
          <li
            v-for="profile in bundledProfiles"
            :key="profile.id"
            class="rounded-md border border-border-muted bg-surface-0 p-3"
            :data-testid="`profile-row-${profile.id}`"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <span class="font-ui text-sm font-medium text-ink">{{ profile.name }}</span>
                  <span
                    class="rounded-full bg-accent/10 px-2 py-0.5 font-ui text-[10px] uppercase tracking-wide text-accent"
                  >
                    Built-in
                  </span>
                  <span
                    class="rounded-full bg-surface-2 px-2 py-0.5 font-ui text-[10px] text-ink-muted"
                  >
                    {{ mergePolicyLabel(profile.mergePolicy) }}
                  </span>
                </div>
                <p class="mt-1 font-ui text-xs text-ink-muted">{{ profile.description }}</p>
                <p v-if="profile.model" class="mt-0.5 font-ui text-[11px] text-ink-subtle">
                  {{ profile.model }}
                </p>
              </div>
              <div class="flex shrink-0 gap-2">
                <button
                  type="button"
                  class="font-ui text-xs text-ink-link hover:underline"
                  :data-testid="`profile-inspect-${profile.id}`"
                  @click="openProfile(profile)"
                >
                  Inspect
                </button>
                <button
                  type="button"
                  class="font-ui text-xs text-ink-link hover:underline"
                  :data-testid="`profile-duplicate-${profile.id}`"
                  @click="duplicateProfile(profile)"
                >
                  Duplicate
                </button>
              </div>
            </div>
          </li>
        </ul>
      </section>

      <!-- User profiles section -->
      <section aria-label="User profiles">
        <h3 class="mb-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          My Profiles
        </h3>
        <div
          v-if="userProfiles.length === 0"
          class="rounded-md border border-dashed border-border-muted p-6 text-center font-ui text-sm text-ink-subtle"
          data-testid="user-profiles-empty"
        >
          No custom profiles yet. Click "+ New Profile" or duplicate a built-in to get started.
        </div>
        <ul v-else class="flex flex-col gap-2" data-testid="user-profile-list">
          <li
            v-for="profile in userProfiles"
            :key="profile.id"
            class="rounded-md border border-border-muted bg-surface-0 p-3"
            :data-testid="`profile-row-${profile.id}`"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <span class="font-ui text-sm font-medium text-ink">{{ profile.name }}</span>
                  <span
                    class="rounded-full bg-surface-2 px-2 py-0.5 font-ui text-[10px] text-ink-muted"
                  >
                    {{ mergePolicyLabel(profile.mergePolicy) }}
                  </span>
                </div>
                <p class="mt-1 font-ui text-xs text-ink-muted">{{ profile.description }}</p>
                <p v-if="profile.model" class="mt-0.5 font-ui text-[11px] text-ink-subtle">
                  {{ profile.model }}
                </p>
              </div>
              <div class="flex shrink-0 gap-2">
                <button
                  type="button"
                  class="font-ui text-xs text-ink-link hover:underline"
                  :data-testid="`profile-edit-${profile.id}`"
                  @click="openProfile(profile)"
                >
                  Edit
                </button>
                <button
                  type="button"
                  class="font-ui text-xs text-status-error hover:underline"
                  :data-testid="`profile-delete-${profile.id}`"
                  @click="handleDelete(profile.id)"
                >
                  Delete
                </button>
              </div>
            </div>
          </li>
        </ul>
      </section>
    </template>

    <!-- Profile editor modal -->
    <AgentProfileEditor
      v-if="editorOpen && editingProfile"
      :profile="editingProfile"
      :read-only="editorReadOnly"
      @save="handleSave"
      @close="handleEditorClose"
    />
  </div>
</template>
