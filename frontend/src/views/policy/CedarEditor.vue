<script setup lang="ts">
/**
 * CedarEditor — extends PolicyView with fleet team-bundle awareness.
 * (fleet-config-pull-01NDFSEX10 WP06)
 *
 * New behaviours on top of PolicyView:
 *   - Policies whose name starts with "fleet-team/" display a "team-managed"
 *     badge in the file list and editor header.
 *   - An "Override" button appears next to team-managed rules when:
 *       signedIn && capability('opa_custom_rego')
 *     Clicking it inserts a local forbid template into a new draft policy
 *     in the user's policy directory, pre-populated with the team_rule_id.
 *   - Config-pull status banner: shows the last applied bundle_id and error
 *     (if any) from the ConfigPoller. Gated by signedIn.
 *
 * Override semantics: a local policy file containing:
 *   forbid(principal, action, resource)
 *     when { context has team_rule_id && context.team_rule_id == "<id>" };
 * is written to the user's policy directory. Cedar's forbid-wins semantics
 * mean this local rule cancels the fleet-managed permit.
 *
 * This component is a drop-in replacement for PolicyView when fleet is active;
 * it wraps and extends the same functionality without duplicating policy logic.
 *
 * Usage: import and register in the router instead of PolicyView.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { signedIn, capability } from '@/lib/featureFlags';
import type { PolicyFileDetail, ParseError, FleetConfigPullStatusView, FleetIdentity } from '@/lib/types';
import BaseDialog from '@/components/ui/BaseDialog.vue';

const client = useHarnessClient();

// ── fleet config-pull status ───────────────────────────────────────────

/** Current config-pull poller status (fleet bundle state). */
const configPullStatus = ref<FleetConfigPullStatusView | null>(null);
/** True while loading config-pull status. */
const configPullLoading = ref(false);

async function loadConfigPullStatus() {
  if (!signedIn.value) return;
  configPullLoading.value = true;
  try {
    configPullStatus.value = await client.settings.fleetConfigPullStatus();
  } catch {
    // non-fatal: fleet might not be wired
    configPullStatus.value = null;
  } finally {
    configPullLoading.value = false;
  }
}

// ── fleet identity (for policy_admin role check) WP08 ─────────────────

/** null = not yet loaded; false = not signed in; FleetIdentity = loaded */
const fleetIdentity = ref<FleetIdentity | null | false>(null);

async function loadFleetIdentity() {
  if (!signedIn.value) {
    fleetIdentity.value = false;
    return;
  }
  try {
    const [signedInFlag, identity] = await Promise.all([
      client.settings.fleetSignedIn(),
      // best-effort: use cached identity if available
      client.settings.fleetRefreshIdentity().catch(() => null),
    ]);
    fleetIdentity.value = signedInFlag ? (identity ?? false) : false;
  } catch {
    fleetIdentity.value = false;
  }
}

/** True when the current user has the policy_admin role (FR-201). */
const isPolicyAdmin = computed<boolean>(() => {
  if (!fleetIdentity.value) return false;
  const id = fleetIdentity.value as FleetIdentity;
  return Array.isArray(id.roles) && id.roles.includes('policy_admin');
});

// ── publish-to-team state (WP08) ──────────────────────────────────────

const publishPreviewOpen = ref(false);
const publishing = ref(false);
const publishError = ref<string | null>(null);

function openPublishPreview() {
  if (!selectedFile.value) return;
  publishError.value = null;
  publishPreviewOpen.value = true;
}

async function confirmPublishToTeam() {
  if (!selectedFile.value) return;
  publishing.value = true;
  publishError.value = null;
  try {
    await client.cedarPublish.publishToTeam(
      selectedFile.value.name,
      editorSource.value,
    );
    publishPreviewOpen.value = false;
    showToast(`Policy "${selectedFile.value.name}" published to team.`);
  } catch (err) {
    publishError.value = err instanceof Error ? err.message : String(err);
  } finally {
    publishing.value = false;
  }
}

