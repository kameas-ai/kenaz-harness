<script setup lang="ts">
/**
 * OnboardingDialog — top-level dialog mounted on app boot when the
 * onboarding API reports FirstRun = true. Mission
 * harness-self-mcp-onboarding-01KQ8TDU WP08.
 *
 * Phase 1 (deterministic FSM): the user picks a provider kind, enters
 * an API key, and runs a connection test via the backend FSM
 * (core/onboarding.FSM via OnboardingAPI.Step). Each render is driven
 * by a Card descriptor returned by the backend so the frontend stays
 * declarative — no business logic about "which state comes next".
 *
 * Phase 2 (assistant handoff): once a provider is configured, the user
 * can pick a curated starter ("set me up for code work" / "writing" /
 * "research" / "data" / "just chat"). RestartPhase2 spawns a new
 * kind=onboarding session and the frontend navigates to it; the
 * harness-self MCP server's tools take over from there.
 *
 * Status: WP08 ships the dialog scaffold + starter picker shell. Wiring
 * to the backend OnboardingAPI binding lands once the Wails bindings
 * codegen pass picks up the new view (TODO(v0.3.x)).
 */

import { computed, ref } from 'vue';

interface OnboardingCard {
  title: string;
  body?: string;
  actions?: Array<{ id: string; label: string; primary?: boolean }>;
  fields?: Array<{ id: string; label: string; placeholder?: string; secret?: boolean }>;
  error_message?: string;
  provider_hint?: string;
}

interface StarterSummary {
  id: string;
  title: string;
  description: string;
  recommendedProvider?: string;
  recommendedModel?: string;
  recommendedRecipes?: string[];
}

const props = defineProps<{
  open: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'navigate-to-session', sessionId: string): void;
}>();

const phase = ref<'phase1' | 'starter-pick'>('phase1');
const card = ref<OnboardingCard | null>(null);
const fieldValues = ref<Record<string, string>>({});
const starters = ref<StarterSummary[]>([]);
const submitting = ref(false);

// TODO(v0.3.x): replace these stubs with calls into the OnboardingAPI
// binding once the Wails codegen picks up core/rpc/views/onboarding.
async function refreshCard() {
  // Until the binding is wired, render a static placeholder so the
  // dialog mounts without crashing during dev.
  card.value = {
    title: 'Welcome to Kaneaz',
    body: 'The onboarding flow lives at this dialog. Wails bindings for OnboardingAPI land in v0.3.x.',
    actions: [
      { id: 'next', label: 'Get started', primary: true },
      { id: 'dismiss', label: 'Skip onboarding' },
    ],
  };
}

function onAction(id: string) {
  if (id === 'dismiss') {
    emit('close');
    return;
  }
  if (id === 'next') {
    phase.value = 'starter-pick';
    starters.value = [
      { id: 'code', title: 'Set me up for code work', description: 'Anthropic Claude + filesystem + git MCP.' },
      { id: 'writing', title: 'Set me up for writing', description: 'Claude + filesystem + brave-search.' },
      { id: 'research', title: 'Set me up for research', description: 'Claude + brave-search + fetch + arxiv.' },
      { id: 'data', title: 'Set me up for data analysis', description: 'Claude + python-runtime.' },
      { id: 'chat', title: 'Just chat', description: 'Skip the assisted setup.' },
    ];
  }
}

function onPickStarter(s: StarterSummary) {
  // TODO(v0.3.x): call OnboardingAPI.RestartPhase2({starterId: s.id}).
  if (s.id === 'chat') {
    emit('close');
    return;
  }
  emit('navigate-to-session', `pending-${s.id}`);
  emit('close');
}

const visible = computed(() => props.open);

if (visible.value) {
  void refreshCard();
}
</script>

<template>
  <div v-if="visible" class="onboarding-overlay" role="dialog" aria-modal="true">
    <div class="onboarding-dialog">
      <header class="dialog-head">
        <h2 v-if="card">{{ card.title }}</h2>
        <h2 v-else>Welcome</h2>
      </header>
      <section class="dialog-body">
        <template v-if="phase === 'phase1' && card">
          <p v-if="card.body">{{ card.body }}</p>
          <p v-if="card.error_message" class="dialog-error">{{ card.error_message }}</p>
          <div v-if="card.fields && card.fields.length" class="dialog-fields">
            <label v-for="f in card.fields" :key="f.id" class="dialog-field">
              <span class="field-label">{{ f.label }}</span>
              <input
                v-model="fieldValues[f.id]"
                :type="f.secret ? 'password' : 'text'"
                :placeholder="f.placeholder"
              />
            </label>
          </div>
          <div v-if="card.actions && card.actions.length" class="dialog-actions">
            <button
              v-for="a in card.actions"
              :key="a.id"
              :class="{ primary: a.primary }"
              :disabled="submitting"
              @click="onAction(a.id)"
            >
              {{ a.label }}
            </button>
          </div>
        </template>

        <template v-else-if="phase === 'starter-pick'">
          <p>Pick a starter — the onboarding agent will configure the harness for that workflow.</p>
          <ul class="starter-list">
            <li v-for="s in starters" :key="s.id">
              <button class="starter-card" @click="onPickStarter(s)">
                <strong>{{ s.title }}</strong>
                <span>{{ s.description }}</span>
              </button>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
.onboarding-overlay {
  position: fixed;
  inset: 0;
  background: var(--modal-overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.onboarding-dialog {
  background: var(--surface-2);
  color: var(--ink);
  border-radius: var(--radius-lg);
  padding: 1.5rem 2rem;
  width: min(560px, 90vw);
  box-shadow: 0 18px 60px var(--modal-shadow);
}
.dialog-head h2 {
  margin: 0 0 1rem;
  font-size: 1.4rem;
}
.dialog-body p {
  line-height: 1.5;
}
.dialog-error {
  color: var(--danger);
}
.dialog-fields {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin: 1rem 0;
}
.dialog-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.dialog-field input {
  padding: 0.5rem 0.75rem;
  background: var(--surface-3);
  border: 1px solid var(--border);
  color: inherit;
  border-radius: var(--radius-sm);
}
.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 1.25rem;
}
.dialog-actions button {
  padding: 0.5rem 1rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.dialog-actions button.primary {
  background: var(--accent);
  color: var(--surface-0);
  border-color: transparent;
}
.starter-list {
  list-style: none;
  padding: 0;
  margin: 1rem 0 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.starter-card {
  text-align: left;
  width: 100%;
  padding: 0.75rem 1rem;
  background: var(--surface-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: inherit;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.starter-card strong {
  font-size: 1rem;
}
.starter-card span {
  font-size: 0.85rem;
  color: var(--ink-muted);
}
</style>
