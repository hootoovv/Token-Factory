import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: () => import('../components/Layout.vue'),
      children: [
        {
          path: '',
          name: 'Dashboard',
          component: () => import('../views/Dashboard.vue'),
        },
        {
          // 3.4 修复：新增"我的仪表板"路由，需要登录后才能访问
          path: 'dashboard',
          name: 'UserDashboard',
          component: () => import('../views/UserDashboard.vue'),
          meta: { requiresAuth: true },
        },
        {
          path: 'login',
          name: 'Login',
          component: () => import('../views/Login.vue'),
        },
        {
          path: 'admin',
          name: 'Admin',
          component: () => import('../views/admin/Layout.vue'),
          meta: { requiresAuth: true, requiresAdmin: true },
          children: [
            { path: '', redirect: '/admin/users' },
            { path: 'users', name: 'AdminUsers', component: () => import('../views/admin/Users.vue') },
            { path: 'providers', name: 'AdminProviders', component: () => import('../views/admin/Providers.vue') },
            { path: 'models', name: 'AdminModels', component: () => import('../views/admin/Models.vue') },
            { path: 'stats', name: 'AdminStats', component: () => import('../views/admin/Stats.vue') },
          ],
        },
        {
          path: 'user',
          name: 'User',
          component: () => import('../views/user/Layout.vue'),
          meta: { requiresAuth: true },
          children: [
            { path: '', redirect: '/user/api-keys' },
            { path: 'api-keys', name: 'UserApiKeys', component: () => import('../views/user/ApiKeys.vue') },
            { path: 'usage', name: 'UserUsage', component: () => import('../views/user/Usage.vue') },
            { path: 'stats', name: 'UserStats', component: () => import('../views/user/MyStats.vue') },
          ],
        },
      ],
    },
  ],
})

// 路由守卫
router.beforeEach((to, _from, next) => {
  const token = localStorage.getItem('token')
  if (to.meta.requiresAuth && !token) {
    next({ name: 'Login', query: { redirect: to.fullPath } })
    return
  }
  if (to.meta.requiresAdmin) {
    const role = localStorage.getItem('role')
    if (role !== 'admin') {
      next({ name: 'Dashboard' })
      return
    }
  }
  next()
})

export default router
