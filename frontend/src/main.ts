import { createApp } from 'vue';
import App from './App.vue';
import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router';

import './styles/global.css';

import { installHarnessClient } from '@/lib/harnessClientContext';
import { createHarnessClient } from '@/lib/harnessClient';
import { bootFeatureFlags } from '@/lib/featureFlags';
import { logEvent } from '@/lib/eventLog';
import { redirectLegacySettingsTab } from '@/lib/legacyRoutes';
import type { SentryTier } from '@/sentry';

// Desktop route table — 20 wired routes as of entry-points-and-crash-
// reporting-01PMZD13 UNIT-7 (the "Placeholder routes … land in downstream
// missions" comment this replaced was stale; every surface listed below
// is a real, mounted route, not a stub).
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
  // knowledge-home-01DOGF0E WP02 (FR-1, FR-2): Contexts and Memory are one
  // Knowledge surface with Curated and Learned sections; the stores stay
  // separate. The old paths redirect into the matching section, keeping the
  // query string (MemoryView reads ?scopeKind=/?scopeId=, MemoryBadge pushes
  // ?project=), so bookmarks, palette history and a persisted lastRoute of
  // either old path still land in the right place.
  { path: '/knowledge', redirect: (to) => ({ path: '/knowledge/curated', query: to.query }) },
  {
    path: '/knowledge/:section(curated|learned)',
    name: 'knowledge',
    component: () => import('@/views/knowledge/KnowledgeView.vue'),
  },
  { path: '/contexts', redirect: (to) => ({ path: '/knowledge/curated', query: to.query }) },
  {
    path: '/projects/:id',
    name: 'project',
    component: () => import('@/views/projects/ProjectLandingPage.vue'),
  },
  // `/memory/<chunk id>` (an old CrossReferenceLink target that never had a
  // route) lands in Learned with the chunk targeted via ?chunk=<id>.
  {
    path: '/memory/:pathMatch(.*)*',
    redirect: (to) => {
      const seg = to.params.pathMatch;
      const chunk = Array.isArray(seg) ? seg[0] : seg;
      return {
        path: '/knowledge/learned',
        query: chunk ? { ...to.query, chunk } : to.query,
      };
    },
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
    // nav-ia-sweep-01DOGF0F WP05: ?tab=scheduledchats|tasks|workflows moved
    // to the Workflows surface; old links redirect (lib/legacyRoutes.ts).
    beforeEnter: redirectLegacySettingsTab,
  },
  {
    path: '/permissions/:family?',
    name: 'permissions',
    component: () => import('@/views/permissions/PermissionsView.vue'),
  },
  // artifacts-as-units-01DOGF0C WP06 (FR-7): Artifacts and Documents are one
  // Library surface with Captured and Authored views. The old paths redirect
  // into the matching view, keeping the query string (DocumentsView reads
  // ?session=), so bookmarks, palette history and a persisted lastRoute of
  // either old path still land in the right place.
  { path: '/library', redirect: (to) => ({ path: '/library/captured', query: to.query }) },
  {
    path: '/library/:view(captured|authored)',
    name: 'library',
    component: () => import('@/views/library/LibraryView.vue'),
  },
  { path: '/artifacts', redirect: (to) => ({ path: '/library/captured', query: to.query }) },
  { path: '/documents', redirect: (to) => ({ path: '/library/authored', query: to.query }) },
  {
    // FR-002 (01NKNOW01): Corpora surface retired; redirect to Contexts,
    // which knowledge-home-01DOGF0E made Knowledge › Curated.
    path: '/corpora/:pathMatch(.*)*',
    redirect: '/knowledge/curated',
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
  // install-framework-01DOGF0B Phase 4 WP08/WP09: the Marketplace folded
  // into the Capabilities surface (/tools) and MarketplaceView was deleted.
  // Old links, palette history and a persisted lastRoute land there, query
  // kept (?kind= picks a kind chip). Mirrored in main-served.ts.
  { path: '/marketplace', redirect: (to) => ({ path: '/tools', query: to.query }) },
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
// without it the Sites nav entry, the three Publish-to-team
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
      const ok = await initSentry({
        tier,
        dsn: settings.sentryDsn,
        release: info.build ?? undefined,
        gitsha: info.commit ?? undefined,
        app,
      });
      // entry-points-and-crash-reporting-01PMZD13 UNIT-7: the return
      // value was previously discarded entirely (`await initSentry(...)`
      // with no assignment) — a false `initSentry` had no way to leave a
      // trace anywhere. `ok` still does not block app mount either way;
      // it only becomes visible.
      if (!ok) {
        logEvent('warn', 'sentry.init_returned_false', { tier });
      }
    }
  } catch (err) {
    // Sentry init failure must never block app mount — but it must not
    // vanish silently either. Previously this catch was empty.
    logEvent('warn', 'sentry.init_threw', {
      message: err instanceof Error ? err.message : String(err),
    });
  }
})();

app.mount('#app');
