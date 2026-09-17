<script setup lang="ts">
/**
 * DocumentsView — /documents: read, write, and build documents into a local,
 * self-hostable knowledge site (contracts/documents-rpc.md).
 *
 * Documents belong to a session. The view always works in one named session
 * and shows what that session can see (its own documents plus global ones);
 * the backend enforces that, this view never widens it.
 *
 * Editing is plain source — HTML, or Markdown for a new document — with a
 * preview that is the SERVER's sanitized output, i.e. exactly the bytes a
 * save would store, rendered in the sandboxed IframeSandbox. Markdown is
 * converted to HTML here with the bundled `marked` and then goes through the
 * same server sanitizer as everything else.
 *
 * Building a site writes files into the workspace and stops. Nothing is
 * uploaded or published, and the result says so.
 *
 * Stale responses: list, open, save, build and preview each capture a
 * generation (and the session they ran in) before awaiting, and drop their
 * result if the user has since switched session, opened another document,
 * or started a different edit. A slow response must never paint another
 * session's data or overwrite the editor the user is now in.
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { marked } from 'marked';
import CanvasHead from '@/shell/CanvasHead.vue';
import IframeSandbox from '@/views/artifacts/preview/IframeSandbox.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { isServedMode } from '@/lib/useServedMode';
import { documentsErrorMessage, parseDocumentsError } from '@/lib/documentsErrors';
import type {
  DocumentPreview,
  DocumentRecord,
  DocumentSummary,
  KnowledgeSiteBuild,
  Session,
} from '@/lib/types';

type Mode = 'idle' | 'read' | 'edit' | 'create';
type SourceFormat = 'html' | 'markdown';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

// ── session ────────────────────────────────────────────────────────────
const sessions = ref<Session[]>([]);
const sessionsError = ref<string | null>(null);
const sessionId = ref<string>('');

function lastActive(s: Session): string {
  return s.lastActiveAt || s.updatedAt || s.createdAt || '';
}

// A fresh install has no sessions, and the global New session dialog needs a
// configured model to start one. Documents do not need a model, so the view
// can create a plain session itself (Sessions_Create takes only a name).
const newSessionName = ref('Documents');
const creatingSession = ref(false);
const createSessionError = ref<string | null>(null);

async function createSession() {
  if (creatingSession.value) return;
  creatingSession.value = true;
  createSessionError.value = null;
  try {
    const name = newSessionName.value.trim() || 'Documents';
    const created = await client.sessions.create(name);
    sessions.value = [created, ...sessions.value.filter((s) => s.id !== created.id)];
    sessionsError.value = null;
    sessionId.value = created.id;
  } catch (e) {
    createSessionError.value = documentsErrorMessage(e);
  } finally {
    creatingSession.value = false;
  }
}

/**
 * syncSessionSelect pins the <select>'s displayed value to sessionId after
 * every DOM patch. Creating a session prepends an option and changes the
 * selection in one patch; the select's own reset rules (keep the last
 * selected option in DOM order) then leave the OLD session displayed while
 * the view loads the new one. Setting the value post-patch removes that.
 */
const sessionSelect = ref<HTMLSelectElement | null>(null);
watch(
  [sessionId, sessions],
  () => {
    if (sessionSelect.value && sessionSelect.value.value !== sessionId.value) {
      sessionSelect.value.value = sessionId.value;
    }
  },
  { flush: 'post' },
);

async function loadSessions() {
  sessionsError.value = null;
  try {
    const list = (await client.sessions.list()) ?? [];
    sessions.value = [...list].sort((a, b) => lastActive(b).localeCompare(lastActive(a)));
    const wanted = typeof route.query.session === 'string' ? route.query.session : '';
    if (wanted && sessions.value.some((s) => s.id === wanted)) {
      sessionId.value = wanted;
    } else if (!sessions.value.some((s) => s.id === sessionId.value)) {
      sessionId.value = sessions.value[0]?.id ?? '';
    }
  } catch (e) {
    sessionsError.value = documentsErrorMessage(e);
  }
}

// ── list ───────────────────────────────────────────────────────────────
const documents = ref<DocumentSummary[]>([]);
const listLoading = ref(false);
const listError = ref<string | null>(null);
const selected = ref<Set<string>>(new Set());

