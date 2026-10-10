<script setup lang="ts">
/**
 * SettingsIssuesBanner — the fix-it banner at the top of every Settings
 * page (settings-cleanup-01SETUX01 WP01, FR-3).
 *
 * Rendered by SettingsShell, so it appears on every route that uses the
 * shell (/settings*, /providers, /bundles, /permissions/*, /policy,
 * /agentgraph*, /sites). Renders nothing while no issue exists.
 *
 * Issues come from lib/settingsIssues.ts providers. Per issue:
 *   - with a fix: the fix button. On success every provider re-runs (a
 *     fixed issue disappears). On failure the error is shown, followed by
 *     the manual instructions and Copy diagnostics.
 *   - without a fix: the instructions and Copy diagnostics straight away.
 *
 * Desktop-only providers are skipped in a served build (their RPCs have no
 * serve dispatch), so in practice the banner is silent there today.
 *
 * The client is injected with a null default rather than through
 * useHarnessClient(), which throws: SettingsShell is mounted by many view
 * tests that provide no client, and a banner must never break the page
 * it sits on.
 */
import { inject, onBeforeUnmount, onMounted, ref } from 'vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { useServedMode } from '@/lib/useServedMode';
import { collectSettingsIssues, type SettingsIssue } from '@/lib/settingsIssues';

const client = inject(HarnessClientKey, null);
const served = useServedMode();

const issues = ref<SettingsIssue[]>([]);
const fixBusy = ref<Record<string, boolean>>({});
const fixError = ref<Record<string, string>>({});
const copied = ref<Record<string, boolean>>({});
const copyError = ref<Record<string, string>>({});
let copiedTimer: ReturnType<typeof setTimeout> | null = null;
let unmounted = false;

function without(m: Record<string, string>, key: string): Record<string, string> {
  const next = { ...m };
  delete next[key];
  return next;
}

async function load(): Promise<void> {
  if (!client) return;
  const next = await collectSettingsIssues(client, served.value);
  if (!unmounted) issues.value = next;
}

async function runFix(issue: SettingsIssue): Promise<void> {
  if (!issue.fix || fixBusy.value[issue.id]) return;
  fixBusy.value = { ...fixBusy.value, [issue.id]: true };
  fixError.value = without(fixError.value, issue.id);
  try {
    await issue.fix.run();
    await load();
  } catch (e) {
    fixError.value = {
      ...fixError.value,
      [issue.id]: e instanceof Error && e.message ? e.message : 'The repair did not complete.',
    };
  } finally {
    fixBusy.value = { ...fixBusy.value, [issue.id]: false };
  }
}

/** Instructions are the fallback for a fixable issue, the answer otherwise. */
function showManual(issue: SettingsIssue): boolean {
  return !issue.fix || Boolean(fixError.value[issue.id]);
}

// Same clipboard pattern as CodeBlock.vue / RecoveryCodeFlow.vue:
// navigator.clipboard.writeText + a short "Copied" flash.
async function copyDiagnostics(issue: SettingsIssue): Promise<void> {
  if (!issue.diagnostics) return;
  copyError.value = without(copyError.value, issue.id);
  try {
    const text = await issue.diagnostics();
    await navigator.clipboard.writeText(text);
    copied.value = { ...copied.value, [issue.id]: true };
    if (copiedTimer) clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => {
      copied.value = {};
    }, 2000);
  } catch {
    copyError.value = {
      ...copyError.value,
      [issue.id]: 'Couldn’t copy to the clipboard. Try again, or take a screenshot of this page.',
    };
  }
}

onMounted(() => {
  void load();
});

onBeforeUnmount(() => {
  unmounted = true;
  if (copiedTimer) clearTimeout(copiedTimer);
});
</script>

<template>
  <div
    v-if="issues.length > 0"
    class="px-6 pt-4 grid gap-3"
    data-testid="settings-issues-banner"
  >
    <section
      v-for="issue in issues"
      :key="issue.id"
      role="alert"
      :class="[
        'rounded-sm border px-4 py-3',
        issue.severity === 'error'
          ? 'border-signal-danger/30 bg-signal-danger/5'
          : 'border-signal-warn/30 bg-signal-warn/5',
      ]"
      :data-testid="`settings-issue-${issue.id}`"
    >
      <h2
        :class="[
          'font-ui text-[13px] font-medium',
          issue.severity === 'error' ? 'text-signal-danger' : 'text-signal-warn',
        ]"
        data-testid="settings-issue-title"
      >
        {{ issue.title }}
      </h2>
      <p class="mt-1 text-[12px] text-ink-muted max-w-prose" data-testid="settings-issue-body">
        {{ issue.body }}
      </p>

      <div v-if="issue.fix && !fixError[issue.id]" class="mt-3">
        <button
          type="button"
          class="rounded px-3 py-1.5 text-[12px] font-ui bg-accent text-on-accent
                 hover:bg-accent-hover disabled:opacity-50 disabled:cursor-wait"
          :disabled="fixBusy[issue.id] ?? false"
          data-testid="settings-issue-fix"
          @click="runFix(issue)"
        >
          {{ fixBusy[issue.id] ? 'Working…' : issue.fix.label }}
        </button>
      </div>

      <p
        v-if="fixError[issue.id]"
        class="mt-3 text-[12px] text-signal-danger"
        data-testid="settings-issue-fix-error"
      >
        The repair didn’t work: {{ fixError[issue.id] }}
      </p>

      <template v-if="showManual(issue)">
        <ol
          v-if="issue.instructions && issue.instructions.length > 0"
          class="mt-3 ml-5 list-decimal grid gap-1 text-[12px] text-ink max-w-prose"
          data-testid="settings-issue-instructions"
        >
          <li v-for="(step, i) in issue.instructions" :key="i">{{ step }}</li>
        </ol>
        <div v-if="issue.diagnostics" class="mt-3 flex items-center gap-3">
          <button
            type="button"
            class="rounded px-3 py-1.5 text-[12px] font-ui border border-border-muted
                   bg-surface-1 text-ink hover:bg-surface-2"
            data-testid="settings-issue-copy-diagnostics"
            @click="copyDiagnostics(issue)"
          >
            {{ copied[issue.id] ? 'Copied' : 'Copy diagnostics' }}
          </button>
          <span
            v-if="copyError[issue.id]"
            class="text-[11px] text-signal-danger"
            data-testid="settings-issue-copy-error"
          >{{ copyError[issue.id] }}</span>
        </div>
      </template>
    </section>
  </div>
</template>
