<script setup lang="ts">
/**
 * ProjectLandingPage — landing page for a single project (/projects/:id).
 *
 * WP02 ships the placeholder shell: project metadata header + sessions
 * list. Scoped attachments and project-scoped memory arrive in WP04
 * and WP06 respectively.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { MessageSquare } from '@/shell/icons';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Project, Session } from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const project = ref<Project | null>(null);
const sessions = ref<readonly Session[]>([]);
const loading = ref(false);
const errorMsg = ref<string | null>(null);

const projectId = computed(() => {
  const v = route.params.id;
  return typeof v === 'string' ? v : '';
});

async function load(id: string) {
  if (!id) return;
  loading.value = true;
  errorMsg.value = null;
  try {
    project.value = await client.projects.get(id);
    sessions.value = await client.projects.listSessions(id);
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
    project.value = null;
    sessions.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void load(projectId.value);
});

watch(projectId, (next) => {
  void load(next);
});

function openSession(id: string) {
  void router.push(`/sessions/${id}`);
}
</script>

<template>
  <div class="h-full flex flex-col" data-testid="project-landing">
    <CanvasHead
      number="08"
      section="PROJECT"
      :title="project?.name ?? 'Project'"
      :subtitle="
        project?.description
          ? project.description
          : 'Group related sessions; attach context at project scope (later WPs).'
      "
    />

    <div
      v-if="errorMsg"
      class="mx-6 mt-4 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-xs text-signal-danger"
      role="alert"
      data-testid="project-error"
    >
      {{ errorMsg }}
    </div>

    <div class="flex-1 overflow-y-auto px-6 py-4 space-y-6">
      <section>
        <h2
          class="font-ui text-[11px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
        >
          Sessions
          <span class="ml-1 text-ink-dim">({{ sessions.length }})</span>
        </h2>
        <div
          v-if="loading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-loading"
        >
          Loading…
        </div>
        <ul
          v-else-if="sessions.length > 0"
          class="mt-2 space-y-1"
          data-testid="project-sessions-list"
        >
          <li v-for="s in sessions" :key="s.id">
            <button
              type="button"
              class="flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left font-ui text-sm text-ink-muted hover:bg-surface-2 hover:text-ink"
              :data-testid="`project-session-${s.id}`"
              @click="openSession(s.id)"
            >
              <MessageSquare :size="14" />
              <span class="truncate">{{ s.name }}</span>
            </button>
          </li>
        </ul>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="project-sessions-empty"
        >
          No sessions yet. Create one from the rail and assign it to this
          project.
        </p>
      </section>
    </div>
  </div>
</template>
