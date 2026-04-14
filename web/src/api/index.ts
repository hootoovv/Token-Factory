import axios from 'axios'
import { ElMessage } from 'element-plus'
import router from '../router'

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

// 请求拦截器 - 自动附加Token
api.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// 响应拦截器 - 处理错误
api.interceptors.response.use(
  (response) => response,
  (error) => {
    const msg = error.response?.data?.error || '请求失败'
    if (error.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('role')
      localStorage.removeItem('username')
      router.push('/login')
      ElMessage.error('登录已过期，请重新登录')
    } else if (error.response?.status === 403) {
      ElMessage.error('没有权限执行此操作')
    } else {
      ElMessage.error(msg)
    }
    return Promise.reject(error)
  }
)

// ==================== 认证 ====================
export const authApi = {
  login: (data: { username: string; password: string }) => api.post('/login', data),
  me: () => api.get('/me'),
}

// ==================== Dashboard（公开） ====================
export const dashboardApi = {
  stats: (timeFilter: string) => api.get('/dashboard/stats', { params: { time_filter: timeFilter } }),
  modelRanking: (timeFilter: string) => api.get('/dashboard/model-ranking', { params: { time_filter: timeFilter } }),
  providerRanking: (timeFilter: string) => api.get('/dashboard/provider-ranking', { params: { time_filter: timeFilter } }),
  providerStatus: () => api.get('/dashboard/provider-status'),
}

// ==================== 用户接口 ====================
export const userApi = {
  listApiKeys: () => api.get('/api-keys'),
  createApiKey: (data: { name?: string }) => api.post('/api-keys', data),
  deleteApiKey: (id: number) => api.delete(`/api-keys/${id}`),
  usage: (params: { time_filter: string; page?: number; page_size?: number }) => api.get('/usage', { params }),
  stats: (timeFilter: string) => api.get('/usage/stats', { params: { time_filter: timeFilter } }),
  changePassword: (data: { old_password: string; new_password: string }) => api.put('/password', data),
}

// ==================== 管理员 - 用户管理 ====================
export const adminUserApi = {
  list: () => api.get('/users'),
  create: (data: { username: string; password: string; role?: string; display_name?: string }) => api.post('/users', data),
  update: (id: number, data: any) => api.put(`/users/${id}`, data),
  delete: (id: number) => api.delete(`/users/${id}`),
}

// ==================== 管理员 - 供应商管理 ====================
export const adminProviderApi = {
  list: () => api.get('/providers'),
  create: (data: any) => api.post('/providers', data),
  update: (id: number, data: any) => api.put(`/providers/${id}`, data),
  delete: (id: number) => api.delete(`/providers/${id}`),
}

// ==================== 管理员 - 模型管理 ====================
export const adminModelApi = {
  list: () => api.get('/models'),
  create: (data: any) => api.post('/models', data),
  update: (id: number, data: any) => api.put(`/models/${id}`, data),
  delete: (id: number) => api.delete(`/models/${id}`),
}

// ==================== 管理员 - 模型-供应商映射 ====================
export const adminModelProviderApi = {
  list: () => api.get('/model-providers'),
  create: (data: { model_id: number; provider_id: number; provider_model_name: string }) => api.post('/model-providers', data),
  delete: (id: number) => api.delete(`/model-providers/${id}`),
}

// ==================== 管理员 - 统计 ====================
export const adminStatsApi = {
  overview: (timeFilter: string) => api.get('/stats/overview', { params: { time_filter: timeFilter } }),
  reloadCache: () => api.post('/cache/reload'),
}

export default api
