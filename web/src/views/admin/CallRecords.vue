<template>
  <div>
    <div class="page-header">
      <h3>调用记录</h3>
      <el-button @click="fetchRecords" :loading="loading">
        <el-icon><Refresh /></el-icon> 刷新
      </el-button>
    </div>

    <el-alert
      title="调用记录仅保存在内存中，最多保留最近 N 条（N 可在 config.yaml 中配置），重启服务后清空。"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 16px"
    />

    <!-- 数据表格 -->
    <el-table :data="records" stripe v-loading="loading" @row-click="showDetail" class="clickable-table">
      <el-table-column prop="id" label="#" width="60" />
      <el-table-column prop="time" label="时间" width="180">
        <template #default="scope">
          {{ formatDate(scope.row.time) }}
        </template>
      </el-table-column>
      <el-table-column prop="caller" label="调用者" width="120" />
      <el-table-column prop="model_name" label="模型名称" min-width="150" show-overflow-tooltip />
      <el-table-column label="输入数据量" width="120" align="right">
        <template #default="scope">
          {{ formatBytes(scope.row.input_data_size) }}
        </template>
      </el-table-column>
      <el-table-column label="输出数据量" width="120" align="right">
        <template #default="scope">
          {{ formatBytes(scope.row.output_data_size) }}
        </template>
      </el-table-column>
      <el-table-column label="总用时" width="100" align="right">
        <template #default="scope">
          {{ formatDuration(scope.row.total_duration) }}
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="80" align="center">
        <template #default="scope">
          <el-tag :type="scope.row.status === 'success' ? 'success' : 'danger'" size="small">
            {{ scope.row.status === 'success' ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
    </el-table>

    <!-- 详情对话框 -->
    <el-dialog v-model="detailVisible" title="调用详情" width="1100px" destroy-on-close>
      <template v-if="detailRecord">
        <el-descriptions :column="4" border size="small" style="margin-bottom: 16px">
          <el-descriptions-item label="时间">{{ formatDate(detailRecord.time) }}</el-descriptions-item>
          <el-descriptions-item label="调用者">{{ detailRecord.caller }}</el-descriptions-item>
          <el-descriptions-item label="模型名称">{{ detailRecord.model_name }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="detailRecord.status === 'success' ? 'success' : 'danger'" size="small">
              {{ detailRecord.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="供应商">{{ detailRecord.provider_name }}</el-descriptions-item>
          <el-descriptions-item label="供应商模型">{{ detailRecord.provider_model }}</el-descriptions-item>
          <el-descriptions-item label="输入数据量">{{ formatBytes(detailRecord.input_data_size) }}</el-descriptions-item>
          <el-descriptions-item label="输出数据量">{{ formatBytes(detailRecord.output_data_size) }}</el-descriptions-item>
          <el-descriptions-item label="总用时">{{ formatDuration(detailRecord.total_duration) }}</el-descriptions-item>
          <el-descriptions-item label="流式请求">
            <el-tag :type="detailRecord.is_stream ? 'primary' : 'info'" size="small">
              {{ detailRecord.is_stream ? '是' : '否' }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 输入输出左右布局 -->
        <div class="json-panels">
          <!-- 输入参数 -->
          <div class="json-panel">
            <div class="json-header">
              <span class="json-title">输入参数</span>
              <el-button size="small" text @click="copyJson(detailRecord.input_params)">
                <el-icon><CopyDocument /></el-icon> 复制
              </el-button>
            </div>
            <div class="json-block">
              <pre>{{ formatJson(detailRecord.input_params) }}</pre>
            </div>
          </div>

          <!-- 输出参数 -->
          <div class="json-panel">
            <div class="json-header">
              <span class="json-title">{{ detailRecord.is_stream ? '输出参数（流式聚合）' : '输出参数' }}</span>
              <el-button size="small" text @click="copyJson(detailRecord.output_params)">
                <el-icon><CopyDocument /></el-icon> 复制
              </el-button>
            </div>
            <div class="json-block">
              <pre>{{ detailRecord.output_params ? formatJson(detailRecord.output_params) : '（无输出数据）' }}</pre>
            </div>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { adminCallRecordApi } from '../../api'

interface CallRecord {
  id: number
  time: string
  caller: string
  model_name: string
  input_data_size: number
  output_data_size: number
  total_duration: number
  status: string
  input_params: string
  output_params: string
  provider_name: string
  provider_model: string
  is_stream: boolean
}

const records = ref<CallRecord[]>([])
const loading = ref(false)
const detailVisible = ref(false)
const detailRecord = ref<CallRecord | null>(null)

function formatDate(d: string): string {
  if (!d) return ''
  return new Date(d).toLocaleString('zh-CN')
}

function formatBytes(bytes: number): string {
  if (!bytes && bytes !== 0) return '-'
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function formatDuration(ms: number): string {
  if (!ms && ms !== 0) return '-'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  const min = Math.floor(ms / 60000)
  const sec = ((ms % 60000) / 1000).toFixed(0)
  return `${min}m${sec}s`
}

function formatJson(str: string): string {
  if (!str) return ''
  try {
    const parsed = JSON.parse(str)
    return JSON.stringify(parsed, null, 2)
  } catch {
    // 可能是SSE流式输出的原始数据（聚合失败时的回退）
    if (str.includes('data: ')) {
      return formatSSE(str)
    }
    return str
  }
}

function formatSSE(str: string): string {
  // 此函数仅在后端SSE聚合失败时作为回退使用
  const lines = str.split('\n')
  const result: string[] = []
  for (const line of lines) {
    if (line.startsWith('data: ')) {
      const data = line.slice(6)
      if (data === '[DONE]') {
        result.push('data: [DONE]')
      } else {
        try {
          const parsed = JSON.parse(data)
          result.push('data: ' + JSON.stringify(parsed, null, 2).split('\n').map((l, i) => i === 0 ? l : '      ' + l).join('\n'))
        } catch {
          result.push(line)
        }
      }
    } else {
      result.push(line)
    }
  }
  return result.join('\n')
}

async function copyJson(str: string) {
  if (!str) return
  const text = formatJson(str) || str
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制到剪贴板')
  } catch {
    ElMessage.error('复制失败')
  }
}

async function showDetail(row: CallRecord) {
  try {
    const res = await adminCallRecordApi.get(row.id)
    detailRecord.value = res.data
    detailVisible.value = true
  } catch (e) {
    // 如果单条查询失败，直接用列表数据展示
    detailRecord.value = row
    detailVisible.value = true
  }
}

async function fetchRecords() {
  loading.value = true
  try {
    const res = await adminCallRecordApi.list()
    records.value = res.data?.items || []
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  fetchRecords()
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

.clickable-table {
  cursor: pointer;
}

.json-panels {
  display: flex;
  gap: 16px;
}

.json-panel {
  flex: 1;
  min-width: 0;
}

.json-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.json-title {
  font-weight: 600;
  font-size: 14px;
  color: #303133;
}

.json-block {
  background: #f5f7fa;
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 12px 16px;
  max-height: 480px;
  overflow: auto;
}

.json-block pre {
  margin: 0;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
  color: #303133;
}
</style>