// ── policy list (mirrors PolicyView) ──────────────────────────────────

const policyFiles = ref<PolicyFileDetail[]>([]);
const selectedFile = ref<PolicyFileDetail | null>(null);
const editorSource = ref('');
const listLoading = ref(false);
const listError = ref<string | null>(null);
const saving = ref(false);
const deleting = ref(false);
const parseErrors = ref<ParseError[]>([]);
const parseOK = ref<boolean | null>(null);
const opError = ref<string | null>(null);
const confirmDelete = ref(false);
const creatingNew = ref(false);
const newPolicyName = ref('');
const newNameError = ref<string | null>(null);
const toast = ref<string | null>(null);

let validateTimer: ReturnType<typeof setTimeout> | null = null;

// ── computed ──────────────────────────────────────────────────────────

const hasParseErrors = computed(() => parseErrors.value.length > 0);
const isDirty = computed(
  () => selectedFile.value !== null && editorSource.value !== selectedFile.value.source,
);
const isReadOnly = computed(() => selectedFile.value?.read_only === true);
const isEmbedded = computed(() => selectedFile.value?.embedded === true);

/** True when the selected file is a fleet-managed team rule. */
const isTeamManaged = computed(
  () => selectedFile.value?.name?.startsWith('fleet-team/') ?? false,
);

/** True when the Override button should be shown for the selected file. */
const canOverride = computed(
  () => isTeamManaged.value && signedIn.value && capability('opa_custom_rego'),
);

/** The team_rule_id extracted from the selected file name (fleet-team/<id>). */
const teamRuleID = computed(() => {
  const name = selectedFile.value?.name ?? '';
  const prefix = 'fleet-team/';
  if (name.startsWith(prefix)) return name.slice(prefix.length);
  return '';
});

/** True when a config-pull error is present. */
const hasConfigPullError = computed(
  () => signedIn.value && (configPullStatus.value?.lastError ?? '') !== '',
);

// ── lifecycle ──────────────────────────────────────────────────────────

onMounted(async () => {
  await Promise.all([loadFileList(), loadConfigPullStatus(), loadFleetIdentity()]);
});

// ── helpers ────────────────────────────────────────────────────────────

async function loadFileList() {
  listLoading.value = true;
  listError.value = null;
  try {
    const files = await client.cedarPolicy.listPolicies();
    policyFiles.value = (files as PolicyFileDetail[]).map((f) => ({
      ...f,
      source: '',
      read_only: f.embedded ?? false,
    }));
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
  } finally {
    listLoading.value = false;
  }
}

async function selectFile(name: string) {
  opError.value = null;
  parseErrors.value = [];
  parseOK.value = null;
  try {
    const detail = await client.cedarPolicy.getPolicy(name);
    selectedFile.value = detail;
    editorSource.value = detail.source;
    triggerDebouncedValidate(detail.source);
  } catch (err) {
    opError.value = err instanceof Error ? err.message : String(err);
  }
}

function onEditorInput(e: Event) {
  const val = (e.target as HTMLTextAreaElement).value;
  editorSource.value = val;
  triggerDebouncedValidate(val);
}

function triggerDebouncedValidate(source: string) {
  if (validateTimer !== null) clearTimeout(validateTimer);
  parseOK.value = null;
  validateTimer = setTimeout(() => void runValidate(source), 500);
}

async function runValidate(source: string) {
  try {
    const result = await client.cedarPolicy.validatePolicy(source);
    parseOK.value = result.ok;
    parseErrors.value = result.ok ? [] : (result.errors ?? []);
  } catch {
    parseOK.value = null;
    parseErrors.value = [];
  }
}

