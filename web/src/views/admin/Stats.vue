<template>
  <div>
    <div class="page-header">
      <h3>使用统计</h3>
      <div class="filter-bar">
        <el-radio-group v-model="timeFilter" size="small" @change="fetchData">
          <el-radio-button label="1h">1小时</el-radio-button>
          <el-radio-button label="today">今天</el-radio-button>
          <el-radio-button label="week">本周</el-radio-button>
          <el-radio-button label="month">本月</el-radio-button>
        </el-radio-group>
        <el-button size="small" @click="reloadCache" style="margin-left: 12px;">
          <el-icon>
            <Refresh />
          </el-icon> 刷新缓存
        </el-button>
      </div>
    </div>

    <!-- 统计卡片 -->
    <el-row :gutter="16" class="stats-row">
      <el-col :span="6">
        <el-statistic title="总调用次数" :value="overview.stats?.total_calls || 0" />
      </el-col>
      <el-col :span="6">
        <el-statistic title="输入数据量" :value="formatBytes(overview.stats?.total_input_bytes || 0)" />
      </el-col>
      <el-col :span="6">
        <el-statistic title="输出数据量" :value="formatBytes(overview.stats?.total_output_bytes || 0)" />
      </el-col>
      <el-col :span="6">
        <el-statistic title="平均耗时(ms)" :value="Number(overview.stats?.avg_duration || 0).toFixed(0)" />
      </el-col>
    </el-row>

    <!-- 排行 -->
    <el-row :gutter="20" style="margin-top: 20px;">
      <el-col :span="8">
        <el-card shadow="never">
          <template #header><span>模型使用排行</span></template>
          <el-table :data="overview.model_ranking || []" stripe size="small">
            <el-table-column type="index" label="#" width="40" />
            <el-table-column prop="name" label="模型" />
            <el-table-column prop="count" label="调用次数" width="90" />
            <el-table-column label="数据量" width="100">
              <template #default="scope">
                {{ formatBytes(scope.row.input_bytes + scope.row.output_bytes) }}
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="never">
          <template #header><span>用户使用排行</span></template>
          <el-table :data="overview.user_ranking || []" stripe size="small">
            <el-table-column type="index" label="#" width="40" />
            <el-table-column prop="name" label="用户" />
            <el-table-column prop="count" label="调用次数" width="90" />
            <el-table-column label="数据量" width="100">
              <template #default="scope">
                {{ formatBytes(scope.row.input_bytes + scope.row.output_bytes) }}
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="never">
          <template #header><span>供应商使用排行</span></template>
          <el-table :data="overview.provider_ranking || []" stripe size="small">
            <el-table-column type="index" label="#" width="40" />
            <el-table-column prop="name" label="供应商" />
            <el-table-column prop="count" label="调用次数" width="90" />
            <el-table-column label="数据量" width="100">
              <template #default="scope">
                {{ formatBytes(scope.row.input_bytes + scope.row.output_bytes) }}
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminStatsApi } from '../../api'
import { ElMessage } from 'element-plus'

const timeFilter = ref('1h')
const overview = ref<any>({})

async function fetchData() {
  try {
    const res = await adminStatsApi.overview(timeFilter.value)
    overview.value = res.data || {}
  } catch (e) {
    console.error(e)
  }
}

async function reloadCache() {
  try {
    await adminStatsApi.reloadCache()
    ElMessage.success('缓存重载成功')
  } catch (e) { /* 已处理 */ }
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

.stats-row {
  background: #fff;
  border-radius: 8px;
  padding: 20px;
}
</style>
