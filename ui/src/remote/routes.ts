import type { RouteRecordRaw } from 'vue-router'
import '@/main.css'

const meta = { module: 'signing' }

// Routes mounted by the platform shell under their own error boundary.
export const routes: RouteRecordRaw[] = [
  { path: '/signing', name: 'signing-inbox', component: () => import('@/views/inbox/index.vue'), meta },
  { path: '/signing/sign/:signerId', name: 'signing-sign', component: () => import('@/views/sign/index.vue'), meta },
  { path: '/signing/submissions', name: 'signing-submissions', component: () => import('@/views/submissions/index.vue'), meta },
  { path: '/signing/submissions/:id', name: 'signing-submission', component: () => import('@/views/submissions/detail.vue'), meta },
  { path: '/signing/templates', name: 'signing-templates', component: () => import('@/views/templates/index.vue'), meta },
  { path: '/signing/templates/:id/builder', name: 'signing-builder', component: () => import('@/views/builder/index.vue'), meta },
  { path: '/signing/certificate', name: 'signing-certificate', component: () => import('@/views/certificate/index.vue'), meta },
  { path: '/signing/verify', name: 'signing-verify', component: () => import('@/views/verify/index.vue'), meta },
  { path: '/signing/certificates', name: 'signing-certificates', component: () => import('@/views/admin/index.vue'), meta },
]
export default routes
