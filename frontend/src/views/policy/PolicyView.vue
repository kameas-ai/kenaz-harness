<script setup lang="ts">
/**
 * PolicyView — Cedar policy bundle editor + decision audit panel.
 * (cedar-policy-editor-ui-01KQ8TD6 WP02; decisions panel added by
 * consent-surfaces-truth-01PMTR01 WP06)
 *
 * Two sub-views (data-testid="policy-tab-editor" / "policy-tab-decisions"),
 * both reachable at the existing /policy route (SettingsTabs' "Policy" nav
 * entry — unchanged by WP06):
 *
 *   Files (default):
 *     Left:  file list with "New" button + metadata
 *     Right: editor pane with Save / Delete / Validate status
 *
 *   Decisions:
 *     A PULL-based list over CedarPolicy_RecentDecisions — the user opens
 *     the tab or clicks Refresh; there is no push topic (spec §4 non-goal).
 *     Meaningful only once WP05's engine hoist landed: before it, this
 *     view's own engine never had Evaluate called on it, so the ring was
 *     structurally always empty regardless of what this component did.
 *
 * Validation is debounced 500ms via ValidatePolicy (parse-only, no disk I/O).
 * Errors render inline with line/column. Default policies are read-only.
 * Delete requires a confirmation dialog.
 *
 * CodeMirror 6 is NOT available in this package.json; we use a monospace
 * <textarea> with a line-number gutter via CSS counter.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import type { PolicyFileDetail, ParseError, PolicyDecision } from '@/lib/types';

const client = useHarnessClient();

// served-mode-is-a-real-mode-01PMZ707 WP03, spec.md §5.3. All CedarPolicy_*
// and Policy_* RPCs this view calls are unrouted in served mode; verified
// no serve dispatch case exists for any of them.
const servedMode = useServedMode();

// ── state ─────────────────────────────────────────────────────────────

/** All policy files from ListPolicies (no source included). */
const policyFiles = ref<PolicyFileDetail[]>([]);
/** Currently selected/open file detail (includes source). */
const selectedFile = ref<PolicyFileDetail | null>(null);
/** Source text in the editor (may differ from selectedFile.source). */
const editorSource = ref('');
/** True while the file list is loading. */
const listLoading = ref(false);
/** Error from list load. */
const listError = ref<string | null>(null);
/** True while Save is in progress. */
const saving = ref(false);
/** True while Delete is in progress. */
const deleting = ref(false);
/** Parse errors from debounced validation or failed save. */
const parseErrors = ref<ParseError[]>([]);
/** True when the last validate call returned OK. */
const parseOK = ref<boolean | null>(null);
/** Banner-level error from Save/Delete/Load operations. */
const opError = ref<string | null>(null);
/** True when showing the delete-confirm dialog. */
const confirmDelete = ref(false);
/** True when adding a new policy (name input shown). */
const creatingNew = ref(false);
/** Name being typed for a new policy. */
const newPolicyName = ref('');
/** New policy name error. */
const newNameError = ref<string | null>(null);
/** Toast message (success). */
const toast = ref<string | null>(null);
/** Whether the policy editor UI is enabled (feature flag). */
const editorEnabled = ref(true);

// ── decisions panel (consent-surfaces-truth-01PMTR01 WP06) ─────────────
// Pull-based: no push topic, no policy:event contract (spec §4 non-goal).
// The user opens the tab or clicks Refresh; nothing streams in on its
// own. Reachable at the same /policy route SettingsTabs already routes
// to — see the module doc above.
type PolicyTab = 'editor' | 'decisions';
/** Which sub-view of the (already app-shell-reachable) /policy page is showing. */
const activeTab = ref<PolicyTab>('editor');
/** Recent gate decisions, newest first (server-ordered; see engine.RecentDecisions). */
const decisions = ref<PolicyDecision[]>([]);
/** True while a decisions fetch is in flight. */
const decisionsLoading = ref(false);
/** Error from the last decisions fetch. */
const decisionsError = ref<string | null>(null);
/** True once the decisions tab has been opened at least once (lazy load). */
const decisionsLoaded = ref(false);
/** How many rows to request from RecentDecisions. */
const DECISIONS_LIMIT = 100;

// ── debounce handle ───────────────────────────────────────────────────
let validateTimer: ReturnType<typeof setTimeout> | null = null;

// ── computed ──────────────────────────────────────────────────────────

