<script setup lang="ts">
/**
 * PolicyView — Cedar policy bundle inspector + decision audit panel
 * (agent-kernel-graph WP14).
 *
 * This view is intentionally self-contained while the
 * cedarpolicy RPC binding is being wired through the bindings layer:
 * it accepts the policy file list and the recent-decision list as
 * props plus a `reload` callback. A future commit replaces the
 * props-driven interface with the typed harnessClient call.
 *
 * Renders:
 *   - per-file parse status (embedded default + every disk file under
 *     <DataDir>/policy/), with a clear pass/fail badge.
 *   - a Reload button that re-walks the policy directory.
 *   - a recent-decisions audit list (allow / deny / not_applicable
 *     plus principal + action + resource + reason).
 */
import { computed, ref } from 'vue';

export type PolicyOutcome = 'allow' | 'deny' | 'not_applicable' | 'unknown';

export interface PolicyFile {
  name: string;
  path?: string;
  bytes?: number;
  embedded?: boolean;
  parse_ok: boolean;
  parse_err?: string;
}

export interface PolicyDecision {
  outcome: PolicyOutcome;
  action: string;
  principal: string;
  resource: string;
  matched_policy?: string;
  reason?: string;
  evaluated_at: string;
}

const props = defineProps<{
  files: readonly PolicyFile[];
  decisions: readonly PolicyDecision[];
  onReload?: () => Promise<void> | void;
}>();

const reloading = ref(false);
const reloadError = ref<string | null>(null);

async function handleReload() {
  if (!props.onReload) return;
  reloading.value = true;
  reloadError.value = null;
  try {
    await props.onReload();
  } catch (err) {
    reloadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    reloading.value = false;
  }
}

const hasFailures = computed(() => props.files.some((f) => !f.parse_ok));
const okCount = computed(() => props.files.filter((f) => f.parse_ok).length);
const totalCount = computed(() => props.files.length);

function outcomeBadgeClass(outcome: PolicyOutcome): string {
  switch (outcome) {
    case 'allow':
      return 'badge-allow';
    case 'deny':
      return 'badge-deny';
    case 'not_applicable':
      return 'badge-na';
    default:
      return 'badge-unknown';
  }
}
</script>

<template>
  <div class="policy-view">
    <header class="policy-view__head">
      <div class="policy-view__title">
        <span class="policy-view__num">14</span>
        <span class="policy-view__section">POLICY</span>
      </div>
      <h1 class="policy-view__heading">Cedar policy bundle</h1>
      <p class="policy-view__sub">
        Local policy files under <code>&lt;DataDir&gt;/policy/</code>, plus the
        bundled default. Engine decisions are audited locally and never leave
        the device.
      </p>
    </header>

    <section class="policy-view__section-block">
      <div class="policy-view__row">
        <h2 class="policy-view__h2">Policy files</h2>
        <button
          type="button"
          class="policy-view__btn"
          :disabled="reloading || !props.onReload"
          aria-label="Reload policy files"
          data-testid="policy-reload"
          @click="handleReload"
        >
          {{ reloading ? 'Reloading…' : 'Reload' }}
        </button>
      </div>
      <div class="policy-view__count">
        {{ okCount }} of {{ totalCount }} files parsed
        <span v-if="hasFailures" class="policy-view__warn"> — see errors below</span>
      </div>
      <div v-if="reloadError" class="policy-view__error" role="alert">
        Reload failed: {{ reloadError }}
      </div>
      <ul v-if="files.length > 0" class="policy-view__file-list">
        <li
          v-for="f in files"
          :key="f.name"
          class="policy-view__file"
          :data-file-status="f.parse_ok ? 'ok' : 'error'"
        >
          <span class="policy-view__file-name">{{ f.name }}</span>
          <span v-if="f.embedded" class="policy-view__tag">embedded</span>
          <span v-if="f.parse_ok" class="policy-view__ok">OK</span>
          <span v-else class="policy-view__err">FAILED</span>
          <span v-if="!f.parse_ok && f.parse_err" class="policy-view__err-msg">
            {{ f.parse_err }}
          </span>
          <span v-if="f.bytes != null" class="policy-view__bytes">
            {{ f.bytes }} B
          </span>
        </li>
      </ul>
      <div v-else class="policy-view__empty">No policy files loaded.</div>
    </section>

    <section class="policy-view__section-block">
      <h2 class="policy-view__h2">Recent decisions</h2>
      <ul v-if="decisions.length > 0" class="policy-view__decisions" data-testid="policy-decisions">
        <li v-for="(d, idx) in decisions" :key="idx" class="policy-view__decision">
          <span class="policy-view__badge" :class="outcomeBadgeClass(d.outcome)">
            {{ d.outcome }}
          </span>
          <span class="policy-view__action">{{ d.action }}</span>
          <span class="policy-view__resource">{{ d.resource }}</span>
          <span class="policy-view__principal">{{ d.principal }}</span>
          <span v-if="d.reason" class="policy-view__reason">{{ d.reason }}</span>
        </li>
      </ul>
      <div v-else class="policy-view__empty">
        No decisions yet — the engine has not been consulted in this session.
      </div>
    </section>
  </div>
