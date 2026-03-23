import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    path: '/signing',
    name: 'Signing',
    component: () => import('shell/app-layout'),
    redirect: '/signing/submissions',
    meta: {
      order: 2030,
      icon: 'lucide:pen-tool',
      title: 'signing.menu.signing',
      keepAlive: true,
      authority: ['platform:admin', 'tenant:manager'],
    },
    children: [
      {
        path: 'submissions',
        name: 'SigningSubmissions',
        meta: {
          icon: 'lucide:send',
          title: 'signing.menu.submissions',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/submission/index.vue'),
      },
      {
        path: 'templates',
        name: 'SigningTemplates',
        meta: {
          icon: 'lucide:file-signature',
          title: 'signing.menu.templates',
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/template/index.vue'),
      },
      {
        path: 'templates/:id/builder',
        name: 'SigningTemplateBuilder',
        meta: {
          title: 'signing.menu.templates',
          hideInMenu: true,
          authority: ['platform:admin', 'tenant:manager'],
        },
        component: () => import('./views/template/builder/index.vue'),
      },
      {
        path: 'certificates',
        name: 'SigningCertificates',
        meta: {
          icon: 'lucide:shield-check',
          title: 'signing.menu.certificates',
          authority: ['platform:admin'],
        },
        component: () => import('./views/certificate/index.vue'),
      },
    ],
  },
  // Signing session — separate top-level route (no admin layout, requires login)
  {
    path: '/signing/session/:token',
    name: 'SigningSession',
    meta: {
      hideInMenu: true,
      hideInTab: true,
      hideInBreadcrumb: true,
      title: 'Sign Document',
      authority: ['platform:admin', 'tenant:manager', 'tenant:member'],
    },
    component: () => import('./views/session/index.vue'),
  },
];

export default routes;
