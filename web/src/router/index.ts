import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: () => import('@/views/DashboardView.vue'),
    },
    {
      path: '/projects',
      component: () => import('@/views/ProjectListView.vue'),
    },
    {
      path: '/projects/:id',
      component: () => import('@/views/ProjectDetailView.vue'),
      props: true,
    },
    {
      path: '/projects/:id/board',
      component: () => import('@/views/TaskBoardView.vue'),
      props: true,
    },
    {
      path: '/projects/:id/hitl',
      component: () => import('@/views/HITLView.vue'),
      props: true,
    },
    {
      path: '/login',
      component: () => import('@/views/LoginView.vue'),
    },
    {
      path: '/settings',
      component: () => import('@/views/SettingsView.vue'),
    },
  ],
})

export default router
