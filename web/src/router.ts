import { createRouter, createWebHistory } from 'vue-router'
import AppShell from './components/AppShell.vue'
import LoginView from './views/LoginView.vue'
import OverviewView from './views/OverviewView.vue'
import UsersView from './views/UsersView.vue'
import UserDetailView from './views/UserDetailView.vue'
import RequestsView from './views/RequestsView.vue'
import RequestDetailView from './views/RequestDetailView.vue'
import AuditView from './views/AuditView.vue'
import SettingsView from './views/SettingsView.vue'
import { getToken } from './api/token'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: { public: true },
    },
    {
      path: '/',
      component: AppShell,
      children: [
        { path: '', name: 'overview', component: OverviewView },
        { path: 'users', name: 'users', component: UsersView },
        { path: 'users/:id', name: 'user', component: UserDetailView, props: true },
        { path: 'requests', name: 'requests', component: RequestsView },
        { path: 'requests/:id', name: 'request', component: RequestDetailView, props: true },
        { path: 'audit', name: 'audit', component: AuditView },
        { path: 'settings', name: 'settings', component: SettingsView },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach((to) => {
  const authenticated = Boolean(getToken())
  if (!to.meta.public && !authenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && authenticated) {
    return { name: 'overview' }
  }
})

export default router
