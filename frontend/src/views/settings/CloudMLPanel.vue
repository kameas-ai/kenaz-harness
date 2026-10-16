<script setup lang="ts">
/**
 * CloudMLPanel — Settings → Sync → "Cloud ML" (ml-producer-01MLPRD01 WP01,
 * spec §5; kenaz-fleet docs/contract-harness-ml.md).
 *
 * Driven by GET /api/v1/me/ml through Fleet_MLStatus. Shows the org's
 * offload switch and policy, the member's own workflow_events opt-in, the
 * notice status, whether sending is effective, and retention. The opt-in
 * toggle appears only when offload is on and the policy is member_choice.
 * When a notice acknowledgement is required, the contract's notice text
 * (rendered by the backend: org, retention, retain_on_withdrawal variant)
 * is shown with an "I have read this" button that posts the version shown; a 409
 * policy_changed comes back as noticeChanged and the new notice is shown
 * again. When Fleet requires a newer notice TEXT revision than the one the
 * backend renders (noticeNeedsDashboard), no notice and no acknowledge button are
 * shown; the panel points the user to the Fleet dashboard's consent card.
 *
 * WP06: when Fleet's pending-approvals hub lists an ml_notice
 * (noticeFromHub), noticeText is the hub's server-rendered body_text, shown
 * verbatim (line breaks kept, including any "Changed since you last
 * approved" section), and "I have read this" approves that hub item by id — the
 * harness acknowledges exactly the text it showed. The local template and
 * the dashboard routing above are only the fallback for a Fleet without the
 * hub.
 *
 * Fallback notice (2026-10-10): without the hub, `notice` is counsel's
 * approved rev-1 text (Fleet text revision 1) as structured fields: heading,
 * intro, bold-labelled paragraphs and the policy paragraph, laid out here
 * with the policy's buttons — member_choice: "Turn on Offload" (set the
 * workflow_events opt-in, then acknowledge the version shown) and "Not now"
 * (close, no request); required (policy on), or re-acknowledging with
 * Offload already on: "I have read this" (ack). The hub branch's button reads
 * "I have read this" too — legal's fixed labels (kameas-ai-11, 2026-10-16);
 * never "I agree", "Accept" or "Consent" on the ML notice.
 * With the hub, the item's title (`noticeTitle`) is the heading above its
 * verbatim body_text (Fleet's rev-2 notice keeps the heading out of it).
 *
 * Hidden when signed out or without the hosted_inference capability.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MLStatus } from '@/lib/types';

/** Spec §5: the fixed harness line, verbatim. */
const HARNESS_LINE =
  'From this app, Kenaz sends only what the agent does in your harness sessions (tool used, ' +
  'outcome, timing, hashed file names, the first two words of commands). It never sends file ' +
  'contents, prompts or replies.';

const client = useHarnessClient();

const status = ref<MLStatus | null>(null);
const busy = ref(false);
const errorMsg = ref('');
const noticeChanged = ref(false);

