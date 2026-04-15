<template>
  <div class="dashboard-container">
    <!-- 时间过滤 + 标题 -->
    <div class="dashboard-header">
      <div class="header-title">
        <h2>数据概览</h2>
        <p class="subtitle">企业级LLM API代理中心运行状态</p>
      </div>
      <div class="filter-bar">
        <el-radio-group v-model="timeFilter" @change="onTimeFilterChange" size="small">
          <el-radio-button label="1h">1小时</el-radio-button>
          <el-radio-button label="today">今天</el-radio-button>
          <el-radio-button label="week">本周</el-radio-button>
          <el-radio-button label="month">本月</el-radio-button>
        </el-radio-group>
        <el-select
          v-model="modelFilter"
          placeholder="全部模型"
          clearable
          size="small"
          style="width: 160px; margin-left: 12px;"
          @change="onModelFilterChange"
        >
          <el-option v-for="m in allModels" :key="m.id" :label="m.name" :value="m.id" />
        </el-select>
        <el-select
          v-model="providerFilter"
          placeholder="全部供应商"
          clearable
          size="small"
          style="width: 160px; margin-left: 12px;"
          @change="onProviderFilterChange"
        >
          <el-option v-for="p in allProviders" :key="p.id" :label="p.name" :value="p.id" />
        </el-select>
      </div>
    </div>

    <!-- 当前过滤提示 -->
    <div v-if="modelFilter || providerFilter" class="filter-tip">
      <span>已过滤：</span>
      <el-tag v-if="modelFilter" closable size="small" @close="clearModelFilter">
        模型: {{ getModelName(modelFilter) }}
      </el-tag>
      <el-tag v-if="providerFilter" closable size="small" type="warning" @close="clearProviderFilter" style="margin-left: 6px;">
        供应商: {{ getProviderName(providerFilter) }}
      </el-tag>
      <el-button text size="small" @click="clearAllFilters" style="margin-left: 8px;">清除全部</el-button>
    </div>

    <!-- 统计卡片 -->
    <el-row :gutter="20" class="stats-row">
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <div class="stat-icon" style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);">
            <el-icon :size="28"><Phone /></el-icon>
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
            <el-icon :size="28"><Upload /></el-icon>
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
            <el-icon :size="28"><Download /></el-icon>
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
            <el-icon :size="28"><Timer /></el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-value">{{ stats.avg_duration?.toFixed(0) || 0 }} ms</div>
            <div class="stat-label">平均单次耗时</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 排行和供应商状态 -->
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
                <el-progress :percentage="getPercentage(item.count, modelRanking)" :stroke-width="8" :show-text="false" style="width: 120px;" />
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
                <el-progress :percentage="getPercentage(item.count, providerRanking)" :stroke-width="8" :show-text="false" style="width: 120px;" color="#409eff" />
                <span class="ranking-count">{{ formatNumber(item.count) }}</span>
              </div>
            </div>
            <el-empty v-if="providerRanking.length === 0" description="暂无数据" :image-size="60" />
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 供应商实时状态 -->
    <el-card shadow="hover" class="provider-status-card">
      <template #header>
        <div class="card-header">
          <span>供应商实时状态</span>
          <el-button text size="small" @click="fetchProviderStatus">
            <el-icon><Refresh /></el-icon> 刷新
          </el-button>
        </div>
      </template>
      <el-table :data="providerStatus" stripe style="width: 100%">
        <el-table-column prop="name" label="供应商" width="200" />
        <el-table-column prop="status_text" label="状态" width="120">
          <template #default="scope">
            <el-tag :type="getStatusType(scope.row.status)" effect="dark" size="small">
              {{ scope.row.status_text }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态码" width="120" />
      </el-table>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { dashboardApi } from '../api'

const timeFilter = ref('1h')
const modelFilter = ref(null as number | null)
const providerFilter = ref(null as number | null)

// 所有模型/供应商列表（用于下拉框选项，不受过滤影响）
const allModels = ref<any[]>([])
const allProviders = ref<any[]>([])

const stats = ref({
  total_calls: 0,
  total_input_bytes: 0,
  total_output_bytes: 0,
  avg_duration: 0,
})

const modelRanking = ref<any[]>([])
const providerRanking = ref<any[]>([])
const providerStatus = ref<any[]>([])

// 获取模型/供应商列表（用于过滤下拉框）
async function fetchFilterOptions() {
  try {
    const [modelsRes, providersRes] = await Promise.all([
      dashboardApi.models(),
      dashboardApi.providers(),
    ])
    allModels.value = modelsRes.data || []
    allProviders.value = providersRes.data || []
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

// 带过滤参数的数据获取
async function fetchStats() {
  try {
    const res = await dashboardApi.stats(timeFilter.value, modelFilter.value, providerFilter.value)
    stats.value = res.data
  } catch (e) {
    console.error(e)
  }
}

async function fetchModelRanking() {
  try {
    const res = await dashboardApi.modelRanking(timeFilter.value, modelFilter.value, providerFilter.value)
    modelRanking.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

async function fetchProviderRanking() {
  try {
    const res = await dashboardApi.providerRanking(timeFilter.value, modelFilter.value, providerFilter.value)
    providerRanking.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

async function fetchProviderStatus() {
  try {
    const res = await dashboardApi.providerStatus()
    providerStatus.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

function fetchAllData() {
  fetchStats()
  fetchModelRanking()
  fetchProviderRanking()
}

// 过滤器变化处理
function onTimeFilterChange() {
  fetchAllData()
}

function onModelFilterChange() {
  fetchAllData()
}

function onProviderFilterChange() {
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

function clearAllFilters() {
  modelFilter.value = null
  providerFilter.value = null
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

function getStatusType(status: string): string {
  switch (status) {
    case 'active': return 'success'
    case 'cooldown': return 'warning'
    case 'arrears': return 'danger'
    default: return 'info'
  }
}

onMounted(() => {
  fetchFilterOptions()
  fetchAllData()
  fetchProviderStatus()
  // 自动刷新
  const timer = setInterval(fetchAllData, 60000)
  const statusTimer = setInterval(fetchProviderStatus, 30000)
  // 组件卸载时清除
  return () => {
    clearInterval(timer)
    clearInterval(statusTimer)
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
.provider-status-card {
  margin-bottom: 20px;
}
</style>