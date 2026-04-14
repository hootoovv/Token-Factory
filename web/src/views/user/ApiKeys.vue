<template>
  <div>
    <div class="page-header">
      <h3>API密钥管理</h3>
      <el-button type="primary" @click="createKey">
        <el-icon><Plus /></el-icon> 创建密钥
      </el-button>
    </div>

    <el-alert type="info" :closable="false" style="margin-bottom: 16px;">
      API密钥用于通过代理端口(11444)调用大模型API。请在请求的Authorization头中携带密钥：<code>Authorization: Bearer tk-xxxx</code>
    </el-alert>

    <el-table :data="apiKeys" stripe>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" width="150" />
      <el-table-column prop="key" label="密钥" min-width="300">
        <template #default="scope">
          <div class="key-cell">
            <code class="key-text">{{ scope.row.showFull ? scope.row.key : maskKey(scope.row.key) }}</code>
            <el-button text size="small" @click="scope.row.showFull = !scope.row.showFull">
              {{ scope.row.showFull ? '隐藏' : '显示' }}
            </el-button>
            <el-button text size="small" type="primary" @click="copyKey(scope.row.key)">
              <el-icon><CopyDocument /></el-icon> 复制
            </el-button>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="scope">
          <el-tag :type="scope.row.status === 'active' ? 'success' : 'danger'" size="small">
            {{ scope.row.status === 'active' ? '活跃' : '已禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="180">
        <template #default="scope">
          {{ formatDate(scope.row.created_at) }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="scope">
          <el-button size="small" type="danger" @click="deleteKey(scope.row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- 创建对话框 -->
    <el-dialog v-model="createDialogVisible" title="创建API密钥" width="520px">
      <el-form label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="newKeyName" placeholder="如: 生产环境密钥" />
        </el-form-item>
        <el-form-item label="API密钥">
          <div class="key-generate-row">
            <el-input v-model="newKeyValue" readonly class="key-input-readonly" />
            <el-button type="primary" plain @click="regenerateKey">
              <el-icon><Refresh /></el-icon> 重新生成
            </el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitCreate">确认创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { userApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'

const apiKeys = ref<any[]>([])
const createDialogVisible = ref(false)
const newKeyName = ref('')
const newKeyValue = ref('')

// 生成 tk- 前缀 + 32位随机字符（大小写字母、数字、-、_）
const KEY_CHARS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
function generateRandomKey(): string {
  const arr = new Uint8Array(32)
  crypto.getRandomValues(arr)
  let result = 'tk-'
  for (let i = 0; i < 32; i++) {
    result += KEY_CHARS[arr[i] % KEY_CHARS.length]
  }
  return result
}

function regenerateKey() {
  newKeyValue.value = generateRandomKey()
}

function maskKey(key: string): string {
  if (!key || key.length < 10) return key
  return key.substring(0, 7) + '...' + key.substring(key.length - 4)
}

function formatDate(d: string): string {
  if (!d) return ''
  return new Date(d).toLocaleString('zh-CN')
}

async function fetchKeys() {
  try {
    const res = await userApi.listApiKeys()
    apiKeys.value = (res.data || []).map((k: any) => ({ ...k, showFull: false }))
  } catch (e) {
    console.error(e)
  }
}

function createKey() {
  newKeyName.value = ''
  newKeyValue.value = generateRandomKey()
  createDialogVisible.value = true
}

async function submitCreate() {
  try {
    await userApi.createApiKey({ name: newKeyName.value || undefined, key: newKeyValue.value })
    ElMessage.success('密钥创建成功')
    createDialogVisible.value = false
    fetchKeys()
  } catch (e) { /* 已处理 */ }
}

async function deleteKey(key: any) {
  try {
    await ElMessageBox.confirm('确定删除此密钥吗？删除后使用该密钥的请求将被拒绝。', '确认删除', { type: 'warning' })
    await userApi.deleteApiKey(key.id)
    ElMessage.success('密钥已删除')
    fetchKeys()
  } catch (e) { /* 取消 */ }
}

async function copyKey(key: string) {
  try {
    await navigator.clipboard.writeText(key)
    ElMessage.success('已复制到剪贴板')
  } catch (e) {
    ElMessage.warning('复制失败，请手动复制')
  }
}

onMounted(() => {
  fetchKeys()
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
.key-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.key-text {
  font-family: 'Courier New', monospace;
  font-size: 13px;
  background: #f5f7fa;
  padding: 2px 8px;
  border-radius: 4px;
}
.key-generate-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.key-input-readonly {
  flex: 1;
}
.key-input-readonly :deep(.el-input__inner) {
  font-family: 'Courier New', monospace;
  font-size: 13px;
  color: #606266;
  cursor: default;
  background: #f5f7fa;
}
</style>