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
import { createRouter, createWebHashHistory } from 'vue-router';

import './styles/global.css';

import { installHarnessClient } from '@/lib/harnessClientContext';
import { createServedHarnessClient } from '@/lib/harnessClient';
import { logEvent } from '@/lib/eventLog';

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
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
      path: '/corpora',
      name: 'corpora',
      component: () => import('@/views/corpora/CorporaView.vue'),
    },
    {
      path: '/corpora/:id',
      name: 'corpus-detail',
      component: () => import('@/views/corpora/CorpusDetail.vue'),
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
      path: '/search',
      name: 'search',
      component: () => import('@/views/sessions/SessionsView.vue'),
    },
    {
      path: '/policy',
      name: 'policy',
      component: () => import('@/views/policy/PolicyView.vue'),
    },
  ],
});

const app = createApp(App);
app.use(router);

// Served mode: HTTP/WS transport, no Wails dependency.
const client = createServedHarnessClient();
installHarnessClient(app, client);

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
