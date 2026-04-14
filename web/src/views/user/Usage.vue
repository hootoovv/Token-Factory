<template>
  <div>
    <div class="page-header">
      <h3>使用记录</h3>
      <div class="filter-bar">
        <el-radio-group v-model="timeFilter" size="small" @change="fetchData">
          <el-radio-button label="1h">1小时</el-radio-button>
          <el-radio-button label="today">今天</el-radio-button>
          <el-radio-button label="week">本周</el-radio-button>
          <el-radio-button label="month">本月</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <el-table :data="records" stripe>
      <el-table-column prop="model_name" label="模型" width="150" />
      <el-table-column prop="provider_name" label="供应商" width="130" />
      <el-table-column label="输入" width="100">
        <template #default="scope">
          {{ formatBytes(scope.row.input_bytes) }}
        </template>
      </el-table-column>
      <el-table-column label="输出" width="100">
        <template #default="scope">
          {{ formatBytes(scope.row.output_bytes) }}
        </template>
      </el-table-column>
      <el-table-column prop="duration" label="耗时(ms)" width="100" />
      <el-table-column prop="status" label="状态" width="80">
        <template #default="scope">
          <el-tag :type="scope.row.status === 'success' ? 'success' : 'danger'" size="small">
            {{ scope.row.status === 'success' ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="时间" min-width="180">
        <template #default="scope">
          {{ formatDate(scope.row.created_at) }}
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-bar" v-if="total > 0">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="fetchData"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { userApi } from '../../api'

const timeFilter = ref('1h')
const records = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return size.toFixed(1) + ' ' + units[i]
}

function formatDate(d: string): string {
  if (!d) return ''
  return new Date(d).toLocaleString('zh-CN')
}

async function fetchData() {
  try {
    const res = await userApi.usage({
      time_filter: timeFilter.value,
      page: page.value,
      page_size: pageSize.value,
    })
    records.value = res.data?.items || []
    total.value = res.data?.total || 0
  } catch (e) {
    console.error(e)
  }
}

onMounted(() => {
  fetchData()
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
  align-items: center;
}
.pagination-bar {
  margin-top: 16px;
  display: flex;
  justify-content: center;
}
</style>