let listGen = 0;

async function loadDocuments() {
  const gen = ++listGen;
  const forSession = sessionId.value;
  if (!forSession) {
    documents.value = [];
    listLoading.value = false;
    return;
  }
  listLoading.value = true;
  listError.value = null;
  try {
    const rows = (await client.documents.list(forSession)) ?? [];
    if (gen !== listGen || forSession !== sessionId.value) return;
    documents.value = rows;
    const visible = new Set(rows.map((d) => d.id));
    selected.value = new Set([...selected.value].filter((id) => visible.has(id)));
  } catch (e) {
    if (gen !== listGen || forSession !== sessionId.value) return;
    documents.value = [];
    listError.value = documentsErrorMessage(e);
  } finally {
    if (gen === listGen) listLoading.value = false;
  }
}

watch(sessionId, (id, prev) => {
  if (id === prev) return;
  closeDocument();
  selected.value = new Set();
  documents.value = [];
  listError.value = null;
  site.value = null;
  siteError.value = null;
  building.value = false;
  buildGen++;
  if (id && route.query.session !== id) {
    void router.replace({ query: { ...route.query, session: id } });
  }
  void loadDocuments();
});

function toggleSelected(id: string) {
  const next = new Set(selected.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selected.value = next;
}

function formatWhen(stamp: string): string {
  const d = new Date(stamp);
  return Number.isNaN(d.getTime()) ? stamp : d.toLocaleString();
}

// ── open / read ────────────────────────────────────────────────────────
const mode = ref<Mode>('idle');
const current = ref<DocumentRecord | null>(null);
const docError = ref<string | null>(null);

/**
 * docGen identifies "what the right-hand pane is showing". It advances on
 * every open, new document, close and session switch; an async result that
 * started under an older docGen is discarded.
 */
let docGen = 0;

async function openDocument(id: string): Promise<boolean> {
  const gen = ++docGen;
  const forSession = sessionId.value;
  docError.value = null;
  try {
    const doc = await client.documents.get(forSession, id);
    if (gen !== docGen || forSession !== sessionId.value) return false;
    resetEditor();
    current.value = doc;
    mode.value = 'read';
    return true;
  } catch (e) {
    if (gen !== docGen || forSession !== sessionId.value) return false;
    current.value = null;
    mode.value = 'idle';
    docError.value = documentsErrorMessage(e);
    return false;
  }
}

function closeDocument() {
  docGen++;
  mode.value = 'idle';
  current.value = null;
  docError.value = null;
  resetEditor();
}

// ── editor ─────────────────────────────────────────────────────────────
const title = ref('');
const format = ref<SourceFormat>('html');
const source = ref('');
const baseVersion = ref(0);
const saving = ref(false);
const saveError = ref<string | null>(null);
/** Set when a save hit version_conflict; holds the version on the server. */
const conflict = ref<{ latestVersion: number | null } | null>(null);

const preview = ref<DocumentPreview | null>(null);
const previewError = ref<string | null>(null);
let previewTimer: ReturnType<typeof setTimeout> | null = null;
let previewSeq = 0;

function resetEditor() {
  // Invalidate any preview still in flight for the previous editor.
  previewSeq++;
  if (previewTimer) {
    clearTimeout(previewTimer);
    previewTimer = null;
  }
  saving.value = false;
  title.value = '';
  format.value = 'html';
  source.value = '';
  baseVersion.value = 0;
  saveError.value = null;
  conflict.value = null;
  preview.value = null;
  previewError.value = null;
}

function startCreate() {
  docGen++;
  current.value = null;
  docError.value = null;
  resetEditor();
  format.value = 'markdown';
  mode.value = 'create';
}

function startEdit() {
  if (!current.value) return;
  docGen++;
  resetEditor();
  title.value = current.value.title;
  source.value = current.value.body;
  baseVersion.value = current.value.version;
  mode.value = 'edit';
  schedulePreview(0);
}

function cancelEdit() {
  if (mode.value === 'edit' && current.value) {
    docGen++;
    resetEditor();
    mode.value = 'read';
  } else {
    closeDocument();
  }
}

/** The HTML body a save would send. */
function bodyHtml(): string {
  if (format.value === 'markdown') {
    return marked.parse(source.value, { async: false }) as string;
  }
  return source.value;
}

function schedulePreview(delay = 300) {
  if (previewTimer) clearTimeout(previewTimer);
  previewTimer = setTimeout(() => void runPreview(), delay);
}

async function runPreview() {
  const seq = ++previewSeq;
  try {
    const result = await client.documents.preview(bodyHtml());
    if (seq !== previewSeq) return;
    preview.value = result;
    previewError.value = null;
  } catch (e) {
    if (seq !== previewSeq) return;
    preview.value = null;
    previewError.value = documentsErrorMessage(e);
  }
}

watch([source, format], () => {
  if (mode.value === 'edit' || mode.value === 'create') schedulePreview();
});

onBeforeUnmount(() => {
  if (previewTimer) clearTimeout(previewTimer);
});

async function save() {
  if (saving.value) return;
  const gen = docGen;
  const forSession = sessionId.value;
  const editingId = current.value?.id ?? null;
  const stillHere = () => gen === docGen && forSession === sessionId.value;
  saving.value = true;
  saveError.value = null;
  try {
    let doc: DocumentRecord;
    if (mode.value === 'create') {
      doc = await client.documents.create(forSession, title.value, bodyHtml());
    } else if (editingId) {
      doc = await client.documents.update(forSession, editingId, baseVersion.value, bodyHtml());
    } else {
      return;
    }
    // The write landed either way; refresh the list if we are still in
    // that session, but only take over the pane if the user is still here.
    if (forSession === sessionId.value) void loadDocuments();
    if (!stillHere()) return;
    docGen++;
    resetEditor();
    current.value = doc;
    mode.value = 'read';
  } catch (e) {
    if (!stillHere()) return;
    const parsed = parseDocumentsError(e);
    if (parsed?.code === 'version_conflict' && editingId) {
      conflict.value = { latestVersion: null };
      try {
        const latest = await client.documents.get(forSession, editingId);
        if (!stillHere()) return;
        conflict.value = { latestVersion: latest.version };
      } catch {
        // The document may have become unreadable; the banner still says why.
      }
    } else {
      saveError.value = documentsErrorMessage(e);
    }
  } finally {
    if (stillHere()) saving.value = false;
  }
}

/** Discard local edits and reopen the server's current version. */
async function reloadLatest() {
  if (!current.value) return;
  if (await openDocument(current.value.id)) startEdit();
}

/**
 * Keep the local edits and make the next save apply on top of the version
 * now on the server. An explicit choice — the user has seen the conflict.
 */
function keepMineOnLatest() {
  if (conflict.value?.latestVersion == null) return;
  baseVersion.value = conflict.value.latestVersion;
  conflict.value = null;
}

// ── knowledge site ─────────────────────────────────────────────────────
const siteSlug = ref('');
const siteTitle = ref('');
const exportsDir = ref<string | null>(null);
const building = ref(false);
const site = ref<KnowledgeSiteBuild | null>(null);
const siteError = ref<string | null>(null);

const slugLooksValid = computed(() => /^[a-z0-9]+(-[a-z0-9]+)*$/.test(siteSlug.value) && siteSlug.value.length <= 40);
const canBuild = computed(
  () => !!sessionId.value && selected.value.size > 0 && slugLooksValid.value && !building.value,
);

async function loadExportsDir() {
  try {
    exportsDir.value = (await client.documents.exportsDir()).dir;
  } catch {
    exportsDir.value = null;
  }
}

let buildGen = 0;

async function buildSite() {
  if (!canBuild.value) return;
  const gen = ++buildGen;
  const forSession = sessionId.value;
  const stillHere = () => gen === buildGen && forSession === sessionId.value;
  building.value = true;
  siteError.value = null;
  site.value = null;
  try {
    const result = await client.documents.buildSite(
      forSession,
      siteSlug.value,
      siteTitle.value,
      [...selected.value],
    );
    if (stillHere()) site.value = result;
  } catch (e) {
    if (stillHere()) siteError.value = documentsErrorMessage(e);
  } finally {
    if (stillHere()) building.value = false;
  }
}

/** Single-quote a path for a POSIX shell. */
function shellQuote(p: string): string {
  return `'${p.replace(/'/g, `'\\''`)}'`;
}

/** The site's directory name, i.e. the slug it was built under. */
const siteName = computed(() => site.value?.siteDir.split('/').filter(Boolean).pop() ?? '');

const served = isServedMode();
const inWorkbenchMount = computed(
  () => served && !!site.value && site.value.siteDir.startsWith('/workspace/'),
);

function titleFor(id: string): string {
  return documents.value.find((d) => d.id === id)?.title ?? id;
}

onMounted(async () => {
  await loadSessions();
  await Promise.all([loadDocuments(), loadExportsDir()]);
});
</script>

<template>
  <div class="h-full flex flex-col" data-testid="documents-view">
    <CanvasHead
      number="10"
      section="DOCUMENTS"
      title="Documents"
      subtitle="Write documents with the assistant or by hand, then build them into a static knowledge site you host yourself."
    />

    <div class="px-6 py-3 flex flex-wrap items-center gap-3 border-b border-border-muted">
      <label class="flex items-center gap-2">
        <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Session</span>
        <!-- Not v-model: see syncSessionSelect. -->
        <select
          ref="sessionSelect"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink max-w-[22rem]"
          data-testid="documents-session-select"
          :disabled="sessions.length === 0"
          @change="sessionId = ($event.target as HTMLSelectElement).value"
        >
          <option v-for="s in sessions" :key="s.id" :value="s.id">{{ s.name || s.id }}</option>
        </select>
      </label>
      <button
        v-if="sessions.length > 0"
        type="button"
        class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink disabled:opacity-50"
        data-testid="documents-create-session-inline"
        :disabled="creatingSession"
        title="Create a new session for documents (no model needed)"
        @click="createSession"
      >
        {{ creatingSession ? 'Creating…' : 'New session' }}
      </button>
      <span
        v-if="sessions.length > 0 && createSessionError"
        class="font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="documents-create-session-inline-error"
      >
        Could not create a session: {{ createSessionError }}
      </span>
      <button
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
        data-testid="documents-new"
        :disabled="!sessionId"
        @click="startCreate"
      >
        New document
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink disabled:opacity-50"
        data-testid="documents-refresh"
        :disabled="!sessionId || listLoading"
        @click="loadDocuments"
      >
        Refresh
      </button>
      <span class="font-ui text-[11px] text-ink-subtle">
        A session sees its own documents and global ones.
      </span>
    </div>

    <div v-if="sessionsError" class="px-6 py-3 font-ui text-[12px] text-signal-danger" role="alert" data-testid="documents-sessions-error">
      Could not load sessions: {{ sessionsError }}
    </div>
    <div
      v-else-if="sessions.length === 0"
      class="px-6 py-6 space-y-3 max-w-xl"
      data-testid="documents-no-sessions"
    >
      <p class="font-ui text-[12px] text-ink-muted">
        Documents belong to a session. Create one here to start writing — it does not need a model or
        provider, and you can chat in it later once one is configured.
      </p>
      <form class="flex flex-wrap items-center gap-2" @submit.prevent="createSession">
        <label class="flex items-center gap-2">
          <span class="font-ui text-[11px] text-ink-muted">Session name</span>
          <input
            v-model="newSessionName"
            type="text"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
            data-testid="documents-new-session-name"
          />
        </label>
        <button
          type="submit"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
          data-testid="documents-create-session"
          :disabled="creatingSession"
        >
          {{ creatingSession ? 'Creating…' : 'Create session' }}
        </button>
      </form>
      <p
        v-if="createSessionError"
        class="font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="documents-create-session-error"
      >
        Could not create a session: {{ createSessionError }}
      </p>
    </div>

    <div v-else class="flex-1 min-h-0 grid grid-cols-[minmax(16rem,22rem)_1fr]">
      <!-- left: list + site builder -->
      <aside class="min-h-0 overflow-y-auto border-r border-border-muted">
        <div v-if="listError" class="px-4 py-3 font-ui text-[12px] text-signal-danger" role="alert" data-testid="documents-list-error">
          {{ listError }}
        </div>
        <div v-else-if="listLoading && documents.length === 0" class="px-4 py-3 font-ui text-[12px] text-ink-muted">
          Loading…
        </div>
        <div
          v-else-if="documents.length === 0"
          class="px-4 py-4 font-ui text-[12px] text-ink-muted"
          data-testid="documents-empty"
        >
          No documents in this session yet. Ask the assistant to write one (it saves with
          <span class="font-mono text-[11px]">kenaz__save_document</span>), or create one with New document.
        </div>
        <ul v-else class="divide-y divide-border-muted" data-testid="documents-list">
          <li
            v-for="d in documents"
            :key="d.id"
            class="flex items-start gap-2 px-4 py-2"
            :class="current?.id === d.id ? 'bg-surface-2' : ''"
          >
            <input
              type="checkbox"
              class="mt-1 accent-accent"
              :checked="selected.has(d.id)"
              :aria-label="`Include ${d.title} in the site`"
              :data-testid="`documents-select-${d.id}`"
              @change="toggleSelected(d.id)"
            />
            <button
              type="button"
              class="flex-1 text-left"
              :data-testid="`documents-open-${d.id}`"
              @click="openDocument(d.id)"
            >
              <div class="font-ui text-[13px] text-ink">{{ d.title }}</div>
              <div class="font-ui text-[11px] text-ink-subtle">
                v{{ d.version }} · {{ d.scope }} · {{ formatWhen(d.updatedAt) }}
              </div>
            </button>
          </li>
        </ul>

        <section class="px-4 py-4 border-t border-border-muted space-y-2" data-testid="documents-site-panel">
          <h2 class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Build a knowledge site</h2>
          <p class="font-ui text-[11px] text-ink-muted">
            {{ selected.size }} selected. Builds a static site into your workspace. Nothing is uploaded.
          </p>
          <label class="block">
            <span class="font-ui text-[11px] text-ink-muted">Site name</span>
            <input
              v-model.trim="siteSlug"
              type="text"
              placeholder="team-handbook"
              class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-mono text-[11px] text-ink"
              data-testid="documents-site-slug"
            />
          </label>
          <p v-if="siteSlug && !slugLooksValid" class="font-ui text-[11px] text-signal-warn">
            Lowercase letters, digits and single hyphens, at most 40 characters.
          </p>
          <label class="block">
            <span class="font-ui text-[11px] text-ink-muted">Catalog title (optional)</span>
            <input
              v-model="siteTitle"
              type="text"
              class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink"
              data-testid="documents-site-title"
            />
          </label>
          <p v-if="exportsDir" class="font-ui text-[11px] text-ink-subtle break-all">
            Writes to <span class="font-mono">{{ exportsDir }}/{{ siteSlug || '&lt;site-name&gt;' }}</span>
          </p>
          <button
            type="button"
            class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50"
            data-testid="documents-build-site"
            :disabled="!canBuild"
            @click="buildSite"
          >
            {{ building ? 'Building…' : 'Build site' }}
          </button>
          <div v-if="siteError" class="font-ui text-[11px] text-signal-danger" role="alert" data-testid="documents-site-error">
            {{ siteError }}
          </div>
        </section>
      </aside>

      <!-- right: reader / editor / build result -->
      <main class="min-h-0 overflow-y-auto px-6 py-4 space-y-4">
        <div v-if="docError" class="font-ui text-[12px] text-signal-danger" role="alert" data-testid="documents-open-error">
          {{ docError }}
        </div>

        <section v-if="site" class="rounded-sm border border-border-muted bg-surface-1 px-4 py-3 space-y-3" data-testid="documents-site-result">
          <div class="flex items-baseline justify-between gap-3">
            <h2 class="font-ui text-[13px] text-ink">
              Site built locally — {{ site.documents }} {{ site.documents === 1 ? 'document' : 'documents' }}
            </h2>
            <span class="font-ui text-[11px] text-ink-muted" data-testid="documents-site-not-published">
              Not published. Nothing was uploaded.
            </span>
          </div>
          <dl class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 font-ui text-[11px]">
            <dt class="text-ink-subtle">Site folder</dt>
            <dd class="font-mono text-ink break-all" data-testid="documents-site-dir">{{ site.siteDir }}</dd>
            <dt class="text-ink-subtle">Serve this folder</dt>
            <dd class="font-mono text-ink break-all" data-testid="documents-site-public">{{ site.publicDir }}</dd>
            <dt class="text-ink-subtle">Bundle</dt>
            <dd class="font-mono text-ink break-all" data-testid="documents-site-bundle">{{ site.bundle }}</dd>
            <dt class="text-ink-subtle">Bundle sha256</dt>
            <dd class="font-mono text-ink break-all">{{ site.bundleSha256 }}</dd>
          </dl>

          <div class="space-y-2" data-testid="documents-serve-instructions">
            <h3 class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Serve it yourself</h3>
            <p class="font-ui text-[11px] text-ink-muted">
              The site is plain static files. Serve only the <span class="font-mono">public</span> folder;
              <span class="font-mono">kameas-site.json</span> sits beside it and is not part of the site.
            </p>
            <p class="font-ui text-[11px] text-ink-muted">
              Preview it where the files are{{ served ? ' (inside this workbench, run it in a workbench terminal)' : '' }},
              then open http://localhost:8080/ there:
            </p>
            <pre class="rounded-sm bg-surface-2 px-3 py-2 font-mono text-[11px] text-ink whitespace-pre-wrap break-all" data-testid="documents-serve-local">python3 -m http.server 8080 --bind 127.0.0.1 --directory {{ shellQuote(site.publicDir) }}</pre>
            <p class="font-ui text-[11px] text-ink-muted">
              Host it: copy the contents of the <span class="font-mono">public</span> folder to any static web host you
              control — an nginx or Apache document root, an object-storage bucket configured for static websites, or an
              internal file server. Every page is self-contained and declares its own Content-Security-Policy, so no
              server headers or build step are needed.
            </p>
            <p class="font-ui text-[11px] text-ink-muted">Or move the single-file bundle and unpack it where it will be served:</p>
            <pre class="rounded-sm bg-surface-2 px-3 py-2 font-mono text-[11px] text-ink whitespace-pre-wrap break-all" data-testid="documents-serve-bundle">mkdir -p {{ siteName }} &amp;&amp; tar -xzf {{ shellQuote(site.bundle) }} -C {{ siteName }}   # then serve {{ siteName }}/public</pre>
            <p v-if="inWorkbenchMount" class="font-ui text-[11px] text-ink-muted" data-testid="documents-serve-workbench">
              These paths are inside this workbench. <span class="font-mono">/workspace</span> is the folder you shared
              with the workbench, so on your computer the site is in that folder under
              <span class="font-mono">{{ site.siteDir.replace(/^\/workspace\//, '') }}</span>.
            </p>
          </div>

          <div v-if="site.warnings.length" class="space-y-1" data-testid="documents-site-warnings">
            <h3 class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Markdown copies lost formatting</h3>
            <ul class="font-ui text-[11px] text-signal-warn list-disc pl-4">
              <li v-for="w in site.warnings" :key="w.documentId">
                {{ titleFor(w.documentId) }}: {{ w.warnings.join('; ') }}
              </li>
            </ul>
            <p class="font-ui text-[11px] text-ink-subtle">The HTML pages are unaffected.</p>
          </div>
        </section>

        <div
          v-if="mode === 'idle' && !site && !docError"
          class="font-ui text-[12px] text-ink-muted"
          data-testid="documents-idle"
        >
          Open a document to read it, or select documents on the left to build a site.
        </div>

        <!-- reader -->
        <section v-if="mode === 'read' && current" class="space-y-3" data-testid="documents-reader">
          <div class="flex items-baseline justify-between gap-3">
            <div>
              <h2 class="font-ui text-[15px] text-ink" data-testid="documents-reader-title">{{ current.title }}</h2>
              <div class="font-ui text-[11px] text-ink-subtle">
                Version {{ current.version }} · {{ current.scope }} · updated {{ formatWhen(current.updatedAt) }} ·
                {{ current.byteSize }} bytes
              </div>
            </div>
            <div class="flex gap-2">
              <button
                type="button"
                class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
                data-testid="documents-edit"
                @click="startEdit"
              >
                Edit
              </button>
              <button
                type="button"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink"
                @click="closeDocument"
              >
                Close
              </button>
            </div>
          </div>
          <IframeSandbox :html="current.body" />
        </section>

        <!-- editor -->
        <section v-if="mode === 'edit' || mode === 'create'" class="space-y-3" data-testid="documents-editor">
          <div class="flex flex-wrap items-end gap-3">
            <label v-if="mode === 'create'" class="flex-1 min-w-[14rem]">
              <span class="font-ui text-[11px] text-ink-muted">Title</span>
              <input
                v-model="title"
                type="text"
                class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
                data-testid="documents-title"
              />
            </label>
            <h2 v-else class="flex-1 font-ui text-[15px] text-ink">
              {{ title }} <span class="font-ui text-[11px] text-ink-subtle">editing from version {{ baseVersion }}</span>
            </h2>
            <label v-if="mode === 'create'" class="flex items-center gap-2">
              <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Source</span>
              <select
                v-model="format"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink"
                data-testid="documents-format"
              >
                <option value="markdown">Markdown</option>
                <option value="html">HTML</option>
              </select>
            </label>
            <span v-else class="font-ui text-[11px] text-ink-subtle">Source: HTML (as stored)</span>
          </div>

          <div
            v-if="conflict"
            class="rounded-sm border border-signal-warn px-3 py-2 font-ui text-[12px] text-ink space-y-2"
            role="alert"
            data-testid="documents-conflict"
          >
            <p>
              This document changed since you opened version {{ baseVersion }}
              <template v-if="conflict.latestVersion !== null">— it is now version {{ conflict.latestVersion }}</template>.
              Nothing was saved. Your edits are still below.
            </p>
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 text-[11px] text-ink hover:bg-surface-2"
                data-testid="documents-conflict-reload"
                @click="reloadLatest"
              >
                Discard my edits and load the latest
              </button>
              <button
                v-if="conflict.latestVersion !== null"
                type="button"
                class="rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 text-[11px] text-accent hover:bg-accent-glow"
                data-testid="documents-conflict-keep"
                @click="keepMineOnLatest"
              >
                Keep my edits and save them over version {{ conflict.latestVersion }}
              </button>
            </div>
          </div>

          <div class="grid grid-cols-2 gap-4">
            <label class="block">
              <span class="font-ui text-[11px] text-ink-muted">{{ format === 'markdown' ? 'Markdown' : 'HTML' }}</span>
              <textarea
                v-model="source"
                spellcheck="false"
                class="mt-1 w-full h-[60vh] rounded-sm border border-border-muted bg-surface-1 px-3 py-2 font-mono text-[12px] text-ink"
                data-testid="documents-source"
              />
            </label>
            <div>
              <div class="flex items-center justify-between font-ui text-[11px] text-ink-muted">
                <span>Preview — exactly what will be saved</span>
                <span v-if="preview">{{ preview.byteSize }} bytes</span>
              </div>
              <p
                v-if="preview?.sanitized"
                class="mt-1 font-ui text-[11px] text-signal-warn"
                data-testid="documents-sanitized-note"
              >
                Scripts, embeds, event handlers or external resources were removed. Images must be embedded as data URIs.
              </p>
              <p v-if="previewError" class="mt-1 font-ui text-[11px] text-signal-danger" role="alert" data-testid="documents-preview-error">
                {{ previewError }}
              </p>
              <div class="mt-1">
                <IframeSandbox :html="preview?.html ?? ''" />
              </div>
            </div>
          </div>

          <div v-if="saveError" class="font-ui text-[12px] text-signal-danger" role="alert" data-testid="documents-save-error">
            {{ saveError }}
          </div>
          <div class="flex gap-2">
            <button
              type="button"
              class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
              data-testid="documents-save"
              :disabled="saving || !!conflict"
              @click="save"
            >
              {{ saving ? 'Saving…' : mode === 'create' ? 'Create document' : 'Save new version' }}
            </button>
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-1 px-3 py-1 font-ui text-[12px] text-ink-muted hover:bg-surface-2 hover:text-ink"
              @click="cancelEdit"
            >
              Cancel
            </button>
          </div>
        </section>
      </main>
    </div>
  </div>
</template>
