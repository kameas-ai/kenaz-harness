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
      component: () => import('@/views/placeholder/PlaceholderView.vue'),
      props: { number: '02', section: 'TOOLS', title: 'Tools' },
    },
    {
      path: '/bundles',
      name: 'bundles',
      component: () => import('@/views/bundles/BundlesView.vue'),
    },
    {
      path: '/providers',
      name: 'providers',
      component: () => import('@/views/placeholder/PlaceholderView.vue'),
      props: { number: '04', section: 'PROVIDERS', title: 'Providers' },
    },
    {
      path: '/audit',
      name: 'audit',
      component: () => import('@/views/audit/AuditView.vue'),
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/views/placeholder/PlaceholderView.vue'),
      props: { number: '06', section: 'SETTINGS', title: 'Settings' },
    },
  ],
});

const app = createApp(App);
app.use(router);
installHarnessClient(app, createHarnessClient());
app.mount('#app');
