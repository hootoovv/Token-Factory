<template>
  <div class="dashboard-container">
    <!-- 3.4 修复：用户仪表板 - 显示该用户自己的数据，支持模型/供应商/用户过滤 -->
    <div class="dashboard-header">
      <div class="header-title">
        <h2>{{ userStore.isAdmin ? '管理仪表板' : '我的仪表板' }}</h2>
        <p class="subtitle">{{ userStore.isAdmin ? '查看所有用户的使用数据' : '查看您的API使用数据' }}</p>
      </div>
      <div class="filter-bar">
        <el-radio-group v-model="timeFilter" @change="onFilterChange" size="small">
          <el-radio-button label="1h">1小时</el-radio-button>
          <el-radio-button label="today">今天</el-radio-button>
          <el-radio-button label="week">本周</el-radio-button>
          <el-radio-button label="month">本月</el-radio-button>
        </el-radio-group>
        <!-- 3.4 修复：用户和管理员均可使用模型和供应商过滤器 -->
        <el-select v-model="modelFilter" placeholder="全部模型" clearable size="small"
          style="width: 160px; margin-left: 12px;" @change="onFilterChange">
          <el-option v-for="m in allModels" :key="m.id" :label="m.name" :value="m.id" />
        </el-select>
        <el-select v-model="providerFilter" placeholder="全部供应商" clearable size="small"
          style="width: 160px; margin-left: 12px;" @change="onFilterChange">
          <el-option v-for="p in allProviders" :key="p.id" :label="p.name" :value="p.id" />
        </el-select>
        <!-- 3.4 修复：管理员额外显示用户过滤器 -->
        <el-select v-if="userStore.isAdmin" v-model="userFilter" placeholder="全部用户" clearable size="small"
          style="width: 160px; margin-left: 12px;" @change="onFilterChange">
          <el-option v-for="u in allUsers" :key="u.id" :label="u.display_name || u.username" :value="u.id" />
        </el-select>
        <el-button text size="small" @click="$router.push('/')" style="margin-left: 12px;">
          <el-icon>
            <HomeFilled />
          </el-icon> 返回主页
        </el-button>
      </div>
    </div>

    <!-- 当前过滤提示 -->
    <div v-if="modelFilter || providerFilter || userFilter" class="filter-tip">
      <span>已过滤：</span>
      <el-tag v-if="modelFilter" closable size="small" @close="clearModelFilter">
        模型: {{ getModelName(modelFilter) }}
      </el-tag>
      <el-tag v-if="providerFilter" closable size="small" type="warning" @close="clearProviderFilter"
        style="margin-left: 6px;">
        供应商: {{ getProviderName(providerFilter) }}
      </el-tag>
      <el-tag v-if="userFilter && userStore.isAdmin" closable size="small" type="danger" @close="clearUserFilter"
        style="margin-left: 6px;">
        用户: {{ getUserName(userFilter) }}
      </el-tag>
      <el-button text size="small" @click="clearAllFilters" style="margin-left: 8px;">清除全部</el-button>
    </div>

    <!-- 统计卡片 -->
    <el-row :gutter="20" class="stats-row">
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);">
            <el-icon :size="28">
              <Phone />
            </el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-value">{{ formatNumber(stats.total_calls) }}</div>
            <div class="stat-label">总调用次数</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" style="background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);">
            <el-icon :size="28">
              <Upload />
            </el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-value">{{ formatBytes(stats.total_input_bytes) }}</div>
            <div class="stat-label">输入数据量</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" style="background: linear-gradient(135deg, #4facfe 0%, #00f2fe 100%);">
            <el-icon :size="28">
              <Download />
            </el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-value">{{ formatBytes(stats.total_output_bytes) }}</div>
            <div class="stat-label">输出数据量</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" style="background: linear-gradient(135deg, #43e97b 0%, #38f9d7 100%);">
            <el-icon :size="28">
              <Timer />
            </el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-value">{{ stats.avg_duration?.toFixed(0) || 0 }} ms</div>
            <div class="stat-label">平均单次耗时</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 排行 -->
    <el-row :gutter="20" class="charts-row">
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>模型使用排行</span>
              <el-tag size="small" type="info">TOP 10</el-tag>
            </div>
          </template>
          <div class="ranking-list">
            <div v-for="(item, idx) in modelRanking" :key="item.id" class="ranking-item">
              <div class="ranking-left">
                <span class="ranking-index" :class="idx < 3 ? 'top3' : ''">{{ idx + 1 }}</span>
                <span class="ranking-name">{{ item.name }}</span>
              </div>
              <div class="ranking-right">
                <el-progress :percentage="getPercentage(item.count, modelRanking)" :stroke-width="8" :show-text="false"
                  style="width: 120px;" />
                <span class="ranking-count">{{ formatNumber(item.count) }}</span>
              </div>
            </div>
            <el-empty v-if="modelRanking.length === 0" description="暂无数据" :image-size="60" />
          </div>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>供应商使用排行</span>
              <el-tag size="small" type="info">TOP 10</el-tag>
            </div>
          </template>
          <div class="ranking-list">
            <div v-for="(item, idx) in providerRanking" :key="item.id" class="ranking-item">
              <div class="ranking-left">
                <span class="ranking-index" :class="idx < 3 ? 'top3' : ''">{{ idx + 1 }}</span>
                <span class="ranking-name">{{ item.name }}</span>
              </div>
              <div class="ranking-right">
                <el-progress :percentage="getPercentage(item.count, providerRanking)" :stroke-width="8"
                  :show-text="false" style="width: 120px;" color="#409eff" />
                <span class="ranking-count">{{ formatNumber(item.count) }}</span>
              </div>
            </div>
            <el-empty v-if="providerRanking.length === 0" description="暂无数据" :image-size="60" />
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { myDashboardApi } from '../api'
import { useUserStore } from '../stores/user'
import { useRouter } from 'vue-router'

const userStore = useUserStore()
const router = useRouter()

const timeFilter = ref('1h')
const modelFilter = ref(null as number | null)
const providerFilter = ref(null as number | null)
const userFilter = ref(null as number | null) // 仅管理员使用

// 所有模型/供应商/用户列表（用于下拉框选项）
const allModels = ref<any[]>([])
const allProviders = ref<any[]>([])
const allUsers = ref<any[]>([]) // 仅管理员有数据

const stats = ref({
  total_calls: 0,
  total_input_bytes: 0,
  total_output_bytes: 0,
  avg_duration: 0,
})

const modelRanking = ref<any[]>([])
const providerRanking = ref<any[]>([])

// 获取模型/供应商列表（用于过滤下拉框）
async function fetchFilterOptions() {
  try {
    const [modelsRes, providersRes] = await Promise.all([
      myDashboardApi.models(),
      myDashboardApi.providers(),
    ])
    allModels.value = modelsRes.data || []
    allProviders.value = providersRes.data || []

    // 管理员额外加载用户列表
    if (userStore.isAdmin) {
      try {
        const usersRes = await myDashboardApi.users()
        allUsers.value = usersRes.data || []
      } catch (e) {
        console.error('获取用户列表失败', e)
      }
    }
  } catch (e) {
    console.error(e)
  }
}

function getModelName(id: number): string {
  return allModels.value.find(m => m.id === id)?.name || `#${id}`
}

function getProviderName(id: number): string {
  return allProviders.value.find(p => p.id === id)?.name || `#${id}`
}

function getUserName(id: number): string {
  return allUsers.value.find(u => u.id === id)?.display_name || allUsers.value.find(u => u.id === id)?.username || `#${id}`
}

// 带过滤参数的数据获取
async function fetchStats() {
  try {
    const res = await myDashboardApi.stats(timeFilter.value, modelFilter.value, providerFilter.value, userFilter.value)
    stats.value = res.data
  } catch (e) {
    console.error(e)
  }
}

async function fetchModelRanking() {
  try {
    const res = await myDashboardApi.modelRanking(timeFilter.value, modelFilter.value, providerFilter.value, userFilter.value)
    modelRanking.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

async function fetchProviderRanking() {
  try {
    const res = await myDashboardApi.providerRanking(timeFilter.value, modelFilter.value, providerFilter.value, userFilter.value)
    providerRanking.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

function fetchAllData() {
  fetchStats()
  fetchModelRanking()
  fetchProviderRanking()
}

function onFilterChange() {
  fetchAllData()
}

function clearModelFilter() {
  modelFilter.value = null
  fetchAllData()
}

function clearProviderFilter() {
  providerFilter.value = null
  fetchAllData()
}

function clearUserFilter() {
  userFilter.value = null
  fetchAllData()
}

function clearAllFilters() {
  modelFilter.value = null
  providerFilter.value = null
  userFilter.value = null
  fetchAllData()
}

function formatNumber(n: number): string {
  if (!n) return '0'
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return n.toString()
}

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return size.toFixed(1) + ' ' + units[i]
}

function getPercentage(count: number, list: any[]): number {
  const max = list.reduce((m, item) => Math.max(m, item.count), 0)
  if (max === 0) return 0
  return Math.round((count / max) * 100)
}

onMounted(() => {
  // 3.4 修复：仪表板需要登录才能访问
  if (!userStore.isLoggedIn) {
    router.push('/login')
    return
  }
  fetchFilterOptions()
  fetchAllData()
  // 自动刷新
  const timer = setInterval(fetchAllData, 60000)
  return () => {
    clearInterval(timer)
  }
})
</script>

<style scoped>
.dashboard-container {
  max-width: 1400px;
  margin: 0 auto;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.header-title h2 {
  margin: 0;
  font-size: 24px;
  color: #303133;
}

.subtitle {
  margin: 4px 0 0;
  color: #909399;
  font-size: 14px;
}

.filter-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0;
}

.filter-tip {
  display: flex;
  align-items: center;
  margin-bottom: 16px;
  padding: 8px 12px;
  background: #f4f4f5;
  border-radius: 6px;
  font-size: 13px;
  color: #606266;
}

.stats-row {
  margin-bottom: 20px;
}

.stat-card {
  display: flex;
  align-items: center;
  padding: 0;
}

.stat-card :deep(.el-card__body) {
  display: flex;
  align-items: center;
  padding: 20px;
  width: 100%;
}

.stat-icon {
  width: 56px;
  height: 56px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  margin-right: 16px;
  flex-shrink: 0;
}

.stat-info {
  flex: 1;
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: #303133;
}

.stat-label {
  font-size: 13px;
  color: #909399;
  margin-top: 4px;
}

.charts-row {
  margin-bottom: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
}

.ranking-list {
  min-height: 300px;
}

.ranking-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 0;
  border-bottom: 1px solid #f0f0f0;
}

.ranking-item:last-child {
  border-bottom: none;
}

.ranking-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.ranking-index {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
  background: #f0f0f0;
  color: #909399;
}

.ranking-index.top3 {
  background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
  color: #fff;
}

.ranking-name {
  font-size: 14px;
  color: #303133;
}

.ranking-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.ranking-count {
  font-size: 14px;
  font-weight: 600;
  color: #606266;
  min-width: 60px;
  text-align: right;
}
</style>
