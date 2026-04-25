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
      path: '/sessions',
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
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/settings/SettingsView.vue'),
    },
  ],
});

const app = createApp(App);
app.use(router);
installHarnessClient(app, createHarnessClient());
app.mount('#app');
