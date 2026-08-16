/**
 * main-served.ts — Entry point for the served-mode frontend bundle.
 *
 * This module must NOT import anything from wailsjs/ or depend on the Wails
 * runtime.  It wires createServedHarnessClient() as the transport instead of
 * createHarnessClient().
 *
 * Token injection: the Go-side serve handler is expected to inject either
 *   <meta name="harness-token" content="<token>">
 * or set window.__HARNESS_TOKEN__ = "<token>" before this script runs.
 * servedTransport.ts resolves the token in that priority order.
 */
import { createApp } from 'vue';
import App from './App.vue';
import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router';

import './styles/global.css';

import { installHarnessClient } from '@/lib/harnessClientContext';
import { createServedHarnessClient } from '@/lib/harnessClient';
import { bootFeatureFlags } from '@/lib/featureFlags';
import { logEvent } from '@/lib/eventLog';

// Exported so `__tests__/entrypoint.routes.test.ts` can diff this table against
// main.ts's, with the desktop-only surfaces named explicitly. Nothing else
// could see the two drift apart.
export const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/sessions' },
  {
    path: '/sessions/:id?',
    name: 'sessions',
    component: () => import('@/views/sessions/SessionsView.vue'),
  },
  {
    path: '/tools',
    name: 'tools',
    component: () => import('@/views/tools/ToolsView.vue'),
  },
  {
    path: '/bundles',
    name: 'bundles',
    component: () => import('@/views/bundles/BundlesView.vue'),
  },
  {
    path: '/providers',
    name: 'providers',
    component: () => import('@/views/providers/ProvidersView.vue'),
  },
  {
    path: '/audit',
    name: 'audit',
    component: () => import('@/views/audit/AuditView.vue'),
  },
  {
    path: '/contexts',
    name: 'contexts',
    component: () => import('@/views/contexts/ContextsView.vue'),
  },
  {
    path: '/projects/:id',
    name: 'project',
    component: () => import('@/views/projects/ProjectLandingPage.vue'),
  },
  {
    path: '/memory',
    name: 'memory',
    component: () => import('@/views/memory/MemoryView.vue'),
  },
  {
    path: '/hooks',
    name: 'hooks',
    component: () => import('@/views/hooks/HooksView.vue'),
  },
  {
    path: '/workflows',
    name: 'workflows',
    component: () => import('@/views/workflows/WorkflowsView.vue'),
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/settings/SettingsView.vue'),
  },
  {
    path: '/permissions/:family?',
    name: 'permissions',
    component: () => import('@/views/permissions/PermissionsView.vue'),
  },
  {
    path: '/artifacts',
    name: 'artifacts',
    component: () => import('@/views/artifacts/ArtifactsView.vue'),
  },
  {
    // FR-002 (01NKNOW01): Corpora surface retired; redirect to Contexts.
    path: '/corpora/:pathMatch(.*)*',
    redirect: '/contexts',
  },
  {
    path: '/agentgraph',
    name: 'graphs',
    component: () => import('@/views/agentgraph/GraphsView.vue'),
  },
  {
    path: '/agentgraph/edit/:id',
    name: 'graph-editor',
    component: () => import('@/views/agentgraph/GraphEditor.vue'),
  },
  {
    path: '/agentgraph/run/:runId',
    name: 'graph-run',
    component: () => import('@/views/agentgraph/RunView.vue'),
  },
  {
    // The executed run, rendered as a graph
    // (agentgraph-total-convergence-01PMGX01 WP12). Reuses the
    // editor: a materialized conversation and an authored graph are
    // the same artifact, so they get the same viewer — the spec comes
    // back with scope 'materialized', which puts the editor in
    // read-only mode. Chat turns are addressable here too; their run
    // id is the chat stream's sub id.
    path: '/agentgraph/run/:runId/graph',
    name: 'graph-materialized',
    component: () => import('@/views/agentgraph/GraphEditor.vue'),
  },
  {
    path: '/search',
    name: 'search',
    component: () => import('@/views/sessions/SessionsView.vue'),
  },
  {
    path: '/policy',
    name: 'policy',
    component: () => import('@/views/policy/PolicyView.vue'),
  },
  {
    // Catch-all not-found route, mirroring main.ts. Its absence meant an
    // unmatched hash rendered a blank <router-view> with no explanation —
    // Shell.vue has no fallback slot. Note that `/sites` and `/marketplace`
    // are deliberately NOT registered here (see docs/served-mode-boundary.md):
    // the served RPC surface is the allowlist in core/serve/methods.go, which
    // carries no Sites_* or Catalog_* method, so both views would render a
    // shell over a backend that answers "unknown method" to every call. The
    // LeftRail entries that reach them are gated on !isServedMode() for the
    // same reason; this catch-all is the backstop for a bookmarked or
    // hand-typed URL.
    // (docs/dead-code-audit-2026-08-16.md finding B4)
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
  },
];

const router = createRouter({
  history: createWebHashHistory(),
  routes,
});

const app = createApp(App);
app.use(router);

// Served mode: HTTP/WS transport, no Wails dependency.
const client = createServedHarnessClient();
installHarnessClient(app, client);

// Fleet capability gating — same reasoning as main.ts. `AppInfo` is one of the
// methods core/serve/methods.go dispatches, so the served bundle gets a real
// capability snapshot; without this call every gate here is false too.
// (docs/dead-code-audit-2026-08-16.md finding A4)
void bootFeatureFlags(client);

app.config.errorHandler = (err, _instance, info) => {
  const e = err instanceof Error ? err : new Error(String(err));
  // eslint-disable-next-line no-console
  console.error('[vue.errorHandler]', info, e);
  logEvent('error', 'vue.error', {
    info: String(info),
    message: e.message,
    stack: e.stack ?? '',
  });
};
window.addEventListener('error', (ev) => {
  logEvent('error', 'window.error', {
    message: ev.message,
    source: ev.filename ?? '',
    pos: `${ev.lineno ?? 0}:${ev.colno ?? 0}`,
    stack: ev.error instanceof Error ? (ev.error.stack ?? '') : '',
  });
});
window.addEventListener('unhandledrejection', (ev) => {
  const r = ev.reason;
  const e = r instanceof Error ? r : new Error(String(r));
  logEvent('error', 'window.unhandledrejection', {
    message: e.message,
    stack: e.stack ?? '',
  });
});

app.mount('#app');
