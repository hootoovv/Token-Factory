import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { authApi } from "../api";

export const useUserStore = defineStore("user", () => {
  // 这些字段持久化到 localStorage（关闭浏览器后仍存在）
  const token = ref(localStorage.getItem("token") || "");
  const username = ref(localStorage.getItem("username") || "");
  const role = ref(localStorage.getItem("role") || "");
  const displayName = ref(localStorage.getItem("displayName") || "");
  const userId = ref(Number(localStorage.getItem("userId")) || 0);

  // transmissionKey 仅存内存，不写入 localStorage
  // 原因：localStorage 可被 XSS 攻击读取，传输密钥应尽量缩短暴露时间
  // 页面刷新时通过 GET /api/transmission-key 重新获取
  const transmissionKey = ref("");

  const isLoggedIn = computed(() => !!token.value);
  const isAdmin = computed(() => role.value === "admin");

  async function login(user: string, password: string) {
    const res = await authApi.login({ username: user, password: password });
    const data = res.data;
    token.value = data.token;
    username.value = data.user.username;
    role.value = data.user.role;
    displayName.value = data.user.display_name || "";
    userId.value = data.user.id;
    // transmissionKey 仅存内存，不持久化
    transmissionKey.value = data.transmission_key || "";

    localStorage.setItem("token", data.token);
    localStorage.setItem("username", data.user.username);
    localStorage.setItem("role", data.user.role);
    localStorage.setItem("displayName", data.user.display_name || "");
    localStorage.setItem("userId", String(data.user.id));
    // 注意：不将 transmissionKey 写入 localStorage
  }

  function logout() {
    token.value = "";
    username.value = "";
    role.value = "";
    displayName.value = "";
    userId.value = 0;
    transmissionKey.value = "";
    localStorage.removeItem("token");
    localStorage.removeItem("username");
    localStorage.removeItem("role");
    localStorage.removeItem("displayName");
    localStorage.removeItem("userId");
    // transmissionKey 从不写入 localStorage，无需清除
  }

  return {
    token,
    username,
    role,
    displayName,
    userId,
    transmissionKey,
    isLoggedIn,
    isAdmin,
    login,
    logout,
  };
});