async function handleSave() {
  if (!selectedFile.value || isReadOnly.value) return;
  saving.value = true;
  opError.value = null;
  try {
    const result = await client.cedarPolicy.savePolicy(selectedFile.value.name, editorSource.value);
    if (result.ok) {
      selectedFile.value = { ...selectedFile.value, source: editorSource.value, parse_ok: true };
      parseErrors.value = [];
      parseOK.value = true;
      showToast(`Saved: ${selectedFile.value.name}`);
      await loadFileList();
    } else {
      parseErrors.value = result.errors ?? [];
      parseOK.value = false;
      opError.value = 'Parse failed — file not written';
    }
  } catch (err) {
    opError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

function startDelete() {
  if (!selectedFile.value || isReadOnly.value) return;
  confirmDelete.value = true;
}

async function confirmDeletePolicy() {
  if (!selectedFile.value) return;
  const name = selectedFile.value.name;
  confirmDelete.value = false;
  deleting.value = true;
  opError.value = null;
  try {
    await client.cedarPolicy.deletePolicy(name);
    selectedFile.value = null;
    editorSource.value = '';
    parseErrors.value = [];
    parseOK.value = null;
    showToast(`Deleted: ${name}`);
    await loadFileList();
  } catch (err) {
    opError.value = err instanceof Error ? err.message : String(err);
  } finally {
    deleting.value = false;
  }
}

function cancelDelete() {
  confirmDelete.value = false;
}

function startNewPolicy() {
  creatingNew.value = true;
  newPolicyName.value = '';
  newNameError.value = null;
  selectedFile.value = null;
  editorSource.value = 'permit(principal, action, resource);';
  parseErrors.value = [];
  parseOK.value = null;
  triggerDebouncedValidate(editorSource.value);
}

async function saveNewPolicy() {
  const rawName = newPolicyName.value.trim();
  if (!rawName.endsWith('.cedar')) {
    newNameError.value = 'Name must end with .cedar';
    return;
  }
  if (!/^[a-zA-Z0-9._-]+\.cedar$/.test(rawName)) {
    newNameError.value = 'Only letters, digits, hyphens, underscores, dots followed by .cedar';
    return;
  }
  newNameError.value = null;
  saving.value = true;
  opError.value = null;
  try {
    const result = await client.cedarPolicy.savePolicy(rawName, editorSource.value);
    if (result.ok) {
      creatingNew.value = false;
      showToast(`Created: ${rawName}`);
      await loadFileList();
      await selectFile(rawName);
    } else {
      parseErrors.value = result.errors ?? [];
      parseOK.value = false;
      opError.value = 'Parse failed — file not created';
    }
  } catch (err) {
    opError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

function cancelNew() {
  creatingNew.value = false;
  selectedFile.value = null;
  editorSource.value = '';
  parseErrors.value = [];
  parseOK.value = null;
}

async function handleReload() {
  opError.value = null;
  try {
    await client.cedarPolicy.reloadPolicies();
    await loadFileList();
    showToast('Policies reloaded');
  } catch (err) {
    opError.value = err instanceof Error ? err.message : String(err);
  }
}

/**
 * handleOverride creates a new local forbid policy pre-populated with a
 * `context.team_rule_id == "<id>"` guard. This local forbid cancels the
 * fleet-managed permit rule for the given team_rule_id.
 *
 * The new policy is scaffolded in "create new" mode so the user can review
 * before saving. The filename defaults to `override-<team_rule_id>.cedar`.
 */
function handleOverride() {
  const ruleID = teamRuleID.value;
  if (!ruleID) return;

  creatingNew.value = true;
  newPolicyName.value = `override-${ruleID}.cedar`;
  newNameError.value = null;
  selectedFile.value = null;
  editorSource.value =
    `// Override for fleet team rule: ${ruleID}\n` +
    `// This local forbid cancels the fleet-managed permit for this rule ID.\n` +
    `forbid(principal, action, resource)\n` +
    `  when { context has team_rule_id && context.team_rule_id == "${ruleID}" };\n`;
  parseErrors.value = [];
  parseOK.value = null;
  triggerDebouncedValidate(editorSource.value);
  showToast(`Override template created for rule: ${ruleID}`);
}

function showToast(msg: string) {
  toast.value = msg;
  setTimeout(() => { toast.value = null; }, 3000);
}
</script>

<template>
  <div class="cedar-editor">
    <!-- Toast notification -->
    <div v-if="toast" class="cedar-editor__toast" role="status" data-testid="cedar-toast">
      {{ toast }}
    </div>

    <!-- Publish to team diff-preview dialog (WP08 / FR-202) -->
    <BaseDialog
      :open="publishPreviewOpen"
      title="Publish Cedar rule to team"
      panel-class="w-full max-w-lg rounded-md border border-border-muted bg-surface-1 p-5 shadow-xl"
      @close="publishPreviewOpen = false"
    >
      <div data-testid="cedar-publish-preview-dialog">
        <h2 class="font-ui text-base font-semibold text-ink">
          Publish to team
        </h2>
        <p class="mt-1 font-ui text-xs text-ink-muted">
          Publishing <strong>{{ selectedFile?.name }}</strong> will push this rule to the fleet
          team bundle. All team members will receive it on next config-pull cycle.
        </p>
        <!-- Diff preview — shows current source so admin can confirm -->
        <div class="mt-3 rounded-sm border border-border-muted bg-surface-0 p-3 max-h-48 overflow-y-auto">
          <pre class="font-mono text-xs text-ink whitespace-pre-wrap">{{ editorSource }}</pre>
        </div>
        <div
          v-if="publishError"
          class="mt-2 rounded-sm border border-signal-danger bg-signal-danger-soft px-3 py-2 font-ui text-xs text-signal-danger"
          role="alert"
          data-testid="cedar-publish-error"
        >
          {{ publishError }}
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="cedar-editor__btn"
            :disabled="publishing"
            data-testid="cedar-publish-preview-cancel"
            @click="publishPreviewOpen = false"
          >
            Cancel
          </button>
          <button
            type="button"
            class="cedar-editor__btn cedar-editor__btn--primary"
            :disabled="publishing"
            data-testid="cedar-publish-preview-confirm"
            @click="confirmPublishToTeam"
          >
            {{ publishing ? 'Publishing…' : 'Publish to team' }}
          </button>
        </div>
      </div>
    </BaseDialog>

    <!-- Delete confirm dialog -->
    <div
      v-if="confirmDelete"
      class="cedar-editor__dialog-overlay"
      data-testid="cedar-delete-confirm"
    >
      <div class="cedar-editor__dialog">
        <p class="cedar-editor__dialog-msg">
          Delete <strong>{{ selectedFile?.name }}</strong>? This cannot be undone.
        </p>
        <div class="cedar-editor__dialog-btns">
          <button
            type="button"
            class="cedar-editor__btn cedar-editor__btn--danger"
            data-testid="cedar-delete-confirm-yes"
            @click="confirmDeletePolicy"
          >
            Delete
          </button>
          <button
            type="button"
            class="cedar-editor__btn"
            data-testid="cedar-delete-confirm-cancel"
            @click="cancelDelete"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>

    <header class="cedar-editor__head">
      <div class="cedar-editor__title">
        <span class="cedar-editor__section">POLICY</span>
      </div>
      <h1 class="cedar-editor__heading">Cedar policy editor</h1>
      <p class="cedar-editor__sub">
        Edit Cedar policies under <code>&lt;DataDir&gt;/policy/</code>.
        Fleet-managed rules carry a <em>team-managed</em> badge.
        Override them locally via the Override button (requires
        <code>opa_custom_rego</code> capability).
      </p>

      <!-- Config-pull status banner (fleet only) -->
      <div
        v-if="signedIn && configPullStatus"
        class="cedar-editor__fleet-banner"
        :class="{ 'cedar-editor__fleet-banner--error': hasConfigPullError }"
        data-testid="cedar-fleet-status"
      >
        <template v-if="hasConfigPullError">
          Fleet config error: {{ configPullStatus.lastError }}
        </template>
        <template v-else-if="configPullStatus.lastAppliedId > 0">
          Fleet bundle #{{ configPullStatus.lastAppliedId }} applied
          <template v-if="configPullStatus.lastAppliedAt">
            at {{ configPullStatus.lastAppliedAt }}
          </template>
        </template>
        <template v-else>
          Fleet config: waiting for first bundle…
        </template>
      </div>
    </header>

    <div class="cedar-editor__layout">
      <!-- ── Left: file list ─────────────────────────────────────── -->
      <aside class="cedar-editor__sidebar">
        <div class="cedar-editor__sidebar-head">
          <h2 class="cedar-editor__h2">Policies</h2>
          <div class="cedar-editor__sidebar-actions">
            <button
              type="button"
              class="cedar-editor__btn cedar-editor__btn--sm"
              data-testid="cedar-new"
              @click="startNewPolicy"
            >
              + New
            </button>
            <button
              type="button"
              class="cedar-editor__btn cedar-editor__btn--sm"
              data-testid="cedar-reload"
              @click="handleReload"
            >
              ↺ Reload
            </button>
          </div>
        </div>

        <div v-if="listLoading" class="cedar-editor__empty">Loading…</div>
        <div v-else-if="listError" class="cedar-editor__err-msg" role="alert">
          {{ listError }}
        </div>
        <ul v-else class="cedar-editor__file-list" data-testid="cedar-file-list">
          <li
            v-for="f in policyFiles"
            :key="f.name"
            class="cedar-editor__file"
            :class="{
              'cedar-editor__file--active': selectedFile?.name === f.name,
              'cedar-editor__file--team': f.name?.startsWith('fleet-team/'),
            }"
            :data-testid="`cedar-file-${f.name}`"
            @click="selectFile(f.name)"
          >
            <span class="cedar-editor__file-name">{{ f.name }}</span>
            <span
              v-if="f.name?.startsWith('fleet-team/')"
              class="cedar-editor__tag cedar-editor__tag--team"
              data-testid="cedar-team-badge"
            >
              team-managed
            </span>
            <span v-else-if="f.embedded" class="cedar-editor__tag">embedded</span>
            <span
              class="cedar-editor__file-status"
              :class="f.parse_ok ? 'cedar-editor__ok' : 'cedar-editor__err'"
            >
              {{ f.parse_ok ? '✓' : '✕' }}
            </span>
          </li>
          <li v-if="policyFiles.length === 0" class="cedar-editor__empty">
            No policy files loaded.
          </li>
        </ul>
      </aside>

      <!-- ── Right: editor pane ──────────────────────────────────── -->
      <section class="cedar-editor__editor-pane">
        <!-- New policy name input -->
        <div v-if="creatingNew" class="cedar-editor__new-bar" data-testid="cedar-new-bar">
          <input
            v-model="newPolicyName"
            type="text"
            aria-label="New policy file name"
            class="cedar-editor__name-input"
            placeholder="my-policy.cedar"
            data-testid="cedar-new-name"
            @keydown.enter="saveNewPolicy"
            @keydown.escape="cancelNew"
          />
          <span v-if="newNameError" class="cedar-editor__err-msg">{{ newNameError }}</span>
          <div class="cedar-editor__new-actions">
            <button
              type="button"
              class="cedar-editor__btn cedar-editor__btn--primary"
              :disabled="saving"
              data-testid="cedar-create-save"
              @click="saveNewPolicy"
            >
              {{ saving ? 'Saving…' : 'Create' }}
            </button>
            <button
              type="button"
              class="cedar-editor__btn"
              data-testid="cedar-create-cancel"
              @click="cancelNew"
            >
              Cancel
            </button>
          </div>
        </div>

        <!-- File header when editing an existing file -->
        <div
          v-else-if="selectedFile"
          class="cedar-editor__editor-head"
          data-testid="cedar-editor-head"
        >
          <div class="cedar-editor__editor-title">
            <span class="cedar-editor__file-name">{{ selectedFile.name }}</span>
            <span
              v-if="isTeamManaged"
              class="cedar-editor__tag cedar-editor__tag--team"
              data-testid="cedar-team-managed-badge"
            >
              team-managed
            </span>
            <span
              v-else-if="isReadOnly"
              class="cedar-editor__tag cedar-editor__tag--readonly"
              data-testid="cedar-readonly-badge"
            >
              read-only
            </span>
            <span v-else-if="isEmbedded" class="cedar-editor__tag">embedded</span>
          </div>
          <div class="cedar-editor__editor-actions">
            <!-- Override button: only for team-managed rules with opa_custom_rego cap -->
            <button
              v-if="canOverride"
              type="button"
              class="cedar-editor__btn cedar-editor__btn--override"
              data-testid="cedar-override-btn"
              @click="handleOverride"
            >
              Override
            </button>
            <!-- Publish to team button: only for non-team-managed files with policy_admin role (FR-201/WP08) -->
            <button
              v-if="isPolicyAdmin && selectedFile && !isTeamManaged"
              type="button"
              class="cedar-editor__btn cedar-editor__btn--publish"
              data-testid="cedar-publish-to-team-btn"
              @click="openPublishPreview"
            >
              Publish to team
            </button>
            <!-- Parse status pill -->
            <span
              v-if="parseOK === true"
              class="cedar-editor__parse-ok"
              data-testid="cedar-parse-ok"
            >
              ✓ Parse OK
            </span>
            <span
              v-else-if="parseOK === false"
              class="cedar-editor__parse-err"
              data-testid="cedar-parse-error"
            >
              ✕ Parse error
            </span>
            <button
              v-if="!isReadOnly && !isTeamManaged"
              type="button"
              class="cedar-editor__btn cedar-editor__btn--primary"
              :disabled="saving || !isDirty"
              data-testid="cedar-save"
              @click="handleSave"
            >
              {{ saving ? 'Saving…' : 'Save' }}
            </button>
            <button
              v-if="!isReadOnly && !isTeamManaged"
              type="button"
              class="cedar-editor__btn cedar-editor__btn--danger"
              :disabled="deleting"
              data-testid="cedar-delete"
              @click="startDelete"
            >
              Delete
            </button>
          </div>
        </div>

        <!-- Error banner -->
        <div
          v-if="opError"
          class="cedar-editor__banner cedar-editor__banner--error"
          role="alert"
          data-testid="cedar-op-error"
        >
          {{ opError }}
        </div>

        <!-- Parse error details -->
        <div
          v-if="hasParseErrors"
          class="cedar-editor__banner cedar-editor__banner--parse"
          data-testid="cedar-parse-errors"
        >
          <div
            v-for="(e, idx) in parseErrors"
            :key="idx"
            class="cedar-editor__parse-error-row"
          >
            <span v-if="e.line > 0" class="cedar-editor__parse-loc">
              Line {{ e.line }}<template v-if="e.column > 0">, col {{ e.column }}</template>:
            </span>
            {{ e.message }}
          </div>
        </div>

        <!-- Editor (textarea with line-number gutter) -->
        <div v-if="selectedFile || creatingNew" class="cedar-editor__editor-wrap">
          <textarea
            class="cedar-editor__editor"
            :class="{ 'cedar-editor__editor--error': hasParseErrors }"
            :value="editorSource"
            :readonly="(isReadOnly || isTeamManaged) && !creatingNew"
            :aria-label="creatingNew ? 'New policy source' : `Edit ${selectedFile?.name}`"
            data-testid="cedar-editor-textarea"
            spellcheck="false"
            autocorrect="off"
            autocomplete="off"
            @input="onEditorInput"
          />
        </div>

        <!-- Empty state -->
        <div
          v-else
          class="cedar-editor__editor-empty"
          data-testid="cedar-editor-empty"
        >
          Select a policy to edit, or click <strong>+ New</strong> to create one.
          <template v-if="isTeamManaged">
            Fleet-managed rules are read-only — use Override to create a local exception.
          </template>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
/* Scoped styles mirror PolicyView with cedar-editor prefix for encapsulation */
.cedar-editor {
  display: flex;
  flex-direction: column;
  height: 100%;
  font-family: var(--font-ui);
}

.cedar-editor__toast {
  position: fixed;
  bottom: 1.5rem;
  right: 1.5rem;
  background: var(--surface-3);
  color: var(--ink);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0.5rem 1rem;
  font-size: 0.8125rem;
  z-index: 9999;
  box-shadow: 0 2px 8px var(--modal-shadow);
}

.cedar-editor__dialog-overlay {
  position: fixed;
  inset: 0;
  background: var(--modal-overlay);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.cedar-editor__dialog {
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 1.5rem;
  max-width: 380px;
  width: 90%;
}

.cedar-editor__dialog-msg {
  margin: 0 0 1rem;
  font-size: 0.875rem;
}

.cedar-editor__dialog-btns {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
}

.cedar-editor__head {
  padding: 0.75rem 1rem 0.5rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
}

.cedar-editor__title {
  font-size: 0.6875rem;
  color: var(--ink-muted);
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.cedar-editor__heading {
  font-size: 1.125rem;
  font-weight: 600;
  margin: 0.25rem 0 0;
}

.cedar-editor__sub {
  margin: 0.125rem 0 0;
  color: var(--ink-muted);
  font-size: 0.8125rem;
}

.cedar-editor__fleet-banner {
  margin-top: 0.5rem;
  padding: 0.375rem 0.625rem;
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  background: color-mix(in srgb, var(--accent) 8%, transparent);
  color: var(--ink-muted);
  border: 1px solid color-mix(in srgb, var(--accent) 20%, transparent);
}

.cedar-editor__fleet-banner--error {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  color: var(--danger);
  border-color: color-mix(in srgb, var(--danger) 20%, transparent);
}

.cedar-editor__layout {
  display: flex;
  flex: 1;
  overflow: hidden;
}

/* ── Sidebar ── */
.cedar-editor__sidebar {
  width: 220px;
  flex-shrink: 0;
  border-right: 1px solid var(--border-muted);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.cedar-editor__sidebar-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
}

.cedar-editor__h2 {
  font-size: 0.75rem;
  font-weight: 600;
  margin: 0;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--ink-muted);
}

.cedar-editor__sidebar-actions {
  display: flex;
  gap: 0.25rem;
}

.cedar-editor__file-list {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  flex: 1;
}

.cedar-editor__file {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.375rem 0.75rem;
  cursor: pointer;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--border-muted);
  transition: background 0.1s;
}

.cedar-editor__file:hover {
  background: var(--surface-2);
}

.cedar-editor__file--active {
  background: var(--surface-2);
  font-weight: 600;
}

.cedar-editor__file--team {
  border-left: 2px solid var(--accent);
}

.cedar-editor__file-name {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cedar-editor__file-status {
  flex-shrink: 0;
  font-size: 0.6875rem;
  font-weight: 700;
}

.cedar-editor__ok {
  color: var(--ok);
}

.cedar-editor__err {
  color: var(--danger);
  font-weight: 600;
  font-size: 0.75rem;
}

.cedar-editor__tag {
  font-size: 0.5625rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--ink-muted);
  padding: 0 0.2rem;
  border: 1px solid var(--border-muted);
  border-radius: 2px;
  flex-shrink: 0;
}

.cedar-editor__tag--readonly {
  color: var(--warn);
  border-color: var(--warn);
}

.cedar-editor__tag--team {
  color: var(--accent);
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 8%, transparent);
}

.cedar-editor__empty {
  padding: 0.75rem;
  color: var(--ink-muted);
  font-size: 0.8125rem;
}

/* ── Editor pane ── */
.cedar-editor__editor-pane {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.cedar-editor__editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.cedar-editor__editor-title {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  min-width: 0;
}

.cedar-editor__editor-actions {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  flex-shrink: 0;
}

.cedar-editor__parse-ok {
  font-size: 0.75rem;
  color: var(--ok);
  font-weight: 600;
}

.cedar-editor__parse-err {
  font-size: 0.75rem;
  color: var(--danger);
  font-weight: 600;
}

.cedar-editor__banner {
  padding: 0.5rem 0.75rem;
  font-size: 0.8125rem;
  flex-shrink: 0;
}

.cedar-editor__banner--error {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  color: var(--danger);
  border-bottom: 1px solid color-mix(in srgb, var(--danger) 20%, transparent);
}

.cedar-editor__banner--parse {
  background: color-mix(in srgb, var(--warn) 10%, transparent);
  color: var(--warn-ink, var(--ink));
  border-bottom: 1px solid color-mix(in srgb, var(--warn) 20%, transparent);
}

.cedar-editor__parse-error-row {
  display: flex;
  gap: 0.375rem;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.cedar-editor__parse-loc {
  color: var(--ink-muted);
  flex-shrink: 0;
}

.cedar-editor__err-msg {
  color: var(--danger);
  font-size: 0.75rem;
}

.cedar-editor__new-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
  flex-wrap: wrap;
}

.cedar-editor__name-input {
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  color: var(--ink);
  flex: 1;
  min-width: 180px;
}

.cedar-editor__name-input:focus {
  outline: 1px solid var(--accent);
}

.cedar-editor__new-actions {
  display: flex;
  gap: 0.375rem;
}

.cedar-editor__editor-wrap {
  flex: 1;
  overflow: hidden;
  display: flex;
  position: relative;
}

.cedar-editor__editor {
  flex: 1;
  width: 100%;
  padding: 0.75rem;
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  line-height: 1.5;
  border: none;
  outline: none;
  resize: none;
  background: var(--surface-1);
  color: var(--ink);
  white-space: pre;
  overflow: auto;
  tab-size: 2;
}

.cedar-editor__editor:read-only {
  background: var(--surface-2);
  cursor: default;
}

.cedar-editor__editor--error {
  border-top: 2px solid var(--danger);
}

.cedar-editor__editor-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--ink-muted);
  font-size: 0.875rem;
  padding: 2rem;
  text-align: center;
}

/* ── Shared button styles ── */
.cedar-editor__btn {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0.25rem 0.625rem;
  background: var(--surface-2);
  color: var(--ink);
  cursor: pointer;
  font-size: 0.75rem;
  font-family: var(--font-ui);
  transition: background 0.1s;
}

.cedar-editor__btn:hover:not(:disabled) {
  background: var(--surface-3);
}

.cedar-editor__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.cedar-editor__btn--sm {
  padding: 0.125rem 0.5rem;
  font-size: 0.6875rem;
}

.cedar-editor__btn--primary {
  background: var(--accent);
  color: var(--accent-ink);
  border-color: var(--accent);
}

.cedar-editor__btn--primary:hover:not(:disabled) {
  filter: brightness(1.1);
}

.cedar-editor__btn--danger {
  color: var(--danger);
  border-color: var(--danger);
}

.cedar-editor__btn--danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
}

.cedar-editor__btn--override {
  color: var(--accent);
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 8%, transparent);
}

.cedar-editor__btn--override:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 15%, transparent);
}

.cedar-editor__btn--publish {
  background: color-mix(in srgb, var(--accent) 10%, transparent);
  color: var(--accent);
  border-color: color-mix(in srgb, var(--accent) 20%, transparent);
}

.cedar-editor__btn--publish:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 18%, transparent);
}
</style>
