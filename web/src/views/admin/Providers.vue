<template>
  <div>
    <div class="page-header">
      <h3>供应商管理</h3>
      <el-button type="primary" @click="showCreateDialog">
        <el-icon>
          <Plus />
        </el-icon> 新建供应商
      </el-button>
    </div>

    <el-table :data="providers" stripe>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" width="180" />
      <el-table-column prop="description" label="描述" min-width="100" show-overflow-tooltip />
      <el-table-column prop="base_url" label="Base URL" min-width="150" show-overflow-tooltip />
      <el-table-column prop="api_key" label="API Key" min-width="100" show-overflow-tooltip />
      <el-table-column label="超时配置" width="360">
        <template #default="scope">
          <div class="timeout-info">
            <el-tag size="small" type="info">连接: {{ scope.row.connect_timeout }}s</el-tag>
            <el-tag size="small" type="warning">首Token: {{ scope.row.first_token_timeout }}s</el-tag>
            <el-tag size="small" type="">流Idle: {{ scope.row.stream_idle_timeout }}s</el-tag>
            <el-tag size="small" type="danger">总: {{ scope.row.timeout }}s</el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="retry" label="重试" width="70" />
      <el-table-column prop="status" label="状态" width="100">
        <template #default="scope">
          <el-tag :type="getStatusType(scope.row.status)" size="small" effect="dark">
            {{ getStatusText(scope.row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200">
        <template #default="scope">
          <el-button size="small" @click="editProvider(scope.row)">编辑</el-button>
          <el-button size="small" type="danger" @click="deleteProvider(scope.row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-wrapper">
      <el-pagination v-model:current-page="currentPage" v-model:page-size="pageSize" :total="total"
        :page-sizes="[10, 20, 50, 100]" layout="total, sizes, prev, pager, next" @size-change="fetchProviders"
        @current-change="fetchProviders" />
    </div>

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑供应商' : '新建供应商'" width="640px">
      <el-form ref="formRef" :model="form" :rules="formRules" label-width="120px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如: OpenAI, Anthropic" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="Base URL" prop="base_url">
          <el-input v-model="form.base_url" placeholder="如: https://api.openai.com" />
        </el-form-item>
        <el-form-item label="API Key" prop="api_key">
          <el-input v-model="form.api_key" type="password" show-password @input="onApiKeyInput" />
        </el-form-item>

        <!-- 测试连接按钮和结果 -->
        <el-form-item label="连接测试">
          <div class="test-section">
            <el-button
              type="warning"
              :loading="testingProvider"
              :disabled="!form.base_url || !form.api_key"
              @click="testProvider"
            >
              {{ testingProvider ? '测试中...' : '测试连接' }}
            </el-button>
            <span v-if="!form.base_url || !form.api_key" class="test-hint">
              请先填写 Base URL 和 API Key
            </span>
          </div>
        </el-form-item>

        <!-- 测试结果展示 -->
        <el-form-item v-if="testResult" label="">
          <div class="test-result" :class="testResult.success ? 'test-success' : 'test-fail'">
            <div class="test-result-header">
              <el-icon v-if="testResult.success" style="color: #67c23a; margin-right: 6px;"><SuccessFilled /></el-icon>
              <el-icon v-else style="color: #f56c6c; margin-right: 6px;"><CircleCloseFilled /></el-icon>
              <span class="test-result-title">{{ testResult.success ? '连接成功' : '连接失败' }}</span>
              <span v-if="testResult.latency" class="test-latency">{{ testResult.latency }}ms</span>
            </div>
            <div v-if="testResult.success && testResult.message" class="test-message">
              {{ testResult.message }}
            </div>
            <div v-if="!testResult.success && testResult.error" class="test-message">
              {{ testResult.error }}
            </div>
            <div v-if="testResult.warning" class="test-warning">
              <el-icon style="margin-right: 4px;"><WarningFilled /></el-icon>
              {{ testResult.warning }}
            </div>
            <!-- 模型列表展示 -->
            <div v-if="testResult.success && testResult.models && testResult.models.length > 0" class="test-models">
              <div class="test-models-header">
                可用模型 ({{ testResult.model_count }})：
              </div>
              <div class="test-models-list">
                <el-tag
                  v-for="model in testResult.models.slice(0, 50)"
                  :key="model"
                  size="small"
                  type="info"
                  class="model-tag"
                >
                  {{ model }}
                </el-tag>
                <el-tag v-if="testResult.models.length > 50" size="small" type="warning" class="model-tag">
                  ...还有 {{ testResult.models.length - 50 }} 个模型
                </el-tag>
              </div>
            </div>
          </div>
        </el-form-item>

        <el-divider content-position="left">超时配置（秒）</el-divider>

        <el-form-item label="总超时">
          <el-input-number v-model="form.timeout" :min="10" :max="1800" :step="30" />
          <span class="form-hint">请求发送到响应完成的绝对最大时间（默认300s，长文本生成需设长）</span>
        </el-form-item>
        <el-form-item label="连接超时">
          <el-input-number v-model="form.connect_timeout" :min="1" :max="60" />
          <span class="form-hint">TCP+TLS握手完成的最大时间（默认10s，连接不可达时快速失败）</span>
        </el-form-item>
        <el-form-item label="首Token超时">
          <el-input-number v-model="form.first_token_timeout" :min="1" :max="300" :step="5" />
          <span class="form-hint">从请求发送完毕到收到第一个字节的时间（默认120s，超时自动重试其他供应商）</span>
        </el-form-item>
        <el-form-item label="流Idle超时">
          <el-input-number v-model="form.stream_idle_timeout" :min="1" :max="120" />
          <span class="form-hint">流式响应中两次数据传输之间的最大空闲时间（默认60s，检测上游卡死）</span>
        </el-form-item>

        <el-divider />

        <el-form-item label="重试次数">
          <el-input-number v-model="form.retry" :min="0" :max="10" />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status">
            <el-option label="工作中" value="active" />
            <el-option label="冷却中" value="cooldown" />
            <el-option label="欠费" value="arrears" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitForm">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminProviderApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import { SuccessFilled, CircleCloseFilled, WarningFilled } from '@element-plus/icons-vue'
import type { FormInstance } from 'element-plus'

const providers = ref<any[]>([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const dialogVisible = ref(false)
const isEdit = ref(false)
const editId = ref(0)
const formRef = ref<FormInstance>()

// 测试连接相关状态
const testingProvider = ref(false)
const testResult = ref<any>(null)

// 跟踪API Key是否被用户修改过（编辑模式下区分脱敏值和新输入值）
const apiKeyModified = ref(false)

const form = ref({
  name: '',
  description: '',
  base_url: '',
  api_key: '',
  timeout: 300,
  connect_timeout: 10,
  first_token_timeout: 120,
  stream_idle_timeout: 60,
  retry: 1,
  status: 'active',
})

const formRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  base_url: [{ required: true, message: '请输入Base URL', trigger: 'blur' }],
  api_key: [{ required: true, message: '请输入API Key', trigger: 'blur' }],
}

function getStatusType(status: string): string {
  switch (status) {
    case 'active': return 'success'
    case 'cooldown': return 'warning'
    case 'arrears': return 'danger'
    default: return 'info'
  }
}

function getStatusText(status: string): string {
  switch (status) {
    case 'active': return '工作中'
    case 'cooldown': return '冷却中'
    case 'arrears': return '欠费'
    default: return status
  }
}

// API Key输入变化时标记为已修改（编辑模式下用于区分脱敏值和新输入值）
function onApiKeyInput() {
  apiKeyModified.value = true
}

async function fetchProviders() {
  try {
    const res = await adminProviderApi.list({ page: currentPage.value, page_size: pageSize.value })
    providers.value = res.data.items || []
    total.value = res.data.total || 0
  } catch (e) {
    console.error(e)
  }
}

function showCreateDialog() {
  isEdit.value = false
  apiKeyModified.value = false
  form.value = {
    name: '',
    description: '',
    base_url: '',
    api_key: '',
    timeout: 300,
    connect_timeout: 10,
    first_token_timeout: 120,
    stream_idle_timeout: 60,
    retry: 1,
    status: 'active',
  }
  testResult.value = null
  dialogVisible.value = true
}

function editProvider(provider: any) {
  isEdit.value = true
  editId.value = provider.id
  apiKeyModified.value = false
  form.value = {
    name: provider.name,
    description: provider.description || '',
    base_url: provider.base_url,
    api_key: provider.api_key || '',
    timeout: provider.timeout || 300,
    connect_timeout: provider.connect_timeout || 10,
    first_token_timeout: provider.first_token_timeout || 120,
    stream_idle_timeout: provider.stream_idle_timeout || 60,
    retry: provider.retry || 0,
    status: provider.status || 'active',
  }
  testResult.value = null
  dialogVisible.value = true
}

// 测试供应商连接
async function testProvider() {
  if (!form.value.base_url || !form.value.api_key) {
    ElMessage.warning('请先填写 Base URL 和 API Key')
    return
  }

  testingProvider.value = true
  testResult.value = null

  try {
    const testData: { base_url: string; api_key: string; id?: number } = {
      base_url: form.value.base_url,
      api_key: form.value.api_key,
    }
    // 编辑模式下传递供应商ID，后端可据此获取真实API Key（当api_key为脱敏值时）
    if (isEdit.value && editId.value) {
      testData.id = editId.value
    }
    const res = await adminProviderApi.test(testData)
    testResult.value = res.data
  } catch (e: any) {
    testResult.value = {
      success: false,
      error: e.response?.data?.error || '请求失败，请检查网络连接',
    }
  } finally {
    testingProvider.value = false
  }
}

async function submitForm() {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      if (isEdit.value) {
        // 编辑模式下，构建更新数据
        const updateData: any = { ...form.value }
        // 如果API Key没有被用户修改（仍为脱敏值），不传api_key字段，避免覆盖数据库中的真实密钥
        if (!apiKeyModified.value && form.value.api_key.includes('****')) {
          delete updateData.api_key
        }
        await adminProviderApi.update(editId.value, updateData)
        ElMessage.success('更新成功')
      } else {
        await adminProviderApi.create(form.value)
        ElMessage.success('创建成功')
      }
      dialogVisible.value = false
      fetchProviders()
    } catch (e) {
      // 错误已处理
    }
  })
}

async function deleteProvider(provider: any) {
  try {
    await ElMessageBox.confirm(`确定删除供应商 "${provider.name}" 吗？相关的模型映射也将被删除。`, '确认删除', {
      type: 'warning',
    })
    await adminProviderApi.delete(provider.id)
    ElMessage.success('删除成功')
    fetchProviders()
  } catch (e) {
    // 取消
  }
}

onMounted(() => {
  fetchProviders()
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

.pagination-wrapper {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.timeout-info {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  font-size: 12px;
  color: #666;
}

.form-hint {
  margin-left: 12px;
  font-size: 12px;
  color: #999;
}

/* 测试连接区域 */
.test-section {
  display: flex;
  align-items: center;
  gap: 12px;
}

.test-hint {
  font-size: 12px;
  color: #999;
}

/* 测试结果 */
.test-result {
  width: 100%;
  padding: 12px 16px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.6;
}

.test-success {
  background-color: #f0f9eb;
  border: 1px solid #e1f3d8;
}

.test-fail {
  background-color: #fef0f0;
  border: 1px solid #fde2e2;
}

.test-result-header {
  display: flex;
  align-items: center;
  font-weight: 600;
  margin-bottom: 4px;
}

.test-result-title {
  margin-right: 12px;
}

.test-latency {
  font-size: 12px;
  color: #909399;
  font-weight: normal;
}

.test-message {
  color: #606266;
  margin-bottom: 4px;
}

.test-warning {
  color: #e6a23c;
  display: flex;
  align-items: center;
  margin-bottom: 4px;
}

.test-models {
  margin-top: 8px;
}

.test-models-header {
  font-weight: 600;
  margin-bottom: 6px;
  color: #606266;
}

.test-models-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
}

.model-tag {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