</template>

<style scoped>
.policy-view {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 1rem;
  font-family: var(--font-ui, system-ui, sans-serif);
}
.policy-view__head {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.policy-view__title {
  display: flex;
  gap: 0.5rem;
  font-size: 0.75rem;
  color: var(--color-ink-muted, #888);
  letter-spacing: 0.1em;
  text-transform: uppercase;
}
.policy-view__num {
  font-weight: 700;
}
.policy-view__heading {
  font-size: 1.25rem;
  font-weight: 600;
  margin: 0;
}
.policy-view__sub {
  margin: 0;
  color: var(--color-ink-muted, #888);
  font-size: 0.875rem;
}
.policy-view__section-block {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.policy-view__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.policy-view__h2 {
  font-size: 1rem;
  font-weight: 600;
  margin: 0;
}
.policy-view__btn {
  border: 1px solid var(--color-border, #444);
  border-radius: 0.125rem;
  padding: 0.25rem 0.625rem;
  background: var(--color-surface-2, transparent);
  color: var(--color-ink, inherit);
  cursor: pointer;
  font-size: 0.75rem;
}
.policy-view__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.policy-view__count {
  font-size: 0.75rem;
  color: var(--color-ink-muted, #888);
}
.policy-view__warn {
  color: var(--color-signal-warn, #c80);
}
.policy-view__error {
  color: var(--color-signal-danger, #c33);
  font-size: 0.875rem;
}
.policy-view__file-list,
.policy-view__decisions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.policy-view__file,
.policy-view__decision {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.5rem;
  padding: 0.25rem 0;
  border-bottom: 1px solid var(--color-border-muted, #2a2a2a);
  font-size: 0.875rem;
}
.policy-view__file-name {
  font-family: var(--font-mono, monospace);
}
.policy-view__tag {
  font-size: 0.625rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--color-ink-muted, #888);
}
.policy-view__ok {
  color: var(--color-signal-ok, #4a4);
  font-weight: 600;
  font-size: 0.75rem;
}
.policy-view__err {
  color: var(--color-signal-danger, #c33);
  font-weight: 600;
  font-size: 0.75rem;
}
.policy-view__err-msg,
.policy-view__bytes,
.policy-view__reason,
.policy-view__principal {
  color: var(--color-ink-muted, #888);
  font-size: 0.75rem;
}
.policy-view__empty {
  color: var(--color-ink-muted, #888);
  font-size: 0.875rem;
}
.policy-view__badge {
  display: inline-block;
  min-width: 4ch;
  text-align: center;
  font-size: 0.75rem;
  font-weight: 600;
  padding: 0.0625rem 0.375rem;
  border-radius: 0.125rem;
  text-transform: uppercase;
}
.badge-allow {
  background: var(--color-signal-ok-bg, rgba(80, 180, 120, 0.15));
  color: var(--color-signal-ok, #4a4);
}
.badge-deny {
  background: var(--color-signal-danger-bg, rgba(220, 80, 80, 0.15));
  color: var(--color-signal-danger, #c33);
}
.badge-na,
.badge-unknown {
  background: var(--color-surface-2, rgba(140, 140, 140, 0.1));
  color: var(--color-ink-muted, #888);
}
.policy-view__action {
  font-family: var(--font-mono, monospace);
  font-weight: 500;
}
.policy-view__resource {
  font-family: var(--font-mono, monospace);
  font-size: 0.8125rem;
}
</style>
