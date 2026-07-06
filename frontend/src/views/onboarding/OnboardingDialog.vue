<script setup lang="ts">
/**
 * OnboardingDialog — top-level dialog mounted on app boot when the
 * onboarding API reports FirstRun = true. Mission
 * harness-self-mcp-onboarding-01KQ8TDU WP08 (tail polish, v0.5.4).
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
 */

import { onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { OnboardingCard, StarterSummary } from '@/lib/harnessClient';
import BaseDialog from '@/components/ui/BaseDialog.vue';

const props = defineProps<{
  open: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'navigate-to-session', sessionId: string): void;
}>();

const client = useHarnessClient();

const phase = ref<'phase1' | 'starter-pick'>('phase1');
const card = ref<OnboardingCard | null>(null);
const currentState = ref<string>('welcome');
const fieldValues = ref<Record<string, string>>({});
const starters = ref<StarterSummary[]>([]);
const submitting = ref(false);
const errorMsg = ref<string | null>(null);

async function refreshCard() {
  errorMsg.value = null;
  // Render a usable card immediately so the dialog is NEVER button-less, even
  // if begin() is slow, hangs, or the API isn't wired. begin() (below) then
  // overrides it with the real FSM card when it resolves.
  if (!card.value) {
    card.value = {
      title: 'Welcome to Kenaz',
      body: 'Let\'s get you set up.',
      actions: [
        { id: 'next', label: 'Get started', primary: true },
        { id: 'dismiss', label: 'Skip onboarding' },
      ],
    };
  }
  try {
    const resp = await client.onboarding.begin();
    currentState.value = resp.state;
    card.value = resp.card;
  } catch {
    // Keep the static fallback card already shown above.
  }
}

async function loadStarters() {
  try {
    const list = await client.onboarding.listStarters();
    if (list.length > 0) {
      starters.value = list;
    } else {
      // Fallback embedded starters if backend returns empty.
      starters.value = [
        { id: 'code', title: 'Set me up for code work', description: 'Anthropic Claude + filesystem + git MCP.' },
        { id: 'writing', title: 'Set me up for writing', description: 'Claude + filesystem + brave-search.' },
        { id: 'research', title: 'Set me up for research', description: 'Claude + brave-search + fetch + arxiv.' },
        { id: 'data', title: 'Set me up for data analysis', description: 'Claude + python-runtime.' },
        { id: 'chat', title: 'Just chat', description: 'Skip the assisted setup.' },
      ];
    }
  } catch {
    starters.value = [
      { id: 'code', title: 'Set me up for code work', description: 'Anthropic Claude + filesystem + git MCP.' },
      { id: 'writing', title: 'Set me up for writing', description: 'Claude + filesystem + brave-search.' },
      { id: 'research', title: 'Set me up for research', description: 'Claude + brave-search + fetch + arxiv.' },
      { id: 'data', title: 'Set me up for data analysis', description: 'Claude + python-runtime.' },
      { id: 'chat', title: 'Just chat', description: 'Skip the assisted setup.' },
    ];
  }
}

async function onAction(id: string) {
  if (id === 'dismiss') {
    try { await client.onboarding.dismiss(); } catch { /* best-effort */ }
    emit('close');
    return;
  }
  if (id === 'next') {
    await loadStarters();
    phase.value = 'starter-pick';
    return;
  }
  // Generic FSM event — send to backend.
  submitting.value = true;
  errorMsg.value = null;
  try {
    const resp = await client.onboarding.step({
      state: currentState.value,
      event: id,
      payload: { ...fieldValues.value },
    });
    currentState.value = resp.state;
    card.value = resp.card;
    if (resp.card?.error_message) {
      errorMsg.value = resp.card.error_message;
    }
    // If FSM landed on done, show starter picker.
    if (resp.state === 'done') {
      await loadStarters();
      phase.value = 'starter-pick';
    }
  } catch (e: unknown) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}

async function onPickStarter(s: StarterSummary) {
  if (s.id === 'chat') {
    try { await client.onboarding.dismiss(); } catch { /* best-effort */ }
    emit('close');
    return;
  }
  submitting.value = true;
  errorMsg.value = null;
  try {
    const resp = await client.onboarding.restartPhase2({ starterId: s.id });
    if (resp.sessionId) {
      emit('navigate-to-session', resp.sessionId);
    }
    emit('close');
  } catch (e: unknown) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}

onMounted(() => {
  if (props.open) {
    void refreshCard();
  }
});

// `open` flips to true asynchronously (App.vue probes onboarding.state() after
// mount), so onMounted alone often runs while open is still false and the card
// never loads — leaving a bare "Welcome" with no buttons. Re-run refreshCard
// whenever the dialog opens without a card.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen && !card.value) {
      void refreshCard();
    }
  },
);
</script>

<template>
  <BaseDialog :open="open" title="Welcome to Kenaz" panel-class="onboarding-dialog" @close="emit('close')">
    <div>
      <header class="dialog-head">
        <h2 v-if="card">{{ card.title }}</h2>
        <h2 v-else>Welcome</h2>
      </header>
      <section class="dialog-body">
        <template v-if="phase === 'phase1' && card">
          <p v-if="card.body">{{ card.body }}</p>
          <p v-if="errorMsg" class="dialog-error">{{ errorMsg }}</p>
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
          <p v-if="errorMsg" class="dialog-error">{{ errorMsg }}</p>
          <ul class="starter-list">
            <li v-for="s in starters" :key="s.id">
              <button class="starter-card" :disabled="submitting" @click="onPickStarter(s)">
                <strong>{{ s.title }}</strong>
                <span>{{ s.description }}</span>
              </button>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </BaseDialog>
</template>

<style scoped>
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