const hasParseErrors = computed(() => parseErrors.value.length > 0);
const isDirty = computed(
  () => selectedFile.value !== null && editorSource.value !== selectedFile.value.source,
);
const isReadOnly = computed(() => selectedFile.value?.read_only === true);
const isEmbedded = computed(() => selectedFile.value?.embedded === true);

// ── lifecycle ─────────────────────────────────────────────────────────

onMounted(async () => {
  // Served mode: the whole view renders NotAvailableInServedMode instead.
  if (servedMode.value) return;
  // Read policyEditorEnabled from AppInfo.
  try {
    const info = await client.appInfo();
    if (info.policyEditorEnabled === false) {
      editorEnabled.value = false;
      return;
    }
  } catch {
    // AppInfo failure is non-fatal; proceed with editor enabled.
  }
  await loadFileList();
});

// ── helpers ────────────────────────────────────────────────────────────

async function loadFileList() {
  listLoading.value = true;
  listError.value = null;
  try {
    const files = await client.cedarPolicy.listPolicies();
    // listPolicies returns PolicyFile[] (no source); cast to PolicyFileDetail[]
    // with empty source so the type satisfies the list.
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

// ── decisions panel ───────────────────────────────────────────────────

/**
 * loadDecisions pulls the current RecentDecisions ring — a live gate's
 * denial (memory write, workflow save, bash, tools, recipe spawn, …)
 * shows up here because WP05 shares one *cedar.Engine across every gate
 * site AND the policy view, so the ring this reads is fed by real
 * Evaluate calls, not a private engine nobody ever evaluated against.
 */
async function loadDecisions() {
  decisionsLoading.value = true;
  decisionsError.value = null;
  try {
    decisions.value = await client.cedarPolicy.recentDecisions(DECISIONS_LIMIT);
  } catch (err) {
    decisionsError.value = err instanceof Error ? err.message : String(err);
  } finally {
    decisionsLoading.value = false;
    decisionsLoaded.value = true;
  }
}

/** Switch the visible sub-view; lazy-loads decisions on first visit. */
function selectTab(tab: PolicyTab) {
  activeTab.value = tab;
  if (tab === 'decisions' && !decisionsLoaded.value) {
    void loadDecisions();
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
    // Validate the loaded source immediately.
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
  validateTimer = setTimeout(() => {
    void runValidate(source);
  }, 500);
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
      opError.value = `Parse failed — file not written`;
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
  editorSource.value = `permit(principal, action, resource);`;
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
      opError.value = `Parse failed — file not created`;
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

function showToast(msg: string) {
  toast.value = msg;
  setTimeout(() => {
    toast.value = null;
  }, 3000);
}

// Watch editorSource when no file is selected (new policy mode).
watch(editorSource, (val) => {
  if (creatingNew.value) {
    triggerDebouncedValidate(val);
  }
});

/** CSS modifier class for a decision's outcome pill. */
function outcomeClass(outcome: PolicyDecision['outcome']): string {
  switch (outcome) {
    case 'deny':
      return 'policy-view__outcome--deny';
    case 'allow':
      return 'policy-view__outcome--allow';
    case 'not_applicable':
      return 'policy-view__outcome--na';
    default:
      return 'policy-view__outcome--unknown';
  }
}
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Policy editor"
    reason="Cedar policy files, validation and the decision audit trail run through CedarPolicy_*/Policy_* RPCs that are not routed in served mode."
  />
  <div v-else class="flex h-full min-h-0">
    <SettingsTabs />
    <div class="policy-view flex-1 min-w-0">
    <!-- Disabled state when feature flag is off -->
    <div v-if="!editorEnabled" class="policy-view__disabled" data-testid="policy-editor-disabled">
      <p>The advanced security policy editor is disabled. Contact your administrator to enable it.</p>
    </div>

    <template v-else>
      <!-- Toast notification -->
      <div v-if="toast" class="policy-view__toast" role="status" data-testid="policy-toast">
        {{ toast }}
      </div>

      <!-- Delete confirm dialog -->
      <div
        v-if="confirmDelete"
        class="policy-view__dialog-overlay"
        data-testid="policy-delete-confirm"
      >
        <div class="policy-view__dialog">
          <p class="policy-view__dialog-msg">
            Delete <strong>{{ selectedFile?.name }}</strong>? This cannot be undone.
          </p>
          <div class="policy-view__dialog-btns">
            <button
              type="button"
              class="policy-view__btn policy-view__btn--danger"
              data-testid="policy-delete-confirm-yes"
              @click="confirmDeletePolicy"
            >
              Delete
            </button>
            <button
              type="button"
              class="policy-view__btn"
              data-testid="policy-delete-confirm-cancel"
              @click="cancelDelete"
            >
              Cancel
            </button>
          </div>
        </div>
      </div>

      <header class="policy-view__head">
        <div class="policy-view__title">
          <span class="policy-view__section">POLICY</span>
        </div>
        <h1 class="policy-view__heading">Security policy (advanced)</h1>
        <p class="policy-view__sub">
          Edit security policies under <code>&lt;DataDir&gt;/policy/</code>.
          Embedded defaults are read-only. Changes take effect on the next tool call.
        </p>
        <nav class="policy-view__subnav" aria-label="Policy sections" data-testid="policy-subnav">
          <button
            type="button"
            class="policy-view__subtab"
            :class="{ 'policy-view__subtab--active': activeTab === 'editor' }"
            data-testid="policy-tab-editor"
            @click="selectTab('editor')"
          >
            Files
          </button>
          <button
            type="button"
            class="policy-view__subtab"
            :class="{ 'policy-view__subtab--active': activeTab === 'decisions' }"
            data-testid="policy-tab-decisions"
            @click="selectTab('decisions')"
          >
            Decisions
          </button>
        </nav>
      </header>

      <!-- ── Decisions panel: a denial says why (FR-007) ─────────────── -->
      <section
        v-if="activeTab === 'decisions'"
        class="policy-view__decisions"
        data-testid="policy-decisions-panel"
      >
        <div class="policy-view__decisions-head">
          <p class="policy-view__sub">
            Recent policy decisions across every gate (memory, tools, workflows,
            bash, recipe spawn, model selection, scheduled chat, session export,
            ACP), newest first.
          </p>
          <button
            type="button"
            class="policy-view__btn policy-view__btn--sm"
            data-testid="policy-decisions-refresh"
            :disabled="decisionsLoading"
            @click="loadDecisions"
          >
            {{ decisionsLoading ? 'Refreshing…' : '↺ Refresh' }}
          </button>
        </div>

        <div v-if="decisionsLoading && !decisionsLoaded" class="policy-view__empty">
          Loading…
        </div>
        <div v-else-if="decisionsError" class="policy-view__err-msg" role="alert">
          {{ decisionsError }}
        </div>
        <div
          v-else-if="decisions.length === 0"
          class="policy-view__empty"
          data-testid="policy-decisions-empty"
        >
          No decisions recorded yet. Trigger a gated action (a memory write, a
          bash command, a tool call) to see it here.
        </div>
        <ul v-else class="policy-view__decision-list" data-testid="policy-decision-list">
          <li
            v-for="(d, idx) in decisions"
            :key="`${d.action}-${d.evaluated_at}-${idx}`"
            class="policy-view__decision-row"
            :data-testid="`policy-decision-row-${idx}`"
          >
            <span class="policy-view__outcome" :class="outcomeClass(d.outcome)">
              {{ d.outcome }}
            </span>
            <span class="policy-view__decision-main">
              <span class="policy-view__decision-action">{{ d.action }}</span>
              <span v-if="d.resource" class="policy-view__decision-resource">{{ d.resource }}</span>
            </span>
            <span v-if="d.matched_policy" class="policy-view__decision-policy">
              {{ d.matched_policy }}
            </span>
            <span v-if="d.reason" class="policy-view__decision-reason">{{ d.reason }}</span>
            <span class="policy-view__decision-time">{{ d.evaluated_at }}</span>
          </li>
        </ul>
      </section>

      <div v-if="activeTab === 'editor'" class="policy-view__layout">
        <!-- ── Left: file list ─────────────────────────────────────── -->
        <aside class="policy-view__sidebar">
          <div class="policy-view__sidebar-head">
            <h2 class="policy-view__h2">Policies</h2>
            <div class="policy-view__sidebar-actions">
              <button
                type="button"
                class="policy-view__btn policy-view__btn--sm"
                data-testid="policy-new"
                @click="startNewPolicy"
              >
                + New
              </button>
              <button
                type="button"
                class="policy-view__btn policy-view__btn--sm"
                data-testid="policy-reload"
                @click="handleReload"
              >
                ↺ Reload
              </button>
            </div>
          </div>

          <div v-if="listLoading" class="policy-view__empty">Loading…</div>
          <div v-else-if="listError" class="policy-view__err-msg" role="alert">
            {{ listError }}
          </div>
          <ul v-else class="policy-view__file-list" data-testid="policy-file-list">
            <li
              v-for="f in policyFiles"
              :key="f.name"
              class="policy-view__file"
              :class="{ 'policy-view__file--active': selectedFile?.name === f.name }"
              :data-testid="`policy-file-${f.name}`"
              @click="selectFile(f.name)"
            >
              <span class="policy-view__file-name">{{ f.name }}</span>
              <span v-if="f.embedded" class="policy-view__tag">embedded</span>
              <span
                class="policy-view__file-status"
                :class="f.parse_ok ? 'policy-view__ok' : 'policy-view__err'"
              >
                {{ f.parse_ok ? '✓' : '✕' }}
              </span>
            </li>
            <li v-if="policyFiles.length === 0" class="policy-view__empty">
              No policy files loaded.
            </li>
          </ul>
        </aside>

        <!-- ── Right: editor pane ──────────────────────────────────── -->
        <section class="policy-view__editor-pane">
          <!-- New policy name input -->
          <div v-if="creatingNew" class="policy-view__new-bar" data-testid="policy-new-bar">
            <input
              v-model="newPolicyName"
              type="text"
              aria-label="New policy file name"
              class="policy-view__name-input"
              placeholder="my-policy.cedar"
              data-testid="policy-new-name"
              @keydown.enter="saveNewPolicy"
              @keydown.escape="cancelNew"
            />
            <span v-if="newNameError" class="policy-view__err-msg">{{ newNameError }}</span>
            <div class="policy-view__new-actions">
              <button
                type="button"
                class="policy-view__btn policy-view__btn--primary"
                :disabled="saving"
                data-testid="policy-create-save"
                @click="saveNewPolicy"
              >
                {{ saving ? 'Saving…' : 'Create' }}
              </button>
              <button
                type="button"
                class="policy-view__btn"
                data-testid="policy-create-cancel"
                @click="cancelNew"
              >
                Cancel
              </button>
            </div>
          </div>

          <!-- File header when editing an existing file -->
          <div
            v-else-if="selectedFile"
            class="policy-view__editor-head"
            data-testid="policy-editor-head"
          >
            <div class="policy-view__editor-title">
              <span class="policy-view__file-name">{{ selectedFile.name }}</span>
              <span
                v-if="isReadOnly"
                class="policy-view__tag policy-view__tag--readonly"
                data-testid="policy-readonly-badge"
              >
                read-only
              </span>
              <span
                v-if="isEmbedded"
                class="policy-view__tag"
              >
                embedded
              </span>
            </div>
            <div class="policy-view__editor-actions">
              <!-- Parse status pill -->
              <span
                v-if="parseOK === true"
                class="policy-view__parse-ok"
                data-testid="policy-parse-ok"
              >
                ✓ Parse OK
              </span>
              <span
                v-else-if="parseOK === false"
                class="policy-view__parse-err"
                data-testid="policy-parse-error"
              >
                ✕ Parse error
              </span>
              <button
                v-if="!isReadOnly"
                type="button"
                class="policy-view__btn policy-view__btn--primary"
                :disabled="saving || !isDirty"
                data-testid="policy-save"
                @click="handleSave"
              >
                {{ saving ? 'Saving…' : 'Save' }}
              </button>
              <button
                v-if="!isReadOnly"
                type="button"
                class="policy-view__btn policy-view__btn--danger"
                :disabled="deleting"
                data-testid="policy-delete"
                @click="startDelete"
              >
                Delete
              </button>
            </div>
          </div>

          <!-- Error banner -->
          <div
            v-if="opError"
            class="policy-view__banner policy-view__banner--error"
            role="alert"
            data-testid="policy-op-error"
          >
            {{ opError }}
          </div>

          <!-- Parse error details -->
          <div
            v-if="hasParseErrors"
            class="policy-view__banner policy-view__banner--parse"
            data-testid="policy-parse-errors"
          >
            <div
              v-for="(e, idx) in parseErrors"
              :key="idx"
              class="policy-view__parse-error-row"
            >
              <span v-if="e.line > 0" class="policy-view__parse-loc">
                Line {{ e.line }}<template v-if="e.column > 0">, col {{ e.column }}</template>:
              </span>
              {{ e.message }}
            </div>
          </div>

          <!-- Editor (textarea with line-number gutter) -->
          <div
            v-if="selectedFile || creatingNew"
            class="policy-view__editor-wrap"
          >
            <textarea
              class="policy-view__editor"
              :class="{ 'policy-view__editor--error': hasParseErrors }"
              :value="editorSource"
              :readonly="isReadOnly && !creatingNew"
              :aria-label="creatingNew ? 'New policy source' : `Edit ${selectedFile?.name}`"
              data-testid="policy-editor-textarea"
              spellcheck="false"
              autocorrect="off"
              autocomplete="off"
              @input="onEditorInput"
            />
          </div>

          <!-- Empty state -->
          <div
            v-else
            class="policy-view__editor-empty"
            data-testid="policy-editor-empty"
          >
            Select a policy to edit, or click <strong>+ New</strong> to create one.
          </div>
        </section>
      </div>
    </template>
    </div>
  </div>
</template>

<style scoped>
.policy-view {
  display: flex;
  flex-direction: column;
  height: 100%;
  font-family: var(--font-ui);
}

.policy-view__disabled {
  padding: 2rem;
  color: var(--ink-muted);
}

.policy-view__toast {
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

.policy-view__dialog-overlay {
  position: fixed;
  inset: 0;
  background: var(--modal-overlay);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.policy-view__dialog {
  background: var(--surface-1);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 1.5rem;
  max-width: 380px;
  width: 90%;
}

.policy-view__dialog-msg {
  margin: 0 0 1rem;
  font-size: 0.875rem;
}

.policy-view__dialog-btns {
  display: flex;
  gap: 0.5rem;
  justify-content: flex-end;
}

.policy-view__head {
  padding: 0.75rem 1rem 0.5rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
}

.policy-view__title {
  font-size: 0.6875rem;
  color: var(--ink-muted);
  letter-spacing: 0.1em;
  text-transform: uppercase;
}

.policy-view__heading {
  font-size: 1.125rem;
  font-weight: 600;
  margin: 0.25rem 0 0;
}

.policy-view__sub {
  margin: 0.125rem 0 0;
  color: var(--ink-muted);
  font-size: 0.8125rem;
}

.policy-view__layout {
  display: flex;
  flex: 1;
  overflow: hidden;
}

/* ── Sidebar ── */
.policy-view__sidebar {
  width: 220px;
  flex-shrink: 0;
  border-right: 1px solid var(--border-muted);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.policy-view__sidebar-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
}

.policy-view__h2 {
  font-size: 0.75rem;
  font-weight: 600;
  margin: 0;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--ink-muted);
}

.policy-view__sidebar-actions {
  display: flex;
  gap: 0.25rem;
}

.policy-view__file-list {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  flex: 1;
}

.policy-view__file {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.375rem 0.75rem;
  cursor: pointer;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--border-muted);
  transition: background 0.1s;
}

.policy-view__file:hover {
  background: var(--surface-2);
}

.policy-view__file--active {
  background: var(--surface-2);
  font-weight: 600;
}

.policy-view__file-name {
  font-family: var(--font-mono);
  font-size: 0.75rem;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.policy-view__file-status {
  flex-shrink: 0;
  font-size: 0.6875rem;
  font-weight: 700;
}

.policy-view__ok {
  color: var(--ok);
}

.policy-view__err {
  color: var(--danger);
  font-weight: 600;
  font-size: 0.75rem;
}

.policy-view__tag {
  font-size: 0.5625rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--ink-muted);
  padding: 0 0.2rem;
  border: 1px solid var(--border-muted);
  border-radius: 2px;
  flex-shrink: 0;
}

.policy-view__tag--readonly {
  color: var(--warn);
  border-color: var(--warn);
}

.policy-view__empty {
  padding: 0.75rem;
  color: var(--ink-muted);
  font-size: 0.8125rem;
}

/* ── Editor pane ── */
.policy-view__editor-pane {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-width: 0;
}

.policy-view__editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.policy-view__editor-title {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  min-width: 0;
}

.policy-view__editor-actions {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  flex-shrink: 0;
}

.policy-view__parse-ok {
  font-size: 0.75rem;
  color: var(--ok);
  font-weight: 600;
}

.policy-view__parse-err {
  font-size: 0.75rem;
  color: var(--danger);
  font-weight: 600;
}

.policy-view__banner {
  padding: 0.5rem 0.75rem;
  font-size: 0.8125rem;
  flex-shrink: 0;
}

.policy-view__banner--error {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  color: var(--danger);
  border-bottom: 1px solid color-mix(in srgb, var(--danger) 20%, transparent);
}

.policy-view__banner--parse {
  background: color-mix(in srgb, var(--warn) 10%, transparent);
  color: var(--warn-ink, var(--ink));
  border-bottom: 1px solid color-mix(in srgb, var(--warn) 20%, transparent);
}

.policy-view__parse-error-row {
  display: flex;
  gap: 0.375rem;
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.policy-view__parse-loc {
  color: var(--ink-muted);
  flex-shrink: 0;
}

.policy-view__err-msg {
  color: var(--danger);
  font-size: 0.75rem;
}

.policy-view__new-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border-muted);
  flex-shrink: 0;
  flex-wrap: wrap;
}

.policy-view__name-input {
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

.policy-view__name-input:focus {
  outline: 1px solid var(--accent);
}

.policy-view__new-actions {
  display: flex;
  gap: 0.375rem;
}

.policy-view__editor-wrap {
  flex: 1;
  overflow: hidden;
  display: flex;
  position: relative;
}

.policy-view__editor {
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

.policy-view__editor:read-only {
  background: var(--surface-2);
  cursor: default;
}

.policy-view__editor--error {
  border-top: 2px solid var(--danger);
}

.policy-view__editor-empty {
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
.policy-view__btn {
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

.policy-view__btn:hover:not(:disabled) {
  background: var(--surface-3);
}

.policy-view__btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.policy-view__btn--sm {
  padding: 0.125rem 0.5rem;
  font-size: 0.6875rem;
}

.policy-view__btn--primary {
  background: var(--accent);
  color: var(--accent-ink);
  border-color: var(--accent);
}

.policy-view__btn--primary:hover:not(:disabled) {
  filter: brightness(1.1);
}

.policy-view__btn--danger {
  color: var(--danger);
  border-color: var(--danger);
}

.policy-view__btn--danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
}

/* ── Sub-nav (Files / Decisions) ── */
.policy-view__subnav {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.75rem;
}

.policy-view__subtab {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0.25rem 0.75rem;
  background: var(--surface-2);
  color: var(--ink-muted);
  cursor: pointer;
  font-size: 0.75rem;
  font-family: var(--font-ui);
}

.policy-view__subtab:hover {
  color: var(--ink);
}

.policy-view__subtab--active {
  background: var(--surface-3);
  color: var(--ink);
  border-color: var(--accent);
}

/* ── Decisions panel (WP06) ── */
.policy-view__decisions {
  padding: 0 1.5rem 1.5rem;
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}

.policy-view__decisions-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.policy-view__decision-list {
  list-style: none;
  margin: 0.75rem 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}

.policy-view__decision-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  grid-template-areas:
    'outcome main policy'
    'reason reason reason'
    'time time time';
  gap: 0.125rem 0.75rem;
  align-items: baseline;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 0.5rem 0.75rem;
  font-size: 0.8125rem;
  background: var(--surface-1);
}

.policy-view__outcome {
  grid-area: outcome;
  text-transform: uppercase;
  font-size: 0.6875rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 0.0625rem 0.375rem;
  border-radius: var(--radius-sm);
  white-space: nowrap;
  height: fit-content;
}

.policy-view__outcome--deny {
  color: var(--danger);
  background: color-mix(in srgb, var(--danger) 14%, transparent);
}

.policy-view__outcome--allow {
  color: var(--ok);
  background: color-mix(in srgb, var(--ok) 14%, transparent);
}

.policy-view__outcome--na,
.policy-view__outcome--unknown {
  color: var(--ink-muted);
  background: var(--surface-2);
}

.policy-view__decision-main {
  grid-area: main;
  display: flex;
  gap: 0.5rem;
  align-items: baseline;
  min-width: 0;
}

.policy-view__decision-action {
  font-family: var(--font-mono);
  font-weight: 600;
  color: var(--ink);
}

.policy-view__decision-resource {
  font-family: var(--font-mono);
  color: var(--ink-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.policy-view__decision-policy {
  grid-area: policy;
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  color: var(--ink-muted);
  white-space: nowrap;
}

.policy-view__decision-reason {
  grid-area: reason;
  color: var(--ink-muted);
  font-size: 0.75rem;
}

.policy-view__decision-time {
  grid-area: time;
  color: var(--ink-muted);
  font-size: 0.6875rem;
}
</style>
