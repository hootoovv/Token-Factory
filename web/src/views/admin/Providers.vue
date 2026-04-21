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

    <!-- 供应商列表 - Collapse 面板 -->
    <el-collapse v-if="providers.length > 0" v-model="activeCollapse" class="provider-collapse">
      <el-collapse-item v-for="provider in providers" :key="provider.id" :name="provider.id">
        <!-- 自定义标题 -->
        <template #title>
          <div class="collapse-title">
            <div class="collapse-title-left">
              <span class="provider-name">{{ provider.name }}</span>
              <span v-if="provider.description" class="provider-desc">{{ provider.description }}</span>
              <el-tag size="small" type="info" class="key-count">
                {{ (provider.api_keys || []).length }} 个 Key
              </el-tag>
              <div class="timeout-tags">
                <el-tag size="small" type="info">连接: {{ provider.connect_timeout }}s</el-tag>
                <el-tag size="small" type="warning">首Token: {{ provider.first_token_timeout }}s</el-tag>
                <el-tag size="small" type="">流Idle: {{ provider.stream_idle_timeout }}s</el-tag>
                <el-tag size="small" type="danger">总: {{ provider.timeout }}s</el-tag>
              </div>
            </div>
            <div class="collapse-title-right" @click.stop>
              <el-button size="small" text @click="editProvider(provider)">
                <el-icon>
                  <Edit />
                </el-icon> 编辑
              </el-button>
              <el-button size="small" text type="danger" @click="deleteProvider(provider)">
                <el-icon>
                  <Delete />
                </el-icon> 删除
              </el-button>
              <el-button size="small" text type="primary" @click="addAPIKeyRow(provider)">
                <el-icon>
                  <Plus />
                </el-icon> 添加 Key
              </el-button>
            </div>
          </div>
        </template>

        <!-- 展开内容：API Key 列表 -->
        <div class="apikey-list">
          <div v-if="(provider.api_keys || []).length === 0 && !hasNewKeyRow(provider.id)" class="empty-tip">
            暂无 API Key，点击"添加 Key"按钮添加
          </div>

          <!-- 已有的 API Key 行 -->
          <div v-for="ak in provider.api_keys || []" :key="ak.id" class="apikey-row"
            :class="{ 'apikey-disabled': ak.status === 'disabled' }">
            <div class="apikey-info">
              <el-tag :type="getStatusType(ak.status)" effect="dark" size="small" class="status-tag">
                {{ getStatusText(ak.status) }}
              </el-tag>
              <span class="apikey-name">{{ ak.name || '未命名' }}</span>
              <span class="apikey-value">{{ ak.api_key }}</span>
            </div>
            <div class="apikey-actions">
              <el-button v-if="ak.status !== 'disabled'" size="small" text type="warning"
                @click="toggleAPIKeyStatus(provider, ak, 'disabled')">
                <el-icon>
                  <Switch />
                </el-icon> 禁用
              </el-button>
              <el-button v-if="ak.status === 'disabled'" size="small" text type="success"
                @click="toggleAPIKeyStatus(provider, ak, 'active')">
                <el-icon>
                  <Open />
                </el-icon> 启用
              </el-button>
              <el-button size="small" text @click="editAPIKey(provider, ak)">
                <el-icon>
                  <Edit />
                </el-icon> 编辑
              </el-button>
              <el-button size="small" text type="danger" @click="deleteAPIKey(provider, ak)">
                <el-icon>
                  <Delete />
                </el-icon> 删除
              </el-button>
            </div>
          </div>

          <!-- 新增 API Key 行 -->
          <div v-if="hasNewKeyRow(provider.id)" class="apikey-row new-key-row">
            <el-input v-model="newKeyData[provider.id].name" placeholder="备注名称" class="key-name-input" />
            <el-input v-model="newKeyData[provider.id].api_key" placeholder="输入 API Key" type="password" show-password
              class="key-value-input" />
            <el-select v-model="newKeyData[provider.id].status" class="key-status-select">
              <el-option label="工作中" value="active" />
              <el-option label="冷却中" value="cooldown" />
              <el-option label="欠费" value="arrears" />
              <el-option label="已禁用" value="disabled" />
            </el-select>
            <el-button size="small" type="primary" @click="submitNewAPIKey(provider.id)">确认</el-button>
            <el-button size="small" :loading="testingNewKeyId === provider.id"
              @click="testNewAPIKey(provider.id)">测试</el-button>
            <el-button size="small" @click="cancelNewKeyRow(provider.id)">取消</el-button>
          </div>

          <!-- 测试结果展示 -->
          <div v-if="testResult && testResultProviderId === provider.id" class="test-result-section">
            <div class="test-result" :class="testResult.success ? 'test-success' : 'test-fail'">
              <div class="test-result-header">
                <el-icon v-if="testResult.success" style="color: #67c23a; margin-right: 6px;">
                  <SuccessFilled />
                </el-icon>
                <el-icon v-else style="color: #f56c6c; margin-right: 6px;">
                  <CircleCloseFilled />
                </el-icon>
                <span class="test-result-title">{{ testResult.success ? '连接成功' : '连接失败' }}</span>
                <span v-if="testResult.latency" class="test-latency">{{ testResult.latency }}ms</span>
              </div>
              <div v-if="testResult.success && testResult.message" class="test-message">{{ testResult.message }}</div>
              <div v-if="!testResult.success && testResult.error" class="test-message">{{ testResult.error }}</div>
              <div v-if="testResult.warning" class="test-warning">
                <el-icon style="margin-right: 4px;">
                  <WarningFilled />
                </el-icon>
                {{ testResult.warning }}
              </div>
              <div v-if="testResult.success && testResult.models && testResult.models.length > 0" class="test-models">
                <div class="test-models-header">可用模型 ({{ testResult.model_count }})：</div>
                <div class="test-models-list">
                  <el-tag v-for="model in testResult.models.slice(0, 50)" :key="model" size="small" type="info"
                    class="model-tag">
                    {{ model }}
                  </el-tag>
                  <el-tag v-if="testResult.models.length > 50" size="small" type="warning" class="model-tag">
                    ...还有 {{ testResult.models.length - 50 }} 个模型
                  </el-tag>
                </div>
              </div>
            </div>
          </div>
        </div>
      </el-collapse-item>
    </el-collapse>

    <!-- 空状态 -->
    <el-empty v-if="providers.length === 0" description="暂无供应商，点击右上角新建供应商" />

    <!-- 分页 -->
    <div class="pagination-wrapper">
      <el-pagination v-model:current-page="currentPage" v-model:page-size="pageSize" :total="total"
        :page-sizes="[10, 20, 50, 100]" layout="total, sizes, prev, pager, next" @size-change="fetchProviders"
        @current-change="fetchProviders" />
    </div>

    <!-- 创建/编辑供应商对话框 -->
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

        <el-divider content-position="left">超时配置（秒）</el-divider>

        <el-form-item label="总超时">
          <el-input-number v-model="form.timeout" :min="10" :max="1800" :step="30" />
          <span class="form-hint">请求发送到响应完成的绝对最大时间（默认300s）</span>
        </el-form-item>
        <el-form-item label="连接超时">
          <el-input-number v-model="form.connect_timeout" :min="1" :max="60" />
          <span class="form-hint">TCP+TLS握手完成的最大时间（默认10s）</span>
        </el-form-item>
        <el-form-item label="首Token超时">
          <el-input-number v-model="form.first_token_timeout" :min="1" :max="300" :step="5" />
          <span class="form-hint">从请求发送完毕到收到第一个字节的时间（默认30s）</span>
        </el-form-item>
        <el-form-item label="流Idle超时">
          <el-input-number v-model="form.stream_idle_timeout" :min="1" :max="120" />
          <span class="form-hint">流式响应中两次数据传输之间的最大空闲时间（默认15s）</span>
        </el-form-item>

        <el-divider />

        <el-form-item label="重试次数">
          <el-input-number v-model="form.retry" :min="0" :max="10" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitForm">确认</el-button>
      </template>
    </el-dialog>

    <!-- 编辑 API Key 对话框 -->
    <el-dialog v-model="apiKeyDialogVisible" title="编辑 API Key" width="500px">
      <el-form ref="apiKeyFormRef" :model="apiKeyForm" label-width="100px">
        <el-form-item label="备注名称">
          <el-input v-model="apiKeyForm.name" placeholder="如：主用Key、备用Key" />
        </el-form-item>
        <el-form-item label="API Key">
          <el-input v-model="apiKeyForm.api_key" type="password" show-password placeholder="留空则不修改" />
          <span class="form-hint">留空表示不修改现有密钥</span>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="apiKeyForm.status">
            <el-option label="工作中" value="active" />
            <el-option label="冷却中" value="cooldown" />
            <el-option label="欠费" value="arrears" />
            <el-option label="已禁用" value="disabled" />
          </el-select>
          <span class="form-hint">禁用后该Key不再参与代理请求</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="apiKeyDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitAPIKeyForm">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { adminProviderApi, adminProviderAPIKeyApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import { SuccessFilled, CircleCloseFilled, WarningFilled, Switch, Open } from '@element-plus/icons-vue'
import type { FormInstance } from 'element-plus'

const providers = ref<any[]>([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

// Collapse 展开状态
const activeCollapse = ref<number[]>([])

// 供应商对话框
const dialogVisible = ref(false)
const isEdit = ref(false)
const editId = ref(0)
const formRef = ref<FormInstance>()

const form = ref({
  name: '',
  description: '',
  base_url: '',
  timeout: 300,
  connect_timeout: 10,
  first_token_timeout: 30,
  stream_idle_timeout: 15,
  retry: 3,
})

const formRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  base_url: [{ required: true, message: '请输入Base URL', trigger: 'blur' }],
}

// 新增 API Key 行状态
const newKeyData = reactive<Record<number, { name: string; api_key: string; status: string }>>({})

// 编辑 API Key 对话框
const apiKeyDialogVisible = ref(false)
const apiKeyFormRef = ref<FormInstance>()
const editingAPIKey = ref<any>(null)
const editingProviderId = ref(0)
const apiKeyForm = ref({
  name: '',
  api_key: '',
  status: 'active',
})

// 测试连接相关
const testingKeyId = ref<number | null>(null)
const testingNewKeyId = ref<number | null>(null) // 新增 API Key 的测试状态（用 providerId 标识）
const testResult = ref<any>(null)
const testResultProviderId = ref<number | null>(null)

function getStatusType(status: string): string {
  switch (status) {
    case 'active': return 'success'
    case 'cooldown': return 'warning'
    case 'arrears': return 'danger'
    case 'disabled': return 'info'
    default: return 'info'
  }
}

function getStatusText(status: string): string {
  switch (status) {
    case 'active': return '工作中'
    case 'cooldown': return '冷却中'
    case 'arrears': return '欠费'
    case 'disabled': return '已禁用'
    default: return status
  }
}

function hasNewKeyRow(providerId: number): boolean {
  return providerId in newKeyData
}

async function fetchProviders() {
  try {
    const res = await adminProviderApi.list({ page: currentPage.value, page_size: pageSize.value })
    providers.value = res.data.items || []
    total.value = res.data.total || 0
    // 默认展开所有面板
    activeCollapse.value = providers.value.map((p: any) => p.id)
    // 清空新增行状态
    Object.keys(newKeyData).forEach(key => {
      delete newKeyData[Number(key)]
    })
  } catch (e) {
    console.error(e)
  }
}

// ============ 供应商 CRUD ============

function showCreateDialog() {
  isEdit.value = false
  form.value = {
    name: '',
    description: '',
    base_url: '',
    timeout: 300,
    connect_timeout: 10,
    first_token_timeout: 30,
    stream_idle_timeout: 15,
    retry: 3,
  }
  dialogVisible.value = true
}

function editProvider(provider: any) {
  isEdit.value = true
  editId.value = provider.id
  form.value = {
    name: provider.name,
    description: provider.description || '',
    base_url: provider.base_url,
    timeout: provider.timeout || 300,
    connect_timeout: provider.connect_timeout || 10,
    first_token_timeout: provider.first_token_timeout || 30,
    stream_idle_timeout: provider.stream_idle_timeout || 15,
    retry: provider.retry ?? 3,
  }
  dialogVisible.value = true
}

async function submitForm() {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      if (isEdit.value) {
        await adminProviderApi.update(editId.value, form.value)
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
    await ElMessageBox.confirm(
      `确定删除供应商 "${provider.name}" 吗？相关的 API Key 和模型映射也将被删除。`,
      '确认删除',
      { type: 'warning' }
    )
    await adminProviderApi.delete(provider.id)
    ElMessage.success('删除成功')
    activeCollapse.value = activeCollapse.value.filter(id => id !== provider.id)
    fetchProviders()
  } catch (e) {
    // 取消
  }
}

// ============ API Key 操作 ============

function addAPIKeyRow(provider: any) {
  // 如果未展开，自动展开
  if (!activeCollapse.value.includes(provider.id)) {
    activeCollapse.value.push(provider.id)
  }
  // 根据已有 API Key 数量生成默认名称
  const existingCount = (provider.api_keys || []).length
  const defaultName = `Key${existingCount + 1}`
  newKeyData[provider.id] = {
    name: defaultName,
    api_key: '',
    status: 'active',
  }
}

function cancelNewKeyRow(providerId: number) {
  delete newKeyData[providerId]
  // 关闭新增行时同步隐藏该供应商的测试结果
  if (testResultProviderId.value === providerId) {
    testResult.value = null
    testResultProviderId.value = null
  }
}

async function submitNewAPIKey(providerId: number) {
  const row = newKeyData[providerId]
  if (!row) return
  if (!row.api_key.trim()) {
    ElMessage.warning('请输入 API Key')
    return
  }

  try {
    await adminProviderAPIKeyApi.create({
      provider_id: providerId,
      api_key: row.api_key.trim(),
      name: row.name.trim() || undefined,
      status: row.status,
    })
    ElMessage.success('API Key 添加成功')
    delete newKeyData[providerId]
    // 保存后同步隐藏该供应商的测试结果
    if (testResultProviderId.value === providerId) {
      testResult.value = null
      testResultProviderId.value = null
    }
    fetchProviders()
  } catch (e) {
    // 错误已处理
  }
}

function editAPIKey(provider: any, ak: any) {
  editingAPIKey.value = ak
  editingProviderId.value = provider.id
  apiKeyForm.value = {
    name: ak.name || '',
    api_key: '',
    status: ak.status || 'active',
  }
  apiKeyDialogVisible.value = true
}

async function submitAPIKeyForm() {
  if (!editingAPIKey.value) return
  try {
    const updateData: any = {
      name: apiKeyForm.value.name,
      status: apiKeyForm.value.status,
    }
    if (apiKeyForm.value.api_key.trim()) {
      updateData.api_key = apiKeyForm.value.api_key.trim()
    }
    await adminProviderAPIKeyApi.update(editingAPIKey.value.id, updateData)
    ElMessage.success('API Key 更新成功')
    apiKeyDialogVisible.value = false
    fetchProviders()
  } catch (e) {
    // 错误已处理
  }
}

async function toggleAPIKeyStatus(provider: any, ak: any, newStatus: string) {
  const action = newStatus === 'disabled' ? '禁用' : '启用'
  try {
    await ElMessageBox.confirm(
      `确定${action} API Key "${ak.name || '未命名'}" 吗？${newStatus === 'disabled' ? '禁用后该Key将不再参与代理请求，正在进行的请求将被立即中断。' : '启用后该Key将重新参与代理请求。'}`,
      `确认${action}`,
      { type: 'warning' }
    )
    await adminProviderAPIKeyApi.update(ak.id, { status: newStatus })
    ElMessage.success(`API Key 已${action}`)
    fetchProviders()
  } catch (e) {
    // 取消或错误
  }
}

async function deleteAPIKey(provider: any, ak: any) {
  try {
    await ElMessageBox.confirm(
      `确定删除 API Key "${ak.name || '未命名'}" 吗？`,
      '确认删除',
      { type: 'warning' }
    )
    await adminProviderAPIKeyApi.delete(ak.id)
    ElMessage.success('删除成功')
    fetchProviders()
  } catch (e) {
    // 取消
  }
}

async function testAPIKey(provider: any, ak: any) {
  testingKeyId.value = ak.id
  testResult.value = null
  testResultProviderId.value = provider.id

  try {
    const res = await adminProviderAPIKeyApi.test({
      provider_id: provider.id,
      provider_api_key_id: ak.id,
    })
    testResult.value = res.data
  } catch (e: any) {
    testResult.value = {
      success: false,
      error: e.response?.data?.error || '请求失败，请检查网络连接',
    }
  } finally {
    testingKeyId.value = null
  }
}

// 测试新增 API Key（尚未保存的密钥，直接发送明文测试）
async function testNewAPIKey(providerId: number) {
  const row = newKeyData[providerId]
  if (!row) return
  if (!row.api_key.trim()) {
    ElMessage.warning('请先输入 API Key 再测试')
    return
  }

  testingNewKeyId.value = providerId
  testResult.value = null
  testResultProviderId.value = providerId

  try {
    const res = await adminProviderAPIKeyApi.test({
      provider_id: providerId,
      api_key: row.api_key.trim(),
    })
    testResult.value = res.data
  } catch (e: any) {
    testResult.value = {
      success: false,
      error: e.response?.data?.error || '请求失败，请检查网络连接',
    }
  } finally {
    testingNewKeyId.value = null
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

.form-hint {
  margin-left: 12px;
  font-size: 12px;
  color: #999;
}

/* Collapse 面板样式 */
.provider-collapse {
  border: none;
}

.provider-collapse :deep(.el-collapse-item__header) {
  height: auto;
  min-height: 48px;
  line-height: 1.5;
  padding: 8px 0;
  align-items: center;
}

.provider-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}

.provider-collapse :deep(.el-collapse-item__content) {
  padding-bottom: 12px;
}

.collapse-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  padding-right: 12px;
  gap: 12px;
}

.collapse-title-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
  flex-wrap: wrap;
}

.provider-name {
  font-weight: 600;
  font-size: 18px;
  color: #303133;
  white-space: nowrap;
}

.provider-desc {
  color: #909399;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 200px;
}

.key-count {
  flex-shrink: 0;
}

.timeout-tags {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.collapse-title-right {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

/* API Key 列表样式 */
.apikey-list {
  padding: 4px 0;
}

.empty-tip {
  color: #909399;
  font-size: 13px;
  padding: 12px 0;
  text-align: center;
}

.apikey-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-radius: 6px;
  transition: background-color 0.2s;
}

.apikey-row:hover {
  background-color: #f5f7fa;
}

.apikey-disabled {
  opacity: 0.5;
  background-color: #f5f5f5;
}

.apikey-info {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

.status-tag {
  flex-shrink: 0;
}

.apikey-name {
  font-weight: 500;
  font-size: 14px;
  color: #303133;
  min-width: 80px;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.apikey-value {
  font-family: monospace;
  font-size: 13px;
  color: #909399;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 300px;
}

.apikey-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.key-name-input {
  width: 160px;
  flex-shrink: 0;
}

.key-value-input {
  flex: 1;
  min-width: 200px;
}

.key-status-select {
  width: 120px;
  flex-shrink: 0;
}

.new-key-row {
  margin-top: 8px;
  border-top: 1px dashed #dcdfe6;
  padding-top: 12px;
}

/* 测试结果 */
.test-result-section {
  margin-top: 8px;
  padding: 0 12px;
}

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
