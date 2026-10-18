/**
 * builtinTools — the built-in Kenaz tools as rows of the "Add capability"
 * surface (install-framework-01DOGF0B WP04, FR-4: "builtin toggles keep a
 * no-install home — shown in the same list with a toggle instead of
 * Install").
 *
 * These are capabilities you ENABLE, not install: nothing is fetched,
 * verified or registered, so they are not an install.Provider — they are
 * settings toggles rendered in the same list. Moved here verbatim from the
 * retired KenazToolsPanel.vue (same settings calls, same data-testids, same
 * defaults and the same "read error → default" fallbacks).
 */
import type { HarnessClient } from '@/lib/harnessClient';

export interface BuiltinTool {
  /** Stable id; also the row key. */
  id: string;
  /** data-testid stem: `${testid}-tool-row`, `${testid}-toggle`. */
  testid: string;
  name: string;
  /** aria-label of the toggle (unchanged from KenazToolsPanel). */
  ariaLabel: string;
  /** One-line summary for the list row. */
  summary: string;
  /** Full description for the detail pane. */
  description: string;
  /** The kenaz__* tools the toggle gates, when it gates a family. */
  tools?: readonly string[];
  /** Value shown when the settings read fails. */
  fallback: boolean;
  get(client: HarnessClient): Promise<boolean>;
  set(client: HarnessClient, enabled: boolean): Promise<void>;
}

export const BUILTIN_TOOLS: readonly BuiltinTool[] = [
  {
    id: 'websearch',
    testid: 'websearch',
    name: 'Web search',
    ariaLabel: 'Enable web search tool',
    summary: 'Local-first web search — DuckDuckGo + Wikipedia, no API key.',
    description:
      "Local-first web search built into the harness. Queries DuckDuckGo's HTML endpoint + Wikipedia's API and extracts readable content via go-readability. No API key, no third-party SDK, no data leaves your machine beyond the search query itself. Cedar policy gates the outbound HTTP request hostname.",
    fallback: false,
    get: (c) => c.settings.getWebSearch(),
    set: (c, v) => c.settings.setWebSearch(v),
  },
  {
    id: 'webfetch',
    testid: 'webfetch',
    name: 'Web fetch',
    ariaLabel: 'Enable web fetch tool',
    summary: 'Fetch URLs; resolves @secret: references in request headers.',
    description:
      'Lets the assistant fetch URLs and resolve @secret: references in request headers. Outbound HTTP is gated by Cedar policy (host allowlist). Default OFF — enable only when the assistant needs to read live web content or call authenticated endpoints.',
    fallback: false,
    get: (c) => c.settings.getWebFetchEnabled(),
    set: (c, v) => c.settings.setWebFetchEnabled(v),
  },
  {
    id: 'bash',
    testid: 'bash',
    name: 'Bash',
    ariaLabel: 'Enable bash tool',
    summary: 'Local bash, gated by a per-command allowlist.',
    description:
      "Local bash execution gated by a per-command allowlist. The model can only run commands you've allowed; everything else is denied at parse time. Output stays local and is gated through the Cedar policy engine. Use Bash with care — pair with the filesystem recipe or the read_bash_output state node to keep results in context.",
    fallback: false,
    get: (c) => c.settings.getBash(),
    set: (c, v) => c.settings.setBash(v),
  },
  {
    id: 'saveartifact',
    testid: 'saveartifact',
    name: 'Save artifact',
    ariaLabel: 'Enable save artifact tool',
    summary: 'Save deliverables to Artifacts (content-addressed). Default ON.',
    description:
      'Lets the assistant save deliverables (notes, code, summaries) straight to the Artifacts tab. Content is stored content-addressed in the same media-store the rest of the harness uses — no filesystem touch, no MCP filesystem recipe required. Default ON; the model picks this tool when you ask it to save, export, or produce a document. Also gates kenaz__save_document, which saves sanitized HTML documents to your local document store.',
    // Default ON: a settings-store glitch must not disable the dial UI.
    fallback: true,
    get: (c) => c.settings.getSaveArtifact(),
    set: (c, v) => c.settings.setSaveArtifact(v),
  },
  {
    id: 'fs-read',
    testid: 'fs-read',
    name: 'Filesystem read tools',
    ariaLabel: 'Enable filesystem read tool',
    summary: 'Read-family builtins (read_file, list_dir, glob, grep, …).',
    description:
      'Enables the read-family builtin tools shipped with the harness. Default OFF. Cedar policy still applies to every call. Filesystem access requests are gated by the per-directory allowlist.',
    tools: [
      'kenaz__read_file',
      'kenaz__list_dir',
      'kenaz__glob',
      'kenaz__grep',
      'kenaz__list_open_worklist',
    ],
    fallback: false,
    get: (c) => c.settings.getFSReadEnabled(),
    set: (c, v) => c.settings.setFSReadEnabled(v),
  },
  {
    id: 'fs-write',
    testid: 'fs-write',
    name: 'Filesystem write tools',
    ariaLabel: 'Enable filesystem write tool',
    summary: 'Write-family builtins (write_file, edit_file, …).',
    description:
      'Enables the write-family builtin tools shipped with the harness. Default OFF. Write tools modify your filesystem directly. Cedar policy still applies.',
    tools: [
      'kenaz__write_file',
      'kenaz__edit_file',
      'kenaz__update_document',
      'kenaz__build_knowledge_site',
    ],
    fallback: false,
    get: (c) => c.settings.getFSWriteEnabled(),
    set: (c, v) => c.settings.setFSWriteEnabled(v),
  },
  {
    id: 'todo',
    testid: 'todo',
    name: 'Task list',
    ariaLabel: 'Enable todo tool',
    summary: 'Session-scoped structured task list (kenaz__todo_write).',
    description:
      'Enables the kenaz__todo_write builtin. The model maintains a session-scoped structured task list — items have status (pending, in_progress, done, cancelled) and priority. The list is written atomically on each call and visible in the chat via the task-list chip. Default OFF.',
    fallback: false,
    get: (c) => c.settings.getTodoEnabled(),
    set: (c, v) => c.settings.setTodoEnabled(v),
  },
];
