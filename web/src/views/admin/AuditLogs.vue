<template>
  <div>
    <div class="page-header">
      <h3>审计日志</h3>
    </div>

    <!-- 过滤器 -->
    <div class="filter-bar">
      <el-select v-model="filters.action" placeholder="操作类型" clearable style="width: 140px" @change="fetchLogs">
        <el-option label="创建" value="create" />
        <el-option label="删除" value="delete" />
        <el-option label="更新" value="update" />
        <el-option label="登录" value="login" />
      </el-select>

      <el-select v-model="filters.target_type" placeholder="对象类型" clearable style="width: 140px" @change="fetchLogs">
        <el-option label="API Key" value="api_key" />
        <el-option label="用户" value="user" />
        <el-option label="供应商" value="provider" />
        <el-option label="模型" value="model" />
        <el-option label="映射" value="model_provider" />
      </el-select>

      <el-input v-model="filters.operator_name" placeholder="操作者用户名" clearable style="width: 180px" @clear="fetchLogs"
        @keyup.enter="fetchLogs" />

      <el-date-picker v-model="filters.start_time" type="date" placeholder="开始日期" format="YYYY-MM-DD"
        value-format="YYYY-MM-DD" style="width: 150px" @change="fetchLogs" />

      <el-date-picker v-model="filters.end_time" type="date" placeholder="结束日期" format="YYYY-MM-DD"
        value-format="YYYY-MM-DD" style="width: 150px" @change="fetchLogs" />

      <el-button type="primary" @click="fetchLogs">
        <el-icon>
          <Search />
        </el-icon> 查询
      </el-button>

      <el-button @click="resetFilters">
        <el-icon>
          <RefreshRight />
        </el-icon> 重置
      </el-button>
    </div>

    <!-- 数据表格 -->
    <el-table :data="logs" stripe v-loading="loading">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="operator_name" label="操作者" width="120" />
      <el-table-column prop="action" label="操作类型" width="100">
        <template #default="scope">
          <el-tag :type="getActionTagType(scope.row.action)" size="small">
            {{ getActionLabel(scope.row.action) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="target_type" label="对象类型" width="110">
        <template #default="scope">
          {{ getTargetTypeLabel(scope.row.target_type) }}
        </template>
      </el-table-column>
      <el-table-column prop="target_id" label="对象ID" width="80" />
      <el-table-column prop="detail" label="操作详情" min-width="280" show-overflow-tooltip />
      <el-table-column prop="ip_address" label="IP地址" width="140" />
      <el-table-column prop="created_at" label="操作时间" width="180">
        <template #default="scope">
          {{ formatDate(scope.row.created_at) }}
        </template>
      </el-table-column>
    </el-table>

    <!-- 分页 -->
    <div class="pagination-wrapper">
      <el-pagination v-model:current-page="currentPage" v-model:page-size="pageSize" :page-sizes="[10, 20, 50, 100]"
        :total="total" layout="total, sizes, prev, pager, next" @size-change="fetchLogs" @current-change="fetchLogs" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminAuditLogApi } from '../../api'

const logs = ref<any[]>([])
const loading = ref(false)
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const filters = ref({
  action: '',
  target_type: '',
  operator_name: '',
  start_time: '',
  end_time: '',
})

function formatDate(d: string): string {
  if (!d) return ''
  return new Date(d).toLocaleString('zh-CN')
}

function getActionLabel(action: string): string {
  const map: Record<string, string> = {
    create: '创建',
    delete: '删除',
    update: '更新',
    login: '登录',
  }
  return map[action] || action
}

function getActionTagType(action: string): string {
  const map: Record<string, string> = {
    create: 'success',
    delete: 'danger',
    update: 'warning',
    login: 'info',
  }
  return map[action] || ''
}

function getTargetTypeLabel(targetType: string): string {
  const map: Record<string, string> = {
    api_key: 'API Key',
    user: '用户',
    provider: '供应商',
    model: '模型',
    model_provider: '映射',
  }
  return map[targetType] || targetType
}

function resetFilters() {
  filters.value = {
    action: '',
    target_type: '',
    operator_name: '',
    start_time: '',
    end_time: '',
  }
  currentPage.value = 1
  fetchLogs()
}

async function fetchLogs() {
  loading.value = true
  try {
    const params: Record<string, string | number> = {
      page: currentPage.value,
      page_size: pageSize.value,
    }
    if (filters.value.action) params.action = filters.value.action
    if (filters.value.target_type) params.target_type = filters.value.target_type
    if (filters.value.operator_name) params.operator_name = filters.value.operator_name
    if (filters.value.start_time) params.start_time = filters.value.start_time
    if (filters.value.end_time) params.end_time = filters.value.end_time

    const res = await adminAuditLogApi.list(params)
    logs.value = res.data?.items || []
    total.value = res.data?.total || 0
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchLogs()
})
</script>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.page-header h3 {
  margin: 0;
}

.filter-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 20px;
  align-items: center;
}

.pagination-wrapper {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
