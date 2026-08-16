import { createApp } from 'vue';
import App from './App.vue';
import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router';

import './styles/global.css';

import { installHarnessClient } from '@/lib/harnessClientContext';
import { createHarnessClient } from '@/lib/harnessClient';
import { bootFeatureFlags } from '@/lib/featureFlags';
import { logEvent } from '@/lib/eventLog';
import type { SentryTier } from '@/sentry';

// Placeholder routes — primary surfaces (sessions/tools/bundles/providers/audit/settings)
// land in downstream missions consuming this chassis.
//
// Exported so `__tests__/entrypoint.routes.test.ts` can diff this table against
// main-served.ts's. The two drifted silently for a long time — served mode had
// no not-found route at all — and nothing could see it, because each table is
// an anonymous literal inside an entry point no test imported.
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
    // /search opens the sessions view with the search modal overlay.
    // The modal is rendered by Shell.vue and triggered by the
    // route-change guard below; this route makes the sidebar link work
    // and keeps the URL bookmarkable.
    path: '/search',
    name: 'search',
    component: () => import('@/views/sessions/SessionsView.vue'),
  },
  {
    // cedar-policy-editor-ui-01KQ8TD6 WP02 — Policy editor.
    // The view self-disables when AppInfo.policyEditorEnabled === false.
    path: '/policy',
    name: 'policy',
    component: () => import('@/views/policy/PolicyView.vue'),
  },
  {
    // sites-ui-01NSITE06 — Fleet Sites hosting surface.
    // Only reachable when sites_hosting capability is present; the nav
    // entry is hidden unless signedIn && capability('sites_hosting').
    path: '/sites',
    name: 'sites',
    component: () => import('@/views/sites/SitesView.vue'),
  },
  {
    // fleet-share-and-sync-01NDFSEX14 WP03 — Team catalog browser.
    // Only useful when signed in; hidden by nav when signedIn is false.
    path: '/marketplace',
    name: 'marketplace',
    component: () => import('@/views/marketplace/MarketplaceView.vue'),
  },
  {
    // WP08: catch-all not-found route (FR-011).
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

const client = createHarnessClient();
installHarnessClient(app, client);

// Global error visibility. Route ALL uncaught frontend errors to the backend
// log (~/.kenaz/harness/<env>/logs/harness.log via Diag_LogClientEvent) so
// render failures in portaled/modal content — which escape <ErrorBoundary>'s
// component-scoped onErrorCaptured — are diagnosable instead of silently
// blanking the UI (onboarding dialog, Add-provider drawer, etc.).
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

// Fleet capability gating. Every `v-if="signedIn && capability('…')"` in the
// app reads a module-level snapshot that nothing but this call populates —
// without it the Sites and Marketplace nav entries, the three Publish-to-team
// buttons, the Cedar team-policy editor and the Sync panel are invisible to
// every signed-in user, and Settings → Sync tells them to sign in on the same
// screen where Settings → Account shows them signed in.
// (docs/dead-code-audit-2026-08-16.md finding A4)
//
// Deliberately NOT nested inside the Sentry block below: the two have no
// relationship, and burying the capability gate inside a crash-reporting
// conditional is how it would go dark again. The AppInfo fetch is shared so
// boot still issues exactly one AppInfo RPC.
const appInfoPromise = bootFeatureFlags(client);

// wire-up point 4 for sentry: fetch crash-reporting tier from appInfo and
// lazily initialise @sentry/vue only when tier != 'off'.
// (sentry-error-monitoring-01KX5R8G WP04)
void (async () => {
  try {
    const info = await appInfoPromise;
    if (!info) return;
    const settings = await client.settings.get();
    const tier = (settings.crashReportingTier ?? 'off') as SentryTier;
    if (tier !== 'off' && settings.sentryDsn) {
      const { initSentry } = await import('@/sentry');
      await initSentry({
        tier,
        dsn: settings.sentryDsn,
        release: info.build ?? undefined,
        gitsha: info.commit ?? undefined,
        app,
      });
    }
  } catch {
    // Sentry init failure must never block app mount.
  }
})();

app.mount('#app');
