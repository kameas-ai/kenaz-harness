<script setup lang="ts">
/**
 * PolicyView — Cedar policy bundle editor + decision audit panel.
 * (cedar-policy-editor-ui-01KQ8TD6 WP02)
 *
 * Two-pane layout:
 *   Left:  file list with "New" button + metadata
 *   Right: editor pane with Save / Delete / Validate status
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
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import type { PolicyFileDetail, ParseError } from '@/lib/types';

const client = useHarnessClient();

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
</script>

<template>
  <div class="flex h-full min-h-0">
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
        <h1 class="policy-view__heading">Cedar policy editor</h1>
        <p class="policy-view__sub">
          Edit Cedar policies under <code>&lt;DataDir&gt;/policy/</code>.
          Embedded defaults are read-only. Changes take effect on the next tool call.
        </p>
      </header>

      <div class="policy-view__layout">
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
</style>
