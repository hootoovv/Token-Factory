import { createRouter, createWebHistory } from "vue-router";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      component: () => import("../components/Layout.vue"),
      children: [
        {
          path: "",
          name: "Dashboard",
          component: () => import("../views/Dashboard.vue"),
        },
        {
          // 3.4 修复：新增"我的仪表板"路由，需要登录后才能访问
          path: "dashboard",
          name: "UserDashboard",
          component: () => import("../views/UserDashboard.vue"),
          meta: { requiresAuth: true },
        },
        {
          path: "login",
          name: "Login",
          component: () => import("../views/Login.vue"),
        },
        {
          path: "admin",
          name: "Admin",
          component: () => import("../views/admin/Layout.vue"),
          meta: { requiresAuth: true, requiresAdmin: true },
          children: [
            { path: "", redirect: "/admin/users" },
            {
              path: "users",
              name: "AdminUsers",
              component: () => import("../views/admin/Users.vue"),
            },
            {
              path: "providers",
              name: "AdminProviders",
              component: () => import("../views/admin/Providers.vue"),
            },
            {
              path: "models",
              name: "AdminModels",
              component: () => import("../views/admin/Models.vue"),
            },
            {
              path: "stats",
              name: "AdminStats",
              component: () => import("../views/admin/Stats.vue"),
            },
            {
              path: "audit-logs",
              name: "AdminAuditLogs",
              component: () => import("../views/admin/AuditLogs.vue"),
            },
            {
              path: "call-records",
              name: "AdminCallRecords",
              component: () => import("../views/admin/CallRecords.vue"),
            },
          ],
        },
        {
          path: "user",
          name: "User",
          component: () => import("../views/user/Layout.vue"),
          meta: { requiresAuth: true },
          children: [
            { path: "", redirect: "/user/api-keys" },
            {
              path: "api-keys",
              name: "UserApiKeys",
              component: () => import("../views/user/ApiKeys.vue"),
            },
            {
              path: "usage",
              name: "UserUsage",
              component: () => import("../views/user/Usage.vue"),
            },
            {
              path: "stats",
              name: "UserStats",
              component: () => import("../views/user/MyStats.vue"),
            },
          ],
        },
      ],
    },
  ],
});

// 路由守卫
// 5.5 修复：解析JWT的过期时间，提前拦截过期Token
function isTokenExpired(token: string): boolean {
  try {
    // JWT格式: header.payload.signature，解码payload部分
    const parts = token.split(".");
    if (parts.length !== 3) return true;
    // Base64Url解码payload
    const payload = JSON.parse(
      atob(parts[1].replace(/-/g, "+").replace(/_/g, "/")),
    );
    if (!payload.exp) return false; // 无exp字段视为不过期
    // exp是Unix时间戳（秒），与当前时间比较
    return payload.exp * 1000 < Date.now();
  } catch {
    return true; // 解析失败视为已过期
  }
}

router.beforeEach((to, _from, next) => {
  const token = localStorage.getItem("token");
  if (to.meta.requiresAuth && !token) {
    next({ name: "Login", query: { redirect: to.fullPath } });
    return;
  }
  // 5.5 修复：Token存在但已过期时，清除本地存储并跳转登录页
  if (to.meta.requiresAuth && token && isTokenExpired(token)) {
    localStorage.removeItem("token");
    localStorage.removeItem("role");
    next({ name: "Login", query: { redirect: to.fullPath } });
    return;
  }
  if (to.meta.requiresAdmin) {
    const role = localStorage.getItem("role");
    if (role !== "admin") {
      next({ name: "Dashboard" });
      return;
    }
  }
  next();
});

export default router;