async function refresh() {
  try {
    status.value = await client.fleet.mlStatus();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(refresh);

const visible = computed(() => !!status.value?.signedIn && !!status.value?.entitled);
const loaded = computed(() => !!status.value?.loaded);
const showOptIn = computed(
  () => loaded.value && !!status.value?.orgOffloadEnabled && status.value?.orgPolicy === 'member_choice',
);
/**
 * kenaz-fleet PR 225: an ack is owed for a notice text NEWER than the one this
 * harness renders. The user never saw that text here, so it must not be
 * acknowledged from this panel: no notice text, no acknowledge button —
 * route them to the Fleet dashboard's consent card instead.
 */
const DASHBOARD_NOTICE =
  'An updated notice is waiting for your approval. Open your Kenaz Fleet dashboard to review and ' +
  'approve it; uploads from this device stay off until you do.';
const needsDashboard = computed(() => loaded.value && !!status.value?.noticeNeedsDashboard);
const dashboardUrl = computed(() => status.value?.noticeDashboardUrl ?? '');
/** "Not now" closes the fallback notice for this visit; no request is made. */
const dismissed = ref(false);
/** The fallback notice (counsel's rev-1 text), when the hub is not the source. */
const localNotice = computed(() => (status.value?.noticeFromHub ? undefined : status.value?.notice));
/**
 * Member choice, offload on, not opted in: the fallback notice is how the
 * member turns Offload on ("Turn on Offload" / "Not now"), so it shows even
 * before Fleet asks for an acknowledgement.
 */
const memberChoicePrompt = computed(
  () =>
    !!localNotice.value &&
    localNotice.value.policy === 'member_choice' &&
    !!status.value?.orgOffloadEnabled &&
    !status.value?.userWorkflowEventsOptedIn,
);
const showNotice = computed(
  () =>
    loaded.value &&
    !needsDashboard.value &&
    !dismissed.value &&
    (!!status.value?.noticeAckRequired || memberChoicePrompt.value),
);
const ACTION_TURN_ON = 'Turn on Offload';
const ACTION_NOT_NOW = 'Not now';
const ACTION_READ = 'I have read this';

function openDashboard() {
  if (dashboardUrl.value) client.openExternalURL(dashboardUrl.value);
}

/**
 * WP05: the org's exclusions, read-only, as Fleet words them (legal,
 * kameas-ai-11, 2026-10-16): the backend's port of Fleet's
 * exclusionsSummary ("Excluded from upload: …" plus the legacy free-text
 * sentence), never markup of our own. Inside the fallback notice it is
 * notice.exclusionsText, before any action button; with no notice shown it
 * is exclusionsSummary on its own. On the hub path nothing is added: the
 * served body_text is the only exclusions source.
 */
const exclusionsSummary = computed(() => status.value?.exclusionsSummary ?? '');

function policyLabel(p: string): string {
  switch (p) {
    case 'on':
      return 'On for every member';
    case 'off':
      return 'Off for every member';
    case 'member_choice':
      return "Each member's choice";
    default:
      return p || '—';
  }
}

function formatTime(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

const noticeLine = computed(() => {
  const s = status.value;
  if (!s) return '';
  if (s.noticeAckRequired) return `Acknowledgement required (version ${s.noticeVersion})`;
  if (s.noticeAckedAt) return `Acknowledged ${formatTime(s.noticeAckedAt)} (version ${s.noticeVersion})`;
  return 'Not required';
});

function isPolicyChanged(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return msg.includes('policy_changed');
}

async function acknowledge() {
  const s = status.value;
  if (!s || busy.value) return;
  busy.value = true;
  errorMsg.value = '';
  try {
    if (s.noticeFromHub && s.noticeItemId) {
      // The hub's approve action (looked up server-side by id) carries the
      // notice_version and text_revision of the text shown here.
      const res = await client.fleet.approveItem(s.noticeItemId);
      noticeChanged.value = !!res.changed;
      await refresh();
      return;
    }
    const next = await client.fleet.mlAckNotice(s.noticeVersion, ACTION_READ);
    status.value = next;
    noticeChanged.value = !!next.noticeChanged;
  } catch (err) {
    if (isPolicyChanged(err)) {
      // Belt and braces: the backend already folds 409 into noticeChanged.
      noticeChanged.value = true;
      await refresh();
    } else {
      errorMsg.value = err instanceof Error ? err.message : String(err);
    }
  } finally {
    busy.value = false;
  }
}

/**
 * "Turn on Offload" (member choice): set the workflow_events opt-in, then
 * acknowledge the notice version that was shown. If Fleet now serves the
 * notice from the hub, or routes to the dashboard, that (different) text is
 * shown instead and nothing more is acknowledged here.
 */
async function turnOnOffload() {
  const s = status.value;
  if (!s || busy.value) return;
  const shownVersion = s.noticeVersion;
  busy.value = true;
  errorMsg.value = '';
  try {
    const next = await client.fleet.setWorkflowEventsOptIn(true);
    status.value = next;
    if (next.noticeFromHub || next.noticeNeedsDashboard || !next.noticeAckRequired) return;
    const acked = await client.fleet.mlAckNotice(shownVersion, ACTION_TURN_ON);
    status.value = acked;
    noticeChanged.value = !!acked.noticeChanged;
  } catch (err) {
    if (isPolicyChanged(err)) {
      noticeChanged.value = true;
      await refresh();
    } else {
      errorMsg.value = err instanceof Error ? err.message : String(err);
    }
  } finally {
    busy.value = false;
  }
}

function onNoticeAction(action: string) {
  if (action === ACTION_TURN_ON) void turnOnOffload();
  else if (action === ACTION_NOT_NOW) dismissed.value = true;
  else if (action === ACTION_READ) void acknowledge();
}

async function setOptIn(optedIn: boolean) {
  if (busy.value) return;
  busy.value = true;
  errorMsg.value = '';
  try {
    status.value = await client.fleet.setWorkflowEventsOptIn(optedIn);
    noticeChanged.value = false;
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busy.value = false;
  }
}

function onOptInChange(ev: Event) {
  void setOptIn((ev.target as HTMLInputElement).checked);
}
</script>

<template>
  <section v-if="visible && status" class="space-y-3" data-testid="cloud-ml-panel">
    <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">Cloud ML</h2>

    <p v-if="status.fleetError" class="text-[12px] text-ink-muted" data-testid="cloud-ml-fleet-error">
      Could not read your Cloud ML settings from Fleet, so nothing is sent: {{ status.fleetError }}
    </p>

    <template v-if="loaded">
      <dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-[12px]" data-testid="cloud-ml-fields">
        <dt class="text-ink-muted">Organization offload</dt>
        <dd data-testid="cloud-ml-offload">{{ status.orgOffloadEnabled ? 'On' : 'Off' }}</dd>
        <dt class="text-ink-muted">Organization policy</dt>
        <dd data-testid="cloud-ml-policy">{{ policyLabel(status.orgPolicy) }}</dd>
        <dt class="text-ink-muted">Your opt-in</dt>
        <dd data-testid="cloud-ml-optin-state">
          {{ status.userWorkflowEventsOptedIn ? 'Opted in' : 'Not opted in' }}
        </dd>
        <dt class="text-ink-muted">Notice</dt>
        <dd data-testid="cloud-ml-notice-status">{{ noticeLine }}</dd>
        <dt class="text-ink-muted">Sending</dt>
        <dd data-testid="cloud-ml-effective">
          {{ status.effective ? 'On — agent activity is sent to Fleet' : 'Off — nothing is sent' }}
        </dd>
        <dt class="text-ink-muted">Retention</dt>
        <dd data-testid="cloud-ml-retention">{{ status.retentionDays }} days</dd>
      </dl>

      <label v-if="showOptIn" class="flex items-start gap-2 text-[12px]" data-testid="cloud-ml-optin">
        <input
          type="checkbox"
          :checked="status.userWorkflowEventsOptedIn"
          :disabled="busy"
          data-testid="cloud-ml-optin-toggle"
          @change="onOptInChange"
        />
        <span>Share what the agent does in my harness sessions with my organization's Cloud ML.</span>
      </label>

      <div
        v-if="showNotice"
        class="space-y-2 rounded-sm border border-border-muted p-3 text-[12px]"
        data-testid="cloud-ml-notice"
      >
        <p v-if="noticeChanged" class="text-signal-warn" data-testid="cloud-ml-notice-changed">
          The notice changed since it was shown. Please read it again.
        </p>
        <!-- Fallback (no hub): counsel's rev-1 text, laid out from structured fields. -->
        <template v-if="localNotice">
          <h3 class="text-[13px] font-semibold" data-testid="cloud-ml-notice-heading">{{ localNotice.heading }}</h3>
          <p data-testid="cloud-ml-notice-intro">{{ localNotice.intro }}</p>
          <p v-for="s in localNotice.sections" :key="s.label" data-testid="cloud-ml-notice-section">
            <strong>{{ s.label }}</strong> {{ s.text }}
          </p>
          <p v-if="localNotice.policyText" data-testid="cloud-ml-notice-policy">{{ localNotice.policyText }}</p>
          <!-- Exclusions are part of the notice (Fleet's words): before any action button. -->
          <p v-if="localNotice.exclusionsText" data-testid="cloud-ml-exclusions">{{ localNotice.exclusionsText }}</p>
          <div class="flex gap-2">
            <button
              v-for="a in localNotice.actions"
              :key="a"
              type="button"
              class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="busy"
              data-testid="cloud-ml-notice-action"
              @click="onNoticeAction(a)"
            >
              {{ a }}
            </button>
          </div>
        </template>
        <!-- Hub: the item title is the heading; body_text verbatim below it. -->
        <template v-else>
          <h3 v-if="status.noticeTitle" class="text-[13px] font-semibold" data-testid="cloud-ml-notice-heading">
            {{ status.noticeTitle }}
          </h3>
          <!-- The served body_text is the only exclusions source here: nothing of our own. -->
          <p class="whitespace-pre-wrap" data-testid="cloud-ml-notice-text">{{ status.noticeText }}</p>
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="busy"
            data-testid="cloud-ml-ack"
            @click="acknowledge"
          >
            {{ ACTION_READ }}
          </button>
        </template>
      </div>

      <div
        v-if="needsDashboard"
        class="space-y-2 rounded-sm border border-border-muted p-3 text-[12px]"
        data-testid="cloud-ml-notice-dashboard"
      >
        <p data-testid="cloud-ml-notice-dashboard-text">{{ DASHBOARD_NOTICE }}</p>
        <template v-if="dashboardUrl">
          <p class="break-all text-ink-muted" data-testid="cloud-ml-notice-dashboard-url">{{ dashboardUrl }}</p>
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
            data-testid="cloud-ml-notice-dashboard-open"
            @click="openDashboard"
          >
            Open Fleet dashboard
          </button>
        </template>
      </div>

      <!-- No notice shown: Fleet's exclusions paragraph on its own. -->
      <p
        v-if="loaded && !showNotice && exclusionsSummary"
        class="text-[12px]"
        data-testid="cloud-ml-exclusions"
      >
        {{ exclusionsSummary }}
      </p>

      <p class="text-[12px] text-ink-muted" data-testid="cloud-ml-harness-line">{{ HARNESS_LINE }}</p>
    </template>

    <!-- Shipping status: empty until the ML shipper reports. -->
    <div class="text-[12px] text-ink-muted" data-testid="cloud-ml-shipping">
      <template v-if="status.shipping">
        <p data-testid="cloud-ml-shipping-last">
          Last batch: {{ status.shipping.lastBatchAt ? formatTime(status.shipping.lastBatchAt) : 'none yet' }}
        </p>
        <p data-testid="cloud-ml-shipping-counts">
          Accepted {{ status.shipping.accepted }}, duplicates {{ status.shipping.duplicates }},
          rejected {{ status.shipping.rejected }}
        </p>
        <p v-if="status.shipping.stopReason" data-testid="cloud-ml-shipping-stop">
          Last stop: {{ status.shipping.stopReason }}
        </p>
      </template>
    </div>

    <p v-if="errorMsg" class="text-[12px] text-signal-danger" data-testid="cloud-ml-error">{{ errorMsg }}</p>
  </section>
</template>
