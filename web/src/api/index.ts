import axios from "axios";
import { ElMessage } from "element-plus";
import router from "../router";

const api = axios.create({
  baseURL: "/api",
  timeout: 30000,
});

// ==================== API Key 传输加密/解密 ====================

/**
 * decryptFromTransmission 使用XOR+Base64解密后端传输的加密API Key
 * 与后端 encryptForTransmission 函数配对使用
 * @param encrypted 后端返回的 encrypted_key 字段值（Base64编码的XOR密文）
 * @param key 传输密钥 transmission_key
 * @returns 解密后的明文API Key
 */
export function decryptFromTransmission(
  encrypted: string,
  key: string,
): string {
  if (!encrypted || !key) return encrypted || "";
  try {
    const keyBytes = new TextEncoder().encode(key);
    const bytes = Uint8Array.from(atob(encrypted), (c) => c.charCodeAt(0));
    const result = new Uint8Array(bytes.length);
    for (let i = 0; i < bytes.length; i++) {
      result[i] = bytes[i] ^ keyBytes[i % keyBytes.length];
    }
    return new TextDecoder().decode(result);
  } catch (e) {
    console.error("解密API Key失败:", e);
    return "";
  }
}

// 请求拦截器 - 自动附加Token
api.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem("token");
    if (token) {
      config.headers.Authorization = `Bearer ${token}`;
    }
    return config;
  },
  (error) => Promise.reject(error),
);

// 响应拦截器 - 处理错误
api.interceptors.response.use(
  (response) => response,
  (error) => {
    const msg = error.response?.data?.error || "请求失败";
    if (error.response?.status === 401) {
      localStorage.removeItem("token");
      localStorage.removeItem("role");
      localStorage.removeItem("username");
      localStorage.removeItem("displayName");
      localStorage.removeItem("userId");
      router.push("/login");
      ElMessage.error("登录已过期，请重新登录");
    } else if (error.response?.status === 403) {
      ElMessage.error("没有权限执行此操作");
    } else {
      ElMessage.error(msg);
    }
    return Promise.reject(error);
  },
);

// ==================== 认证 ====================
export const authApi = {
  login: (data: { username: string; password: string }) =>
    api.post("/login", data),
  me: () => api.get("/me"),
  getTransmissionKey: () => api.get("/transmission-key"), // 页面刷新时重新获取传输密钥
};

// ==================== Dashboard（公开 - 主页使用） ====================
export const dashboardApi = {
  stats: (timeFilter: string) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    return api.get("/dashboard/stats", { params });
  },
  modelRanking: (timeFilter: string) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    return api.get("/dashboard/model-ranking", { params });
  },
  providerRanking: (timeFilter: string) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    return api.get("/dashboard/provider-ranking", { params });
  },
  providerStatus: () => api.get("/dashboard/provider-status"),
  models: () => api.get("/dashboard/models"),
  providers: () => api.get("/dashboard/providers"),
};

// ==================== 3.4 修复：用户仪表板（认证后，显示用户自己的数据） ====================
export const myDashboardApi = {
  stats: (
    timeFilter: string,
    modelId?: number | null,
    providerId?: number | null,
    userId?: number | null,
  ) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    if (modelId) params.model_id = String(modelId);
    if (providerId) params.provider_id = String(providerId);
    if (userId) params.user_id = String(userId);
    return api.get("/my/dashboard/stats", { params });
  },
  modelRanking: (
    timeFilter: string,
    modelId?: number | null,
    providerId?: number | null,
    userId?: number | null,
  ) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    if (modelId) params.model_id = String(modelId);
    if (providerId) params.provider_id = String(providerId);
    if (userId) params.user_id = String(userId);
    return api.get("/my/dashboard/model-ranking", { params });
  },
  providerRanking: (
    timeFilter: string,
    modelId?: number | null,
    providerId?: number | null,
    userId?: number | null,
  ) => {
    const params: Record<string, string> = { time_filter: timeFilter };
    if (modelId) params.model_id = String(modelId);
    if (providerId) params.provider_id = String(providerId);
    if (userId) params.user_id = String(userId);
    return api.get("/my/dashboard/provider-ranking", { params });
  },
  users: () => api.get("/my/dashboard/users"), // 管理员获取用户列表用于过滤
  models: () => api.get("/dashboard/models"),
  providers: () => api.get("/dashboard/providers"),
};

// ==================== 用户接口 ====================
export const userApi = {
  listApiKeys: () => api.get("/api-keys"),
  createApiKey: (data: { name?: string; key?: string }) =>
    api.post("/api-keys", data),
  deleteApiKey: (id: number) => api.delete(`/api-keys/${id}`),
  usage: (params: { time_filter: string; page?: number; page_size?: number }) =>
    api.get("/usage", { params }),
  stats: (timeFilter: string) =>
    api.get("/usage/stats", { params: { time_filter: timeFilter } }),
  changePassword: (data: { old_password: string; new_password: string }) =>
    api.put("/password", data),
};

// ==================== 管理员 - 用户管理 ====================
export const adminUserApi = {
  list: (params?: { page?: number; page_size?: number }) =>
    api.get("/users", { params }),
  create: (data: {
    username: string;
    password: string;
    role?: string;
    display_name?: string;
  }) => api.post("/users", data),
  update: (id: number, data: any) => api.put(`/users/${id}`, data),
  delete: (id: number) => api.delete(`/users/${id}`),
};

// ==================== 管理员 - 供应商管理 ====================
export const adminProviderApi = {
  list: (params?: { page?: number; page_size?: number }) =>
    api.get("/providers", { params }),
  create: (data: any) => api.post("/providers", data),
  update: (id: number, data: any) => api.put(`/providers/${id}`, data),
  delete: (id: number) => api.delete(`/providers/${id}`),
};

// ==================== 管理员 - 模型管理 ====================
export const adminModelApi = {
  list: (params?: { page?: number; page_size?: number }) =>
    api.get("/models", { params }),
  create: (data: any) => api.post("/models", data),
  update: (id: number, data: any) => api.put(`/models/${id}`, data),
  delete: (id: number) => api.delete(`/models/${id}`),
};

// ==================== 管理员 - 模型-供应商映射 ====================
export const adminModelProviderApi = {
  list: (params?: { page?: number; page_size?: number }) =>
    api.get("/model-providers", { params }),
  create: (data: {
    model_id: number;
    provider_id: number;
    provider_model_name: string;
  }) => api.post("/model-providers", data),
  delete: (id: number) => api.delete(`/model-providers/${id}`),
};

// ==================== 管理员 - 审计日志 ====================
export const adminAuditLogApi = {
  list: (params: {
    page?: number;
    page_size?: number;
    action?: string;
    target_type?: string;
    operator_name?: string;
    start_time?: string;
    end_time?: string;
  }) => api.get("/audit-logs", { params }),
};

// ==================== 管理员 - 统计 ====================
export const adminStatsApi = {
  overview: (timeFilter: string) =>
    api.get("/stats/overview", { params: { time_filter: timeFilter } }),
  reloadCache: () => api.post("/cache/reload"),
};

export default api;
