<script setup lang="ts">
/**
 * CrashReportingPanel — Privacy → Crash Reporting settings tab.
 *
 * Lets the user select their crash-reporting tier, view the last 5 captured
 * events, generate a local crash report, and test their Sentry DSN.
 *
 * (sentry-error-monitoring-01KX5R8G WP05)
 */
import { ref, computed, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

const tier = ref<string>('off');
const sentryDsn = ref<string>('');
const lastFive = ref<
  Array<{
    id: string;
    capturedAt: string;
    kind: string;
    summary: string;
    sentryEventId?: string;
  }>
>([]);
const localReportPath = ref<string>('');
const testDsnResult = ref<{ ok: boolean; error?: string } | null>(null);
const saving = ref(false);
const generatingReport = ref(false);
const testingDsn = ref(false);
const errorMsg = ref<string>('');

// ── Load ────────────────────────────────────────────────────────────────────

onMounted(async () => {
  try {
    const s = await client.settings.get();
    tier.value = s.crashReportingTier ?? 'off';
    sentryDsn.value = s.sentryDsn ?? '';
    const entries = await client.sentry.getLastFive();
    lastFive.value = entries;
  } catch (err) {
    errorMsg.value = String(err);
  }
});

// ── Actions ─────────────────────────────────────────────────────────────────

async function saveTier(newTier: string) {
  saving.value = true;
  errorMsg.value = '';
  try {
    const s = await client.settings.get();
    s.crashReportingTier = newTier;
    await client.settings.set(s);
    tier.value = newTier;
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    saving.value = false;
  }
}

async function saveDsn() {
  saving.value = true;
  errorMsg.value = '';
  try {
    const s = await client.settings.get();
    s.sentryDsn = sentryDsn.value;
    await client.settings.set(s);
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    saving.value = false;
  }
}

async function generateReport() {
  generatingReport.value = true;
  errorMsg.value = '';
  try {
    const result = await client.sentry.generateLocalReport();
    localReportPath.value = result.path;
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    generatingReport.value = false;
  }
}

async function testDsn() {
  testingDsn.value = true;
  testDsnResult.value = null;
  try {
    const result = await client.sentry.testDsn(sentryDsn.value);
    testDsnResult.value = result;
  } catch (err) {
    testDsnResult.value = { ok: false, error: String(err) };
  } finally {
    testingDsn.value = false;
  }
}

// entry-points-and-crash-reporting-01PMZD13 UNIT-7, AC-17. The Test button
// above issues client.sentry.testDsn(), which resolves to
// core/sentry/test_dsn.go's client.Head(ingestURL) — plain Go, no browser,
// no CSP. Its green result previously rendered as the unqualified string
// "DSN reachable", which a user reasonably reads as "crash reports from
// this app will reach Sentry" — untrue for the RENDERER half whenever the
// active page CSP's connect-src does not allow the DSN's origin (desktop
// production: always 'none'; served mode: 'self', which also excludes an
// external Sentry ingest host). This computes that second, independent
// answer by reading the page's own <meta http-equiv="Content-Security-
// Policy"> and evaluating connect-src against the DSN's origin — the same
// decision the browser itself makes — so the sentence flips on its own if
// the CSP is ever relaxed to allow it (E-001), with no code change here.
function browserCanTransmitUnderCurrentCSP(dsn: string): boolean {
  const meta = document.querySelector('meta[http-equiv="Content-Security-Policy"]');
  const csp = meta?.getAttribute('content') ?? '';
  const match = csp.match(/connect-src\s+([^;]+)/);
  if (!match) return true; // no connect-src directive found at all — nothing to block on
  const connectSrc = match[1].trim();
  if (connectSrc === "'none'") return false;
  if (connectSrc === "'self'") {
    try {
      return new URL(dsn).origin === window.location.origin;
    } catch {
      return false; // unparsable DSN — cannot confirm same-origin, report blocked
    }
  }
  return true; // connect-src names other hosts too — assume the DSN's is among them
}

const browserTransmitStatus = computed(() => {
  if (!sentryDsn.value) return null;
  return browserCanTransmitUnderCurrentCSP(sentryDsn.value)
    ? 'Browser: this page\'s CSP allows reaching the DSN origin.'
    : "Browser: this page's CSP blocks the renderer from reaching Sentry — only the Go crash reporter (tested above) can transmit.";
});
</script>

<template>
  <section class="space-y-6 p-4" data-testid="crash-reporting-panel">
    <div>
      <h2 class="text-sm font-semibold text-ink mb-1">Crash Reporting</h2>
      <p class="text-xs text-ink-muted">
        When enabled, anonymous crash data is sent to the configured Sentry DSN
        to help diagnose errors. No conversation content, API keys, or personal
        data is ever included.
      </p>
    </div>

    <!-- Tier picker -->
    <fieldset class="space-y-2">
      <legend class="text-xs font-medium text-ink-muted uppercase tracking-wide">
        Reporting tier
      </legend>
      <div class="space-y-1">
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="crash-tier"
            value="off"
            :checked="tier === 'off'"
            class="mt-0.5"
            @change="saveTier('off')"
          />
          <span>
            <span class="text-sm text-ink font-medium">Off</span>
            <span class="block text-xs text-ink-muted">
              No crash data is sent. You can still generate a local crash report.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="crash-tier"
            value="anonymous"
            :checked="tier === 'anonymous'"
            class="mt-0.5"
            @change="saveTier('anonymous')"
          />
          <span>
            <span class="text-sm text-ink font-medium">Anonymous</span>
            <span class="block text-xs text-ink-muted">
              Redacted stack traces and error types are sent. No user identity is included.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="crash-tier"
            value="identified"
            :checked="tier === 'identified'"
            class="mt-0.5"
            @change="saveTier('identified')"
          />
          <span>
            <span class="text-sm text-ink font-medium">Identified</span>
            <span class="block text-xs text-ink-muted">
              Same as Anonymous plus your fleet identity. Requires fleet sign-in.
              Auto-downgrades to Anonymous on logout.
            </span>
          </span>
        </label>
      </div>
    </fieldset>

    <!-- DSN input -->
    <div class="space-y-1">
      <label for="sentry-dsn" class="block text-xs font-medium text-ink-muted uppercase tracking-wide">
        Sentry DSN
      </label>
      <div class="flex gap-2">
        <input
          id="sentry-dsn"
          v-model="sentryDsn"
          type="text"
          placeholder="https://key@sentry.example.com/project-id"
          class="flex-1 text-sm px-2 py-1.5 rounded border border-border-muted bg-surface text-ink"
          :disabled="saving"
          @blur="saveDsn"
        />
        <button
          type="button"
          class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink-muted hover:text-ink"
          :disabled="!sentryDsn || testingDsn"
          @click="testDsn"
        >
          {{ testingDsn ? 'Testing…' : 'Test' }}
        </button>
      </div>
      <p v-if="testDsnResult" class="text-xs" :class="testDsnResult.ok ? 'text-signal-ok' : 'text-signal-danger'">
        {{ testDsnResult.ok ? 'Go crash reporter: DSN reachable' : `Go crash reporter: DSN error: ${testDsnResult.error}` }}
      </p>
      <p v-if="testDsnResult && browserTransmitStatus" class="text-xs text-ink-muted">
        {{ browserTransmitStatus }}
      </p>
    </div>

    <!-- What we never send -->
    <div class="rounded border border-border-muted p-3 space-y-1">
      <p class="text-xs font-semibold text-ink">What we never send:</p>
      <ul class="text-xs text-ink-muted list-disc list-inside space-y-0.5">
        <li>Conversation messages or prompt text</li>
        <li>API keys, tokens, or credentials (stripped by redactor)</li>
        <li>File contents or filesystem paths outside the harness data dir</li>
        <li>Email addresses or phone numbers (stripped by redactor)</li>
        <li>Attributes marked <code>private.</code> in structured logs</li>
        <li>Long binding-arg strings (&gt;200 chars, truncated)</li>
      </ul>
    </div>

    <!-- Last 5 cache -->
    <div class="space-y-2">
      <h3 class="text-xs font-medium text-ink-muted uppercase tracking-wide">
        Recent crash events (last {{ lastFive.length }})
      </h3>
      <div v-if="lastFive.length === 0" class="text-xs text-ink-muted">
        No crash events captured yet.
      </div>
      <ul v-else class="space-y-1">
        <li
          v-for="entry in lastFive"
          :key="entry.id"
          class="text-xs rounded border border-border-muted px-2 py-1 flex justify-between items-start gap-2"
        >
          <span class="text-ink">{{ entry.summary }}</span>
          <span class="text-ink-muted shrink-0">
            {{ new Date(entry.capturedAt).toLocaleDateString() }}
          </span>
        </li>
      </ul>
    </div>

    <!-- Generate local report -->
    <div class="space-y-2">
      <button
        type="button"
        class="text-xs px-3 py-1.5 rounded border border-border-muted text-ink-muted hover:text-ink"
        :disabled="generatingReport"
        @click="generateReport"
      >
        {{ generatingReport ? 'Generating…' : 'Generate local crash report' }}
      </button>
      <p v-if="localReportPath" class="text-xs text-ink-muted break-all">
        Saved to: <code>{{ localReportPath }}</code>
      </p>
    </div>

    <p v-if="errorMsg" class="text-xs text-signal-danger">{{ errorMsg }}</p>
  </section>
</template>
