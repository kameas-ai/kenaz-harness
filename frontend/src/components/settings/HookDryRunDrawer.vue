<script setup lang="ts">
/**
 * HookDryRunDrawer — side drawer that fires a hook against a synthetic
 * payload and displays the returned HookOutput + MergedOutput.
 *
 * The user can edit the synthetic payload JSON in a textarea before
 * firing. The drawer renders:
 *   1. The synthetic payload editor (pre-filled with a sample for the
 *      hook's event from SYNTHETIC_PAYLOADS).
 *   2. Fire button + latency.
 *   3. Per-hook output JSON viewer.
 *   4. Merged decision summary (blocked / approved / permission decision).
 *   5. Shell stdout / stderr (for kind=shell hooks).
 *
 * hooks-event-surface-expansion-01KZNP3A WP07c.
 */
import { ref, watch, computed } from 'vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { SYNTHETIC_PAYLOADS } from '@/lib/hooks';
import type { Hook, DryRunResult } from '@/lib/types';
import type { HookEventName } from '@/lib/hooks';

const props = defineProps<{
  hook: Hook | null;
  open: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const client = useHarnessClient();

// ── payload state ────────────────────────────────────────────────────

const payload = ref('{}');
const running = ref(false);
const runError = ref<string | null>(null);
const result = ref<DryRunResult | null>(null);

// Pre-fill the payload when the hook (or event) changes.
watch(
  () => [props.hook, props.open] as const,
  ([h, open]) => {
    if (!open || !h) return;
    const sample = SYNTHETIC_PAYLOADS[h.event as HookEventName];
    payload.value = sample ?? '{}';
    result.value = null;
    runError.value = null;
  },
  { immediate: true },
);

// ── actions ──────────────────────────────────────────────────────────

async function fireRun(): Promise<void> {
  if (!props.hook) return;
  running.value = true;
  runError.value = null;
  result.value = null;
  try {
    // Validate JSON before sending to avoid a confusing Go error.
    let payloadStr = payload.value.trim();
    if (!payloadStr) payloadStr = '{}';
    JSON.parse(payloadStr); // throws if invalid — caught below
    result.value = await client.hooks.dryRun(props.hook.id, payloadStr);
  } catch (err) {
    runError.value = err instanceof Error ? err.message : String(err);
  } finally {
    running.value = false;
  }
}

// ── derived display ──────────────────────────────────────────────────

const decisionBadgeClass = computed<string>(() => {
  if (!result.value) return '';
  if (result.value.merged.blocked) return 'bg-signal-danger/10 text-signal-danger border-signal-danger';
  if (result.value.merged.permissionDenied) return 'bg-signal-danger/10 text-signal-danger border-signal-danger';
  if (result.value.merged.permissionAllowed) return 'bg-accent-glow text-accent border-accent-hairline';
  const d = result.value.output.decision;
  if (d === 'block') return 'bg-signal-danger/10 text-signal-danger border-signal-danger';
  if (d === 'approve') return 'bg-accent-glow text-accent border-accent-hairline';
  return 'bg-surface-2 text-ink-muted border-border-muted';
});

const decisionLabel = computed<string>(() => {
  if (!result.value) return '';
  if (result.value.merged.blocked) return `BLOCK: ${result.value.merged.blockReason ?? ''}`;
  if (result.value.merged.permissionDenied) return `DENY: ${result.value.merged.permissionReason ?? ''}`;
  if (result.value.merged.permissionAllowed) return `ALLOW: ${result.value.merged.permissionReason ?? ''}`;
  const d = result.value.output.decision;
  if (!d) return 'NO OPINION';
  return d.toUpperCase() + (result.value.output.reason ? `: ${result.value.output.reason}` : '');
});
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open && hook"
      class="fixed inset-0 z-50 flex"
      role="dialog"
      aria-modal="true"
      aria-label="Hook dry run"
      data-testid="hook-dry-run-drawer"
    >
      <!-- Scrim -->
      <div
        class="flex-1 bg-modal-overlay"
        data-testid="hook-dry-run-scrim"
        @click="emit('close')"
      />

      <!-- Drawer panel -->
      <div
        class="w-[520px] max-w-full overflow-y-auto border-l border-border-muted bg-surface-0 shadow-lg flex flex-col"
      >
        <!-- Header -->
        <header
          class="flex items-center justify-between border-b border-border-muted px-6 py-4 shrink-0"
        >
          <div>
            <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">DRY RUN</div>
            <h2 class="mt-0.5 font-ui text-base font-semibold text-ink">
              {{ hook.name || hook.id }}
            </h2>
            <p class="font-mono text-[10px] text-ink-subtle">
              {{ hook.event }} · {{ hook.kind }}
            </p>
          </div>
          <Button variant="ghost" data-testid="hook-dry-run-close" @click="emit('close')">
            Close
          </Button>
        </header>

        <!-- Body -->
        <div class="flex-1 px-6 py-4 grid gap-4">
          <!-- Payload editor -->
          <div>
            <label
              for="dry-run-payload"
              class="block font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1"
            >
              Synthetic payload (JSON)
            </label>
            <textarea
              id="dry-run-payload"
              v-model="payload"
              class="w-full text-xs font-mono px-2 py-2 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent resize-y"
              rows="8"
              data-testid="hook-dry-run-payload"
              spellcheck="false"
            />
          </div>

          <!-- Fire button -->
          <div class="flex items-center gap-3">
            <Button
              variant="accent"
              :disabled="running"
              data-testid="hook-dry-run-fire"
              @click="fireRun"
            >
              {{ running ? 'Running…' : 'Fire hook' }}
            </Button>
            <span
              v-if="result"
              class="font-ui text-[11px] text-ink-muted"
              data-testid="hook-dry-run-latency"
            >
              {{ result.latencyMs }} ms
            </span>
          </div>

          <!-- Error banner -->
          <div
            v-if="runError"
            class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
            role="alert"
            data-testid="hook-dry-run-error"
          >
            {{ runError }}
          </div>

          <!-- Results section -->
          <template v-if="result">
            <!-- Decision badge -->
            <div>
              <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
                Merged decision
              </p>
              <span
                :class="decisionBadgeClass"
                class="inline-block font-mono text-[11px] rounded border px-2 py-0.5"
                data-testid="hook-dry-run-decision"
              >
                {{ decisionLabel }}
              </span>
            </div>

            <!-- AdditionalContext -->
            <div
              v-if="result.output.additionalContext || result.merged.additionalContext"
              data-testid="hook-dry-run-additional-context"
            >
              <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
                Additional context injected
              </p>
              <pre
                class="text-xs font-mono bg-surface-1 border border-border-muted rounded p-2 whitespace-pre-wrap text-ink"
              >{{ result.merged.additionalContext || result.output.additionalContext }}</pre>
            </div>

            <!-- HookOutput JSON -->
            <div>
              <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
                Hook output (raw)
              </p>
              <pre
                class="text-xs font-mono bg-surface-1 border border-border-muted rounded p-2 overflow-auto max-h-40 text-ink"
                data-testid="hook-dry-run-output-json"
              >{{ JSON.stringify(result.output, null, 2) }}</pre>
            </div>

            <!-- Shell stdout / stderr (kind=shell only) -->
            <template v-if="hook.kind === 'shell'">
              <div v-if="result.stdout" data-testid="hook-dry-run-stdout">
                <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
                  Stdout
                  <span v-if="result.exitCode !== 0" class="text-signal-danger ml-1">
                    (exit {{ result.exitCode }})
                  </span>
                </p>
                <pre
                  class="text-xs font-mono bg-surface-1 border border-border-muted rounded p-2 overflow-auto max-h-32 text-ink"
                >{{ result.stdout }}</pre>
              </div>
              <div v-if="result.stderr" data-testid="hook-dry-run-stderr">
                <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1 text-signal-warning">
                  Stderr
                </p>
                <pre
                  class="text-xs font-mono bg-surface-1 border border-signal-warning/40 rounded p-2 overflow-auto max-h-32 text-signal-warning"
                >{{ result.stderr }}</pre>
              </div>
            </template>

            <!-- Merged output full JSON -->
            <div>
              <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
                Merged output
              </p>
              <pre
                class="text-xs font-mono bg-surface-1 border border-border-muted rounded p-2 overflow-auto max-h-40 text-ink"
                data-testid="hook-dry-run-merged-json"
              >{{ JSON.stringify(result.merged, null, 2) }}</pre>
            </div>
          </template>
        </div>
      </div>
    </div>
  </Teleport>
</template>
