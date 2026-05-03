import { createApp } from 'vue';
import App from './App.vue';
import { createRouter, createWebHashHistory } from 'vue-router';

import './styles/global.css';

import { installHarnessClient } from '@/lib/harnessClientContext';
import { createHarnessClient } from '@/lib/harnessClient';

// Placeholder routes — primary surfaces (sessions/tools/bundles/providers/audit/settings)
// land in downstream missions consuming this chassis.
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
      // /search opens the sessions view with the search modal overlay.
      // The modal is rendered by Shell.vue and triggered by the
      // route-change guard below; this route makes the sidebar link work
      // and keeps the URL bookmarkable.
      path: '/search',
      name: 'search',
      component: () => import('@/views/sessions/SessionsView.vue'),
    },
  ],
});

const app = createApp(App);
app.use(router);
installHarnessClient(app, createHarnessClient());
app.mount('#app');
